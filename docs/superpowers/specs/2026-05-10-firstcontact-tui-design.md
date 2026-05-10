# First Contact wizard — Bubble Tea TUI

**Date**: 2026-05-10
**Status**: Approved (pending implementation)
**Target**: dev (the next release after the wizard ships in PR #32)
**Scope**: Replace `internal/firstcontact/render/cli.go`'s plain-stdio implementation with a [Bubble Tea](https://github.com/charmbracelet/bubbletea)-based TUI for the wizard's interactive surface, while keeping the existing CLI as a non-TTY fallback. Phase logic, claude wrapper, slug derivation, and ontology / wake / forge plumbing are unchanged.

This builds on `docs/superpowers/specs/2026-05-09-first-contact-wizard-design.md` (the wizard core) — that doc's §3.5 "Renderer interface" is the seam this design plugs into.

## 1. Problem statement

Manual smoke testing of the wizard surfaced three concurrent CLI deficiencies:

1. **No animated feedback during long operations.** Status spinners are a single ` · ...` line printed once; logo flickers crudely. While claude works for 30–90 seconds (research, displaying, calling-words, boot-wake response), the wizard looks frozen — operators can't tell if it's progressing or hung. Recent fixes added an elapsed-time tick but the renderer still cannot animate.
2. **Crude input UX.** `PromptChoice` reads numbered input ("type 1, 2, 3") instead of arrow-key navigation. Multi-line input ends with the unfamiliar blank-line + Enter convention. No inline editing of long inputs. Feels like a 1990s installer in 2026.
3. **No layout / typography polish.** Plain ANSI text without boxes, borders, or color hierarchy. The summoning-book preview dumps markdown verbatim. The agent's response renders without markdown styling. The narrative copy (already careful prose) feels misplaced because the frame around it is bare.

The reference points are the modern terminal UIs the operator already uses daily: Codex CLI and Claude Code. Both use Bubble Tea + the Charm widget stack. This doc adopts the same.

## 2. Design intent

Six commitments shape the rest:

1. **Bubble Tea + Charm stack.** `github.com/charmbracelet/bubbletea` (Elm-architecture state machine) + `bubbles` (text input, list, spinner, paginator, viewport) + `lipgloss` (styling, borders, gradients) + `glamour` (markdown rendering). What Codex CLI uses; the largest community and the best-maintained widget set.
2. **Renderer interface unchanged.** The existing `Renderer` (`Frame`, `Show`, `Typewriter`, `Prompt`, `PromptChoice`, `Status`, `Logo`, `Capabilities`) stays as the seam. Phase logic, claude wrapper, slug derivation, and ontology / wake / forge plumbing don't change. Zero regression risk on the wizard core.
3. **Goroutine + channel bridge.** Bubble Tea owns the main goroutine (its programming model requires it). Phase logic runs in a worker goroutine. `Renderer` method calls send `tea.Msg`-typed asks to the program and block on a reply channel; `Update` handles asks by switching the model into the appropriate input mode and sends the reply when the user submits.
4. **Style C: transcript, not alt-screen.** Inline scroll, warm bronze accent on phase rules and prompts, dimmer past-phases, no full-screen takeover. Matches the wizard's narrative-thread feel: the operator sees the ritual unfold in their terminal's history rather than in a modal overlay.
5. **CLI fallback preserved.** The current `cli.go` is kept verbatim and demoted to "fallback for non-TTY environments." `NewAuto(in, out, cps)` picks the TUI when stdout is a TTY ≥60 cols and `EIDOS_NO_TUI != "1"` and `CI` is unset; otherwise falls back to CLI. CI integration tests, piped-stdin scripted runs, and dumb terminals continue to work.
6. **No phase-logic changes.** This PR is purely a renderer swap. The `Renderer` interface and the `firstcontact.Run` driver are unchanged. The cmd-level wrapper in `cmd/eidos/summon/cmd.go` adapts to the new optional `RunWithPhases` entry point but otherwise its logic (claude credentials, contact-add, response-tail) is unchanged.

## 3. Architecture

### 3.1 New files

```
internal/firstcontact/render/
├── renderer.go           (unchanged) — interface
├── cli.go                (unchanged) — kept as fallback
├── factory.go            NEW — NewAuto(in, out, cps) Renderer
├── tui.go                NEW — tuiRenderer struct + RunWithPhases
├── tui_model.go          NEW — Bubble Tea Model + Update + View
├── tui_messages.go       NEW — typed Msgs for the bridge
├── tui_styles.go         NEW — lipgloss palette + styled snippets
└── tui_test.go           NEW — teatest-based Model + bridge tests
```

### 3.2 Capability detection

```go
// factory.go
func NewAuto(in io.Reader, out io.Writer, cps int) Renderer {
    if shouldUseTUI(out) {
        return NewTUI(in, out, cps)
    }
    return NewCLI(in, out, cps)
}

func shouldUseTUI(out io.Writer) bool {
    if os.Getenv("EIDOS_NO_TUI") == "1" {
        return false
    }
    if os.Getenv("CI") != "" {
        return false
    }
    if os.Getenv("TERM") == "dumb" {
        return false
    }
    f, ok := out.(*os.File)
    if !ok {
        return false
    }
    if !term.IsTerminal(int(f.Fd())) {
        return false
    }
    cols, _, err := term.GetSize(int(f.Fd()))
    if err != nil || cols < 60 {
        return false
    }
    return true
}
```

`cmd/eidos/summon/cmd.go`'s only diff is `render.NewCLI(...)` → `render.NewAuto(...)` plus the `TUIRenderer` extension call (§3.4).

### 3.3 Bridge channel

The bridge has two channel types:

```go
// tui_messages.go

// askMsg is the worker → TUI message: phase logic asks for input.
type askMsg struct {
    kind     askKind  // kindPrompt | kindPromptChoice | kindShow | kindTypewriter | ...
    question string
    opts     PromptOpts
    choices  []ChoiceOption
    body     string  // for Show / Typewriter / Frame
}

// replyMsg is the TUI → worker message: user submitted input.
type replyMsg struct {
    text string  // Prompt / PromptChoice→idx-as-string
    err  error
}
```

The `tuiRenderer` keeps a `*tea.Program` reference and the in-flight `replies chan replyMsg`:

```go
// tui.go
type tuiRenderer struct {
    prog    *tea.Program
    replies chan replyMsg
    cps     int
}

func (r *tuiRenderer) Prompt(question string, opts PromptOpts) (string, error) {
    r.prog.Send(askMsg{kind: kindPrompt, question: question, opts: opts})
    reply := <-r.replies
    return reply.text, reply.err
}

func (r *tuiRenderer) PromptChoice(q string, options []ChoiceOption) (int, error) {
    r.prog.Send(askMsg{kind: kindPromptChoice, question: q, choices: options})
    reply := <-r.replies
    if reply.err != nil {
        return 0, reply.err
    }
    var i int
    fmt.Sscanf(reply.text, "%d", &i)
    return i, nil
}

func (r *tuiRenderer) Show(text string) {
    r.prog.Send(askMsg{kind: kindShow, body: text})
    <-r.replies // wait for ack so the next call doesn't race with the render
}
func (r *tuiRenderer) Typewriter(ctx context.Context, text string) {
    r.prog.Send(askMsg{kind: kindTypewriter, body: text})
    <-r.replies // wait for ack — typewriter is synchronous from the worker's POV
}
func (r *tuiRenderer) Frame(content string) {
    r.prog.Send(askMsg{kind: kindFrame, body: content})
    <-r.replies
}
func (r *tuiRenderer) Status(message string) StatusHandle { ... see §3.5 ... }
func (r *tuiRenderer) Logo(ctx context.Context, d time.Duration) { ... see §3.6 ... }
func (r *tuiRenderer) Capabilities() Capabilities { return Capabilities{IsTTY: true, ANSI: true, Color: true} }
```

### 3.4 `RunWithPhases` (TUIRenderer extension interface)

Bubble Tea must run on the main goroutine. The plain `Renderer` interface can't express this — it would require every method call to round-trip through `tea.Program`, which doesn't help at the call-site level. Instead, the cmd-level wrapper checks for a `TUIRenderer` and delegates the lifecycle.

```go
// renderer.go (added)

// TUIRenderer is implemented by renderers whose lifecycle requires
// owning the main goroutine (Bubble Tea). Surfaces detect this via
// type-assertion and call RunWithPhases instead of running the phase
// driver directly.
type TUIRenderer interface {
    Renderer
    RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error
}
```

```go
// tui.go
func (r *tuiRenderer) RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error {
    workerErr := make(chan error, 1)
    workerCtx, cancel := context.WithCancel(ctx)
    defer cancel()

    go func() {
        defer func() {
            if rec := recover(); rec != nil {
                workerErr <- fmt.Errorf("phase logic panicked: %v", rec)
                r.prog.Send(workerDoneMsg{err: workerErr})
                return
            }
        }()
        err := fn(workerCtx, r)
        workerErr <- err
        r.prog.Send(workerDoneMsg{err: err})
    }()

    if _, err := r.prog.Run(); err != nil {
        cancel()
        // Drain the worker so its purge runs.
        return errors.Join(fmt.Errorf("tui: %w", err), <-workerErr)
    }
    cancel()
    close(r.replies)
    return <-workerErr
}
```

`cmd/eidos/summon/cmd.go` becomes:

```go
rend := render.NewAuto(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
if tui, ok := rend.(render.TUIRenderer); ok {
    return tui.RunWithPhases(ctx, func(ctx context.Context, r render.Renderer) error {
        deps.Renderer = r
        s, body, err := firstcontact.Run(ctx, deps)
        // post-processing (typewriter response + completion message) moves
        // into the TUI's last frame; see §4.
        _ = s; _ = body
        return err
    })
}
// CLI fallback path — phase logic runs directly:
deps.Renderer = rend
_, _, err := firstcontact.Run(ctx, deps)
return err
```

### 3.5 Status with live updates

Phase 3's response-wait emits `Update("Waiting for its reply... (15s)")` every 5 seconds (already in PR #32). The TUI Status implementation forwards each update to the model:

```go
type tuiStatusHandle struct {
    prog *tea.Program
    id   int  // unique per-Status; lets concurrent Statuses coexist
    once sync.Once
}

func (h *tuiStatusHandle) Update(message string) {
    h.prog.Send(updateStatusMsg{id: h.id, message: message})
}

func (h *tuiStatusHandle) Stop() {
    h.once.Do(func() {
        h.prog.Send(stopStatusMsg{id: h.id})
    })
}
```

The model holds an active spinner (`bubbles/spinner.Model`) and the current message. `Update(updateStatusMsg)` swaps the message; the spinner keeps animating on its own ticker.

### 3.6 Logo

Style C is a transcript — a 3-second rotating logo doesn't fit. The TUI's `Logo` implementation prints the static EIDOPSYCHE letter circle once at the top of phase 0, then continues. (The CLI fallback's `Logo` flicker stays.)

## 4. Visual identity

### 4.1 Palette (`tui_styles.go`)

```go
var (
    bronze    = lipgloss.Color("#c8946b")  // primary accent
    bronzeDim = lipgloss.Color("#7a5a44")  // dim accent
    fg        = lipgloss.Color("#d4cdc1")  // body text
    fgDim     = lipgloss.Color("#5d6370")  // metadata
    fgMuted   = lipgloss.Color("#3a3d44")  // separator rules
)

var (
    StyleAccent      = lipgloss.NewStyle().Foreground(bronze).Bold(true)
    StyleHint        = lipgloss.NewStyle().Foreground(fgDim)
    StylePromptChar  = lipgloss.NewStyle().Foreground(bronze).PaddingRight(1)
    StyleBody        = lipgloss.NewStyle().Foreground(fg)
    StylePhaseRule   = lipgloss.NewStyle().Foreground(fgMuted)
    StylePhaseLabel  = lipgloss.NewStyle().Foreground(bronze).Bold(true)
)
```

### 4.2 Phase frame (transcript style)

Top of each phase, rendered once:

```
eidos summon
─────────────────────────────────────

▎ phase 2 — the summoning book
```

Body content (claude paragraphs, prompts, status spinners) flows under the label. When phase 3 starts, it renders below phase 2's already-completed transcript — the operator can scroll up to re-read.

### 4.3 Per-element treatment

| Element | Bubbles widget | Notes |
|---|---|---|
| Single-line prompt | `bubbles/textinput` | bronze prompt char `›`; body in `fg` |
| Multi-line prompt | `bubbles/textarea` | submit on Esc-then-Enter (Codex CLI convention); hint on first line |
| Choice list | `bubbles/list` (compact, no border) | arrow keys + Enter; bronze on focused item |
| Status spinner | `bubbles/spinner` (`spinner.Dot`: `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) | bronze spinner + dim message |
| Typewriter | custom (`tea.Tick` per char) | body color, ~30 cps; cancellable |
| Logo | static print (no animation) | dim bronze |
| Section rule between phases | `─` × terminal width | `fgMuted` |
| Summoning-book preview | `bubbles/viewport` (paged scroll) | up/down to scroll; then `[s]` / `[q]` keystrokes |
| Agent response (final) | `glamour.Render(body)` | markdown styled with bronze accent; falls back to `StyleBody` if render fails |

### 4.4 Multi-line UX change

The CLI accepts multi-line input by reading until a blank line + Enter. `bubbles/textarea` has its own conventions: Enter inserts a newline, Esc-then-Enter (or `Ctrl+D`) submits. The TUI uses **Esc-then-Enter** (Codex CLI convention) and updates the on-screen hint:

- CLI: `(Answer freely; blank line to finish)`
- TUI: `(Press Esc then Enter when finished)`

Both languages get matching strings.

## 5. Lifecycle & error handling

### 5.1 Window resize

```go
case tea.WindowSizeMsg:
    m.width, m.height = msg.Width, msg.Height
    m.viewport.Width, m.viewport.Height = msg.Width, msg.Height-headerHeight
    m.textinput.Width = msg.Width - 4
```

Glamour-rendered markdown re-renders at the new width. Already-rendered transcript above doesn't reflow (Bubble Tea convention; the user just sees minor wrap).

### 5.2 Ctrl-C / cancellation

```go
case tea.KeyMsg:
    if msg.Type == tea.KeyCtrlC {
        m.cancelFn()  // cancels parent ctx; worker sees ctx.Done() at next ResponseWait poll
        return m, tea.Quit
    }
```

Worker's `firstcontact.Run` returns `ctx.Err()`; phase 3's `purge()` runs against `context.WithoutCancel(ctx)` (already in place) so the volume rollback survives the cancellation.

### 5.3 Bridge deadlock guards

| Failure mode | Fix |
|---|---|
| Worker dies before sending `replyMsg` (panic) | `defer recover()` in worker; converts panics to `replyMsg{err}`. |
| TUI quits while worker blocks on `<-replies` | `RunWithPhases` closes `r.replies` after `tea.Program.Run()` returns; worker reads zero-value `replyMsg{}` with `err = ErrTUIClosed` |
| Worker calls Show/Typewriter and racing ahead | Synchronous Show/Typewriter — they wait for an ack reply before returning. (Async-fire-and-forget would race with the next prompt.) |

### 5.4 Panic safety

`tea.NewProgram(initial, tea.WithContext(ctx), tea.WithoutCatchPanics())` — Bubble Tea's default is to recover panics, which would mask programmer errors and leave the wizard mid-render. We disable that and rely on the worker's deferred purge for cleanup.

If the TUI panics mid-phase and the worker has already created a volume, `RunWithPhases` returns the error, and the cmd-level wrapper logs + exits — but `firstcontact.Run`'s deferred cleanup (already in PR #32 via `purge` closure) tears down docker artifacts.

## 6. Testing

### 6.1 Existing tests stay valid

The `Renderer` interface is unchanged. None of these need edits:

- `internal/firstcontact/{slug,claude,ready,phase3_seal,run}_test.go`
- `internal/firstcontact/render/cli_test.go`

Zero regression risk on phase logic.

### 6.2 New tests

#### `tui_test.go` — Bubble Tea Model unit tests via `teatest`

`charmbracelet/x/exp/teatest` runs a `tea.Program` with simulated keystrokes against an in-memory I/O buffer, then asserts on the rendered output.

Coverage targets:

- Single-line `Prompt` returns the typed string on Enter
- Multi-line `Prompt` (textarea) submits on Esc-then-Enter
- `PromptChoice` arrow-down + Enter selects the second option
- `Status` lifecycle: `Status() → Update() → Stop()` produces the expected frame sequence
- Window resize (`tea.WindowSizeMsg`) reflows widgets without crashing
- Ctrl-C cancels the parent context and triggers `tea.Quit`

#### `tui_test.go` — Bridge integration test

```go
func TestTUI_BridgeRoundTrip(t *testing.T) {
    var got []string
    worker := func(ctx context.Context, r Renderer) error {
        if v, _ := r.Prompt("first?", PromptOpts{}); v != "" { got = append(got, "p:"+v) }
        if i, _ := r.PromptChoice("which?", []ChoiceOption{{Label: "a"}, {Label: "b"}}); i >= 0 {
            got = append(got, fmt.Sprintf("c:%d", i))
        }
        return nil
    }
    rend := newTUIRendererForTest( /* canned reply queue */ )
    if err := rend.RunWithPhases(context.Background(), worker); err != nil {
        t.Fatalf("RunWithPhases: %v", err)
    }
    if !reflect.DeepEqual(got, []string{"p:alice", "c:1"}) {
        t.Errorf("got %v", got)
    }
}
```

The harness substitutes a tiny "scripted reply" model for the real Bubble Tea Model — verifies the goroutine bridge plumbing without rendering.

#### `factory_test.go` — Capability detection

```go
func TestNewAuto_FallsBackOnPipe(t *testing.T)         { /* *bytes.Buffer → CLI */ }
func TestNewAuto_RespectsEIDOS_NO_TUI(t *testing.T)    { /* env → CLI */ }
func TestNewAuto_RespectsCI(t *testing.T)              { /* env → CLI */ }
func TestNewAuto_RespectsDumbTerm(t *testing.T)        { /* TERM=dumb → CLI */ }
```

### 6.3 Out of automated test scope

- Pixel-exact rendering of glamour markdown (depends on terminal renderer)
- Actual color output (lipgloss color codes are validated at unit level only)
- Real `bubbles` widgets internals (we trust the Charm libraries)
- Live claude calls (claude is faked everywhere, same as PR #32)

### 6.4 Manual smoke checklist (release-time)

For the design doc / CHANGELOG. Not automated.

1. `eidos summon` on a real terminal → wizard renders inline, bronze accent visible, arrow keys work for choices
2. Resize the terminal mid-phase → widgets reflow without garbage
3. Ctrl-C during the displaying-paragraph render → wizard exits cleanly with no leaked container/volume
4. `EIDOS_NO_TUI=1 eidos summon` → falls back to plain CLI
5. `eidos summon < /dev/null` → exits cleanly (CLI fallback handles EOF)
6. SSH into the host with a 50-column window → wizard auto-falls-back to CLI

## 7. Out of scope

- WebUI rendering (the wizard's spec marked it as deferred; the renderer interface is webui-ready).
- A full theming system — palette is hardcoded for now.
- Streaming claude output token-by-token. The wizard's typewriter renders the *complete* claude result; streaming would require switching to `claude --output-format stream-json` and rewriting the claude wrapper. Defer.
- Configurable typewriter rate (~30 cps constant for now).
- Any phase-logic changes — this is purely a renderer swap.

## 8. Migration

This is an additive change: the existing CLI renderer stays as the fallback. Operators on stock terminals get the TUI; CI / piped / dumb terminals get the same plain CLI as today. No flag or config change required.

`EIDOS_NO_TUI=1` is the documented escape hatch (added to README + CHANGELOG).

## 9. Dependencies added

- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/bubbles`
- `github.com/charmbracelet/lipgloss`
- `github.com/charmbracelet/glamour`
- `github.com/charmbracelet/x/exp/teatest` (test-only)

All Charm libraries; well-maintained; together add ~5 MB to the binary (acceptable — eidos is already 34 MB).
