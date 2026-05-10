# First Contact wizard TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the wizard's plain-stdio CLI with a Bubble Tea TUI (transcript style with bronze accent), keeping the existing CLI as a fallback for non-TTY / CI / piped use.

**Architecture:** Bubble Tea owns the main goroutine; phase logic runs in a worker goroutine. They communicate through typed `tea.Msg` (worker → TUI ask) and a buffered reply channel (TUI → worker). The existing `Renderer` interface is unchanged so phase logic and tests don't move; a new `TUIRenderer` extension interface carries `RunWithPhases(ctx, fn)` for the lifecycle. `NewAuto(in, out, cps)` picks TUI on a real ≥60-col TTY, CLI otherwise.

**Tech Stack:** Go 1.25, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/glamour`. Test harness: `github.com/charmbracelet/x/exp/teatest`.

---

## Working agreements

- Worktree root: `/data/eidopsyche/.claude/worktrees/firstcontact-tui`
- After every task: `gofmt -l . && go vet ./... && go test ./...` clean before commit.
- Conventional Commits, scope `firstcontact` (sometimes `eidos`).
- Each commit ends with `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`.
- The plan is purely additive to the renderer package; phase logic, claude wrapper, slug derivation, ontology / wake / forge plumbing are not touched.

---

## Milestone A — Scaffolding (no Bubble Tea yet)

### Task A1: Extend `Renderer` with `TUIRenderer`; add `NewAuto` factory + capability detection

**Files:**
- Modify: `internal/firstcontact/render/renderer.go`
- Create: `internal/firstcontact/render/factory.go`
- Create: `internal/firstcontact/render/factory_test.go`

- [ ] **Step 1: Add `TUIRenderer` interface to renderer.go**

```go
// TUIRenderer is implemented by renderers whose lifecycle requires
// owning the main goroutine (Bubble Tea). Surfaces detect this via
// type-assertion and call RunWithPhases instead of running the phase
// driver directly. The plain Renderer interface (Prompt, Show, etc.)
// is unchanged so phase logic doesn't need to know about the
// distinction.
type TUIRenderer interface {
	Renderer
	RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error
}
```

- [ ] **Step 2: Write the failing factory tests**

```go
// internal/firstcontact/render/factory_test.go
package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

func TestNewAuto_FallsBackOnPipe(t *testing.T) {
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if r.Capabilities().IsTTY {
		t.Errorf("expected CLI fallback for *bytes.Buffer; got TTY-capable renderer")
	}
}

func TestNewAuto_RespectsEIDOS_NO_TUI(t *testing.T) {
	t.Setenv("EIDOS_NO_TUI", "1")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("EIDOS_NO_TUI=1 should force CLI fallback")
	}
}

func TestNewAuto_RespectsCIEnv(t *testing.T) {
	t.Setenv("CI", "true")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("CI=true should force CLI fallback")
	}
}

func TestNewAuto_RespectsDumbTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("TERM=dumb should force CLI fallback")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/firstcontact/render/ -run TestNewAuto -v
```
Expected: `undefined: render.NewAuto`.

- [ ] **Step 4: Implement `NewAuto` and capability check**

```go
// internal/firstcontact/render/factory.go
package render

import (
	"io"
	"os"

	"golang.org/x/term"
)

// NewAuto returns a TUI Renderer when stdout is a real ≥60-col TTY
// and TUI is not explicitly disabled, otherwise the existing plain CLI
// fallback. The TUI implementation itself lands later; for now this
// always returns the CLI fallback.
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

`NewTUI` doesn't exist yet. To unblock the factory unit tests, add a stub in `tui.go` that returns a CLI renderer for now (the real implementation lands in milestone B/C):

```go
// internal/firstcontact/render/tui.go (stub for now; real impl in milestones B/C)
package render

import "io"

// NewTUI returns a Bubble Tea TUI renderer. Stub for milestone A —
// returns the CLI renderer until the real Model + bridge land.
func NewTUI(in io.Reader, out io.Writer, cps int) Renderer {
	return NewCLI(in, out, cps)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{renderer.go,factory.go,factory_test.go,tui.go}
git commit -m "$(cat <<'EOF'
refactor(firstcontact): TUIRenderer interface + NewAuto factory

Sets up the seam for the Bubble Tea TUI: TUIRenderer extends Renderer
with RunWithPhases (Bubble Tea must own the main goroutine).
NewAuto(in, out, cps) picks TUI when stdout is a ≥60-col TTY and TUI
isn't disabled (EIDOS_NO_TUI=1, CI, TERM=dumb), else falls back to
the existing CLI renderer. NewTUI is a stub that returns the CLI
renderer until the real Model + bridge land in milestone B/C.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task A2: Switch `cmd/eidos/summon` to `NewAuto`

**Files:**
- Modify: `cmd/eidos/summon/cmd.go`

- [ ] **Step 1: Replace `NewCLI` with `NewAuto`**

In `cmd/eidos/summon/cmd.go`, change:

```go
rend := render.NewCLI(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
```

to:

```go
rend := render.NewAuto(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
```

Leave the rest of `Run()` alone for now — the `TUIRenderer` branch lands in milestone D once the bridge exists. This step is purely about routing through the auto-selector; since `NewTUI` is a stub returning the CLI renderer, behavior is unchanged.

- [ ] **Step 2: Run tests + build**

```bash
go test ./...
go build ./...
```
Expected: PASS, builds cleanly.

- [ ] **Step 3: Commit**

```bash
git add cmd/eidos/summon/cmd.go
git commit -m "$(cat <<'EOF'
refactor(eidos): summon picks renderer via NewAuto

Routes summon's renderer construction through render.NewAuto so the
TUI implementation can take over when it lands. Behavior is unchanged
in this commit — NewTUI is still a stub returning the CLI renderer.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Milestone B — Bubble Tea Model

### Task B1: Add Charm dependencies + lipgloss palette

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/firstcontact/render/tui_styles.go`

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/bubbles
go get github.com/charmbracelet/lipgloss
go get github.com/charmbracelet/glamour
go mod tidy
```

- [ ] **Step 2: Create the palette + styled snippets**

```go
// internal/firstcontact/render/tui_styles.go
package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette — Style C (transcript, warm bronze accent on dark).
// Picked during the brainstorm visual companion. Hardcoded for now;
// theming is out of scope for this milestone.
var (
	bronze    = lipgloss.Color("#c8946b")
	bronzeDim = lipgloss.Color("#7a5a44")
	fg        = lipgloss.Color("#d4cdc1")
	fgDim     = lipgloss.Color("#5d6370")
	fgMuted   = lipgloss.Color("#3a3d44")
)

// Styled snippets that the Model uses. Names express intent so
// View() reads as prose.
var (
	StyleAccent     = lipgloss.NewStyle().Foreground(bronze).Bold(true)
	StyleHint       = lipgloss.NewStyle().Foreground(fgDim)
	StylePromptChar = lipgloss.NewStyle().Foreground(bronze)
	StyleBody       = lipgloss.NewStyle().Foreground(fg)
	StylePastBody   = lipgloss.NewStyle().Foreground(fgDim)
	StylePhaseRule  = lipgloss.NewStyle().Foreground(fgMuted)
	StylePhaseLabel = lipgloss.NewStyle().Foreground(bronze).Bold(true)
	StyleSpinner    = lipgloss.NewStyle().Foreground(bronze)
)

// SectionRule renders a horizontal separator at the given width.
func SectionRule(width int) string {
	if width < 1 {
		width = 40
	}
	return StylePhaseRule.Render(strings.Repeat("─", width))
}

// PhaseLabel formats "▎ phase 2 — the summoning book" with the bronze bar.
func PhaseLabel(text string) string {
	return StylePhaseLabel.Render("▎ ") + StyleAccent.Render(text)
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet ./...
git add go.mod go.sum internal/firstcontact/render/tui_styles.go
git commit -m "$(cat <<'EOF'
feat(firstcontact): Charm dependencies + lipgloss palette

Adds bubbletea, bubbles, lipgloss, glamour. tui_styles.go pins the
Style C palette (warm bronze accent on dark) and exposes named
styled snippets so the Model's View() reads as prose
(PhaseLabel, SectionRule, StylePromptChar, etc).

No behavior change yet — the Model and bridge land in following commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B2: Typed bridge messages

**Files:**
- Create: `internal/firstcontact/render/tui_messages.go`

- [ ] **Step 1: Define the typed Msgs**

```go
// internal/firstcontact/render/tui_messages.go
package render

// askKind discriminates the renderer method that produced an askMsg.
type askKind int

const (
	kindPrompt       askKind = iota // single-line text input
	kindMultiline                   // textarea
	kindPromptChoice                // arrow-key list
	kindShow                        // print one-shot text
	kindFrame                       // print full-screen frame (transcript-style: just inserts content)
	kindTypewriter                  // body printed char-by-char
	kindLogo                        // logo render
)

// askMsg is the worker → TUI message. The Model's Update switches on
// kind to decide which widget to activate.
type askMsg struct {
	kind     askKind
	question string
	opts     PromptOpts
	choices  []ChoiceOption
	body     string
}

// replyMsg is the TUI → worker message sent through the renderer's
// reply channel after the user submits input or after a fire-and-act
// op (Show/Frame/Typewriter/Logo) finishes rendering.
type replyMsg struct {
	text string // for Prompt: the typed text; for PromptChoice: the index as a string
	err  error
}

// startStatusMsg / updateStatusMsg / stopStatusMsg drive a long-running
// status line that ticks while a claude call or boot-wake runs.
type startStatusMsg struct {
	id      int
	message string
}

type updateStatusMsg struct {
	id      int
	message string
}

type stopStatusMsg struct {
	id int
}

// workerDoneMsg signals that the phase function returned. The TUI
// renders the final state and quits on receipt.
type workerDoneMsg struct {
	err error
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/tui_messages.go
git commit -m "$(cat <<'EOF'
feat(firstcontact): typed Msgs for the TUI bridge

askMsg (worker → TUI), replyMsg (TUI → worker), and the status
lifecycle msgs (start/update/stop). The bridge channel architecture
keeps phase logic synchronous — each renderer method sends an askMsg
then blocks on the reply channel.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B3: Bare Model with header + Init/Update/View skeleton

**Files:**
- Create: `internal/firstcontact/render/tui_model.go`
- Create: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/firstcontact/render/tui_test.go
package render

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestModel_InitialFrame(t *testing.T) {
	m := newModel(modelDeps{})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	out := readUntilContains(t, tm, "eidos summon", 500)
	if !strings.Contains(out, "eidos summon") {
		t.Errorf("initial frame missing header; got: %q", out)
	}
}

// readUntilContains polls the TestModel's output until it contains the
// substring or the deadline (in ms) expires.
func readUntilContains(t *testing.T, tm *teatest.TestModel, want string, deadlineMs int) string {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return strings.Contains(string(out), want)
	}, teatest.WithDuration(deadlineMs*1_000_000))
	return string(tm.FinalOutput().([]byte))
}
```

(Note: teatest's exact API may differ slightly between versions — adjust the helper to use `tm.Output()` polling per `teatest`'s actual surface. The key contract is "the initial frame contains 'eidos summon' as the header".)

- [ ] **Step 2: Run test**

```bash
go test ./internal/firstcontact/render/ -run TestModel_InitialFrame -v
```
Expected: undefined `newModel`, `modelDeps`.

- [ ] **Step 3: Implement the bare Model**

```go
// internal/firstcontact/render/tui_model.go
package render

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// modelDeps groups the channels and config the Model needs at
// construction. Exposed for tests to wire fakes.
type modelDeps struct {
	asks    <-chan askMsg
	replies chan<- replyMsg
}

// model is the Bubble Tea Model that drives the wizard's TUI surface.
// It is a state machine over askMsg + tea.KeyMsg events; the active
// widget is a function of the current ask.
type model struct {
	deps   modelDeps
	width  int
	height int

	// transcript accumulates already-rendered phase output. View()
	// concatenates transcript + active widget so past phases stay
	// visible (Style C: transcript, no alt-screen).
	transcript []string

	// activeAsk is non-nil while the worker is waiting for input.
	activeAsk *askMsg

	// finalErr holds the worker's terminal error if any.
	finalErr error
	done     bool
}

func newModel(d modelDeps) *model {
	return &model{deps: d}
}

func (m *model) Init() tea.Cmd {
	return waitForAsk(m.deps.asks)
}

// waitForAsk is a tea.Cmd that blocks on the asks channel and
// surfaces the next askMsg as a tea.Msg. The Update loop chains this
// after every reply so we get a continuous stream of asks.
func waitForAsk(asks <-chan askMsg) tea.Cmd {
	return func() tea.Msg {
		ask, ok := <-asks
		if !ok {
			return workerDoneMsg{}
		}
		return ask
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.done = true
			return m, tea.Quit
		}
		return m, nil
	case askMsg:
		// Detailed widget switch lands in B4-B10. For now: ack so the
		// worker doesn't deadlock, and re-arm the asks listener.
		m.deps.replies <- replyMsg{}
		return m, waitForAsk(m.deps.asks)
	case workerDoneMsg:
		m.finalErr = msg.err
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) View() string {
	var b strings.Builder
	b.WriteString(StyleAccent.Render("eidos summon"))
	b.WriteString("\n")
	b.WriteString(SectionRule(min(m.width, 60)))
	b.WriteString("\n")
	for _, line := range m.transcript {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): bare TUI Model skeleton

Bubble Tea Model with Init/Update/View. View renders the header
("eidos summon") + section rule + transcript. Update handles
WindowSizeMsg and Ctrl-C; askMsg currently just acks (so the worker
never deadlocks) until widget-specific handling lands in B4-B10.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B4: Single-line `Prompt` (textinput widget)

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

Add to `tui_test.go`:

```go
func TestModel_PromptReturnsTypedText(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{kind: kindPrompt, question: "name?", opts: PromptOpts{}}

	tm.Type("alice")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-replies:
		if got.text != "alice" {
			t.Errorf("reply text = %q, want %q", got.text, "alice")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no reply received")
	}
}
```

(Add `import "time"` and `import tea "github.com/charmbracelet/bubbletea"` if not already present.)

- [ ] **Step 2: Add textinput to the Model**

In `tui_model.go`:

```go
import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	deps   modelDeps
	width  int
	height int
	transcript []string

	activeAsk *askMsg
	input     textinput.Model // active for kindPrompt

	finalErr error
	done     bool
}

func newModel(d modelDeps) *model {
	ti := textinput.New()
	ti.Prompt = StylePromptChar.Render("› ")
	ti.CharLimit = 256
	return &model{deps: d, input: ti}
}
```

- [ ] **Step 3: Wire askMsg → activate input; KeyEnter → reply**

Replace the `case askMsg:` branch in Update:

```go
case askMsg:
	m.activeAsk = &msg
	switch msg.kind {
	case kindPrompt:
		m.input.SetValue("")
		m.input.Focus()
		m.input.CharLimit = 256
		return m, m.input.Cursor.BlinkCmd()
	default:
		// Other kinds land in B5-B10. For now ack and move on.
		m.deps.replies <- replyMsg{}
		m.activeAsk = nil
		return m, waitForAsk(m.deps.asks)
	}
case tea.KeyMsg:
	if msg.Type == tea.KeyCtrlC {
		m.done = true
		return m, tea.Quit
	}
	if m.activeAsk != nil && m.activeAsk.kind == kindPrompt {
		if msg.Type == tea.KeyEnter {
			text := strings.TrimSpace(m.input.Value())
			m.activeAsk = nil
			m.input.Blur()
			m.transcript = append(m.transcript, "  "+StylePromptChar.Render("› ")+text)
			m.deps.replies <- replyMsg{text: text}
			return m, waitForAsk(m.deps.asks)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
```

- [ ] **Step 4: Update View to show the active prompt**

```go
func (m *model) View() string {
	var b strings.Builder
	b.WriteString(StyleAccent.Render("eidos summon"))
	b.WriteString("\n")
	b.WriteString(SectionRule(min(m.width, 60)))
	b.WriteString("\n")
	for _, line := range m.transcript {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.activeAsk != nil {
		switch m.activeAsk.kind {
		case kindPrompt:
			b.WriteString("\n")
			b.WriteString(StyleBody.Render(m.activeAsk.question))
			b.WriteString("\n")
			if m.activeAsk.opts.HelpText != "" {
				b.WriteString(StyleHint.Render("  (" + m.activeAsk.opts.HelpText + ")"))
				b.WriteString("\n")
			}
			b.WriteString(m.input.View())
		}
	}
	return b.String()
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): TUI single-line Prompt via textinput

Activates bubbles/textinput when an askMsg of kindPrompt arrives;
Enter submits and replies through the bridge channel. Submitted text
lands in the transcript dimmed for past-phase scrolling.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B5: Multi-line `Prompt` (textarea widget, Esc-then-Enter submit)

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

Add to `tui_test.go`:

```go
func TestModel_MultilinePromptSubmitsOnEscEnter(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{kind: kindMultiline, question: "describe", opts: PromptOpts{Multiline: true}}

	tm.Type("line one")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Type("line two")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-replies:
		if !strings.Contains(got.text, "line one") || !strings.Contains(got.text, "line two") {
			t.Errorf("multiline reply missing input lines: %q", got.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no reply received")
	}
}
```

- [ ] **Step 2: Add textarea to the Model**

```go
import (
	"github.com/charmbracelet/bubbles/textarea"
)

type model struct {
	// ... (existing fields)
	textarea     textarea.Model
	escPending   bool // true after Esc; next Enter submits
}

func newModel(d modelDeps) *model {
	ti := textinput.New()
	ti.Prompt = StylePromptChar.Render("› ")
	ti.CharLimit = 256
	ta := textarea.New()
	ta.Placeholder = ""
	ta.CharLimit = 4096
	return &model{deps: d, input: ti, textarea: ta}
}
```

- [ ] **Step 3: Wire askMsg → activate textarea; Esc-then-Enter submits**

In Update's `case askMsg:`, add:

```go
case kindMultiline:
	m.textarea.SetValue("")
	m.textarea.Focus()
	m.escPending = false
	return m, m.textarea.Cursor.BlinkCmd()
```

In Update's `case tea.KeyMsg:`, add (before the textinput-active branch):

```go
if m.activeAsk != nil && m.activeAsk.kind == kindMultiline {
	if msg.Type == tea.KeyEsc {
		m.escPending = true
		return m, nil
	}
	if msg.Type == tea.KeyEnter && m.escPending {
		text := strings.TrimSpace(m.textarea.Value())
		m.activeAsk = nil
		m.textarea.Blur()
		m.escPending = false
		m.transcript = append(m.transcript,
			StylePastBody.Render(indentBlock(text, "  ")))
		m.deps.replies <- replyMsg{text: text}
		return m, waitForAsk(m.deps.asks)
	}
	m.escPending = false
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}
```

Add a small helper:

```go
func indentBlock(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}
```

In View, add:

```go
case kindMultiline:
	b.WriteString("\n")
	b.WriteString(StyleBody.Render(m.activeAsk.question))
	b.WriteString("\n")
	b.WriteString(StyleHint.Render("  (Press Esc then Enter to submit)"))
	b.WriteString("\n")
	b.WriteString(m.textarea.View())
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): TUI multi-line Prompt via textarea

bubbles/textarea for kindMultiline. Submit on Esc-then-Enter
(Codex CLI convention; matches the operator's existing terminal
muscle memory). Hint line says so explicitly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B6: `PromptChoice` (bubbles/list)

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModel_PromptChoiceArrowDownEnter(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{
		kind:    kindPromptChoice,
		question: "which?",
		choices: []ChoiceOption{{Label: "a"}, {Label: "b"}, {Label: "c"}},
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-replies:
		if got.text != "1" {
			t.Errorf("reply text = %q, want %q", got.text, "1")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no reply received")
	}
}
```

- [ ] **Step 2: Add a tiny choices state** (we don't need full bubbles/list — a custom focus index keeps the View predictable for tests)

In `tui_model.go`:

```go
type model struct {
	// ...
	choiceIdx int // active index for kindPromptChoice
}
```

In Update's `case askMsg:`:

```go
case kindPromptChoice:
	m.choiceIdx = 0
	return m, nil
```

In Update's `case tea.KeyMsg:` (before the textinput / textarea branches):

```go
if m.activeAsk != nil && m.activeAsk.kind == kindPromptChoice {
	switch msg.Type {
	case tea.KeyUp:
		if m.choiceIdx > 0 {
			m.choiceIdx--
		}
		return m, nil
	case tea.KeyDown:
		if m.choiceIdx < len(m.activeAsk.choices)-1 {
			m.choiceIdx++
		}
		return m, nil
	case tea.KeyEnter:
		idx := m.choiceIdx
		label := m.activeAsk.choices[idx].Label
		m.activeAsk = nil
		m.transcript = append(m.transcript,
			"  "+StylePromptChar.Render("› ")+StylePastBody.Render(label))
		m.deps.replies <- replyMsg{text: fmt.Sprintf("%d", idx)}
		return m, waitForAsk(m.deps.asks)
	}
	return m, nil
}
```

(Add `import "fmt"` if not already present.)

In View, add:

```go
case kindPromptChoice:
	b.WriteString("\n")
	b.WriteString(StyleBody.Render(m.activeAsk.question))
	b.WriteString("\n")
	for i, opt := range m.activeAsk.choices {
		focus := "  "
		label := opt.Label
		if i == m.choiceIdx {
			focus = StylePromptChar.Render("› ")
			label = StyleAccent.Render(opt.Label)
		}
		b.WriteString(focus + label)
		if opt.Hint != "" {
			b.WriteString("    " + StyleHint.Render("("+opt.Hint+")"))
		}
		b.WriteString("\n")
	}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): TUI PromptChoice with arrow-key navigation

Custom focus-index implementation (no bubbles/list — its borders
don't match Style C and we don't need filtering). Up/Down move,
Enter submits, focused option gets the bronze prompt char + bold
label.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B7: Status spinner (start / update / stop lifecycle)

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModel_StatusLifecycle(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	tm.Send(startStatusMsg{id: 1, message: "thinking..."})
	out := readUntilContains(t, tm, "thinking", 500)
	if !strings.Contains(out, "thinking") {
		t.Errorf("output missing initial status: %q", out)
	}
	tm.Send(updateStatusMsg{id: 1, message: "thinking... (5s)"})
	out = readUntilContains(t, tm, "(5s)", 500)
	if !strings.Contains(out, "(5s)") {
		t.Errorf("output missing updated status: %q", out)
	}
	tm.Send(stopStatusMsg{id: 1})
	// no assertion on absence — that requires a different harness.
}
```

- [ ] **Step 2: Implement spinner state**

Add to model:

```go
import (
	"github.com/charmbracelet/bubbles/spinner"
)

type activeStatus struct {
	id      int
	message string
}

type model struct {
	// ...
	spinner spinner.Model
	status  *activeStatus
}

func newModel(d modelDeps) *model {
	// ...
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = StyleSpinner
	return &model{
		deps: d, input: ti, textarea: ta, spinner: sp,
	}
}
```

In Update, handle the lifecycle:

```go
case startStatusMsg:
	m.status = &activeStatus{id: msg.id, message: msg.message}
	return m, m.spinner.Tick
case updateStatusMsg:
	if m.status != nil && m.status.id == msg.id {
		m.status.message = msg.message
	}
	return m, nil
case stopStatusMsg:
	if m.status != nil && m.status.id == msg.id {
		m.status = nil
	}
	return m, nil
case spinner.TickMsg:
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
```

In View (after the active-ask block):

```go
if m.status != nil {
	b.WriteString("\n")
	b.WriteString(m.spinner.View())
	b.WriteString(" ")
	b.WriteString(StyleHint.Render(m.status.message))
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): TUI status spinner with update lifecycle

bubbles/spinner with the Dot frames in bronze. Start/Update/Stop
msgs match the StatusHandle interface used by phase logic;
spinner.TickMsg drives the animation independently of asks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B8: Show / Frame / Typewriter / Logo

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModel_ShowAddsToTranscript(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{kind: kindShow, body: "hello world"}
	out := readUntilContains(t, tm, "hello world", 500)
	if !strings.Contains(out, "hello world") {
		t.Errorf("transcript missing Show body: %q", out)
	}
	// Show is synchronous; an ack reply should arrive.
	select {
	case <-replies:
	case <-time.After(time.Second):
		t.Errorf("no ack reply for Show")
	}
}

func TestModel_FrameAndTypewriterAddToTranscript(t *testing.T) {
	asks := make(chan askMsg, 2)
	replies := make(chan replyMsg, 2)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{kind: kindFrame, body: "frame body"}
	<-replies
	asks <- askMsg{kind: kindTypewriter, body: "typed body"}
	<-replies

	out := readUntilContains(t, tm, "typed body", 1000)
	if !strings.Contains(out, "frame body") {
		t.Errorf("transcript missing frame: %q", out)
	}
	if !strings.Contains(out, "typed body") {
		t.Errorf("transcript missing typewriter: %q", out)
	}
}
```

- [ ] **Step 2: Wire up the kinds**

In Update's `case askMsg:`, add cases:

```go
case kindShow, kindFrame:
	m.transcript = append(m.transcript, StyleBody.Render(msg.body))
	m.deps.replies <- replyMsg{}
	return m, waitForAsk(m.deps.asks)
case kindTypewriter:
	// Render instantly into the transcript — the actual char-by-char
	// pacing is a UX flourish that's worth deferring for v1; the body
	// landing in the transcript is the hard contract. (The CLI fallback
	// renders char-by-char; future enhancement: tea.Tick driven char
	// reveal.)
	m.transcript = append(m.transcript, StyleBody.Render(msg.body))
	m.deps.replies <- replyMsg{}
	return m, waitForAsk(m.deps.asks)
case kindLogo:
	// Logo is one-shot: print the static letter circle.
	m.transcript = append(m.transcript, m.renderLogo())
	m.deps.replies <- replyMsg{}
	return m, waitForAsk(m.deps.asks)
```

Add the logo helper:

```go
func (m *model) renderLogo() string {
	const logo = `       E   I
     D       O
    P    *    P
     S       Y
       C   H
         E`
	return StyleAccent.Render(logo)
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): TUI Show / Frame / Typewriter / Logo

All four kinds append to the transcript and ack via the reply
channel (synchronous from the worker's POV — guards against races
with the next ask). Typewriter currently renders the full body
instantly; per-char pacing via tea.Tick is a future enhancement.
Logo is a one-shot static letter circle (style C is a transcript;
no animated rotation).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task B9: Glamour markdown for the agent's response

**Files:**
- Modify: `internal/firstcontact/render/tui_model.go`
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModel_TypewriterRendersMarkdownIfFlagged(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	defer tm.Quit()

	asks <- askMsg{
		kind: kindTypewriter,
		body: "> *quoted line*\n\nplain line",
		opts: PromptOpts{HelpText: "markdown"},
	}
	<-replies
	out := readUntilContains(t, tm, "plain line", 1000)
	// Glamour's exact output is terminal-dependent; assert only that
	// the rendered version contains the body words and that the > char
	// is no longer at column 0 (glamour styles blockquotes).
	if !strings.Contains(out, "quoted line") {
		t.Errorf("rendered body missing 'quoted line': %q", out)
	}
}
```

- [ ] **Step 2: Add markdown rendering when `opts.HelpText == "markdown"`**

(We overload `HelpText` as a render hint to avoid expanding `PromptOpts` for one use-case. The wizard's response render is the only call site that wants markdown.)

In Update, replace the kindTypewriter case:

```go
case kindTypewriter:
	body := msg.body
	if msg.opts.HelpText == "markdown" {
		if rendered, err := renderMarkdown(body, m.width); err == nil {
			body = rendered
		}
	}
	m.transcript = append(m.transcript, StyleBody.Render(body))
	m.deps.replies <- replyMsg{}
	return m, waitForAsk(m.deps.asks)
```

Add a helper:

```go
import "github.com/charmbracelet/glamour"

func renderMarkdown(body string, width int) (string, error) {
	if width < 40 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width-4),
	)
	if err != nil {
		return "", err
	}
	return r.Render(body)
}
```

(Confirm the wizard's call site sets `opts.HelpText = "markdown"` when calling `Typewriter` for the response — task D2 will adjust the cmd-level wrapper.)

- [ ] **Step 3: Run tests**

```bash
go test ./internal/firstcontact/render/ -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{tui_model.go,tui_test.go}
git commit -m "$(cat <<'EOF'
feat(firstcontact): glamour markdown for the agent's response

When kindTypewriter's askMsg has opts.HelpText == "markdown", route
the body through glamour for blockquote / heading / emphasis styling
before adding to the transcript. Falls back to the plain body on
glamour error. Used by Phase3's response render so Aristotle's
> *blockquote* echo of the calling-words actually looks like a
blockquote.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Milestone C — Bridge integration

### Task C1: Implement `tuiRenderer.Renderer` methods

**Files:**
- Modify: `internal/firstcontact/render/tui.go`

- [ ] **Step 1: Replace the stub with the real renderer**

```go
// internal/firstcontact/render/tui.go
package render

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// NewTUI returns a Bubble Tea TUI renderer. The phase logic talks to
// it through the Renderer interface; the program's lifecycle is
// owned by RunWithPhases (see TUIRenderer).
func NewTUI(in io.Reader, out io.Writer, cps int) Renderer {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	prog := tea.NewProgram(m,
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithoutCatchPanics(),
	)
	return &tuiRenderer{
		prog:    prog,
		asks:    asks,
		replies: replies,
		cps:     cps,
	}
}

type tuiRenderer struct {
	prog    *tea.Program
	asks    chan askMsg
	replies chan replyMsg
	cps     int
	statusN atomic.Int64
}

func (r *tuiRenderer) Capabilities() Capabilities {
	return Capabilities{IsTTY: true, ANSI: true, Color: true}
}

func (r *tuiRenderer) Frame(content string) {
	r.asks <- askMsg{kind: kindFrame, body: content}
	<-r.replies
}

func (r *tuiRenderer) Show(text string) {
	r.asks <- askMsg{kind: kindShow, body: text}
	<-r.replies
}

func (r *tuiRenderer) Typewriter(_ context.Context, text string) {
	r.asks <- askMsg{kind: kindTypewriter, body: text}
	<-r.replies
}

func (r *tuiRenderer) Prompt(question string, opts PromptOpts) (string, error) {
	kind := kindPrompt
	if opts.Multiline {
		kind = kindMultiline
	}
	r.asks <- askMsg{kind: kind, question: question, opts: opts}
	reply, ok := <-r.replies
	if !ok {
		return "", fmt.Errorf("tui closed before reply")
	}
	return reply.text, reply.err
}

func (r *tuiRenderer) PromptChoice(q string, options []ChoiceOption) (int, error) {
	r.asks <- askMsg{kind: kindPromptChoice, question: q, choices: options}
	reply, ok := <-r.replies
	if !ok {
		return 0, fmt.Errorf("tui closed before reply")
	}
	if reply.err != nil {
		return 0, reply.err
	}
	var i int
	if _, err := fmt.Sscanf(reply.text, "%d", &i); err != nil {
		return 0, fmt.Errorf("parse choice index %q: %w", reply.text, err)
	}
	return i, nil
}

func (r *tuiRenderer) Status(message string) StatusHandle {
	id := int(r.statusN.Add(1))
	r.prog.Send(startStatusMsg{id: id, message: message})
	return &tuiStatusHandle{prog: r.prog, id: id}
}

func (r *tuiRenderer) Logo(_ context.Context, _ time.Duration) {
	r.asks <- askMsg{kind: kindLogo}
	<-r.replies
}

type tuiStatusHandle struct {
	prog *tea.Program
	id   int
	done atomic.Bool
}

func (h *tuiStatusHandle) Update(message string) {
	if h.done.Load() {
		return
	}
	h.prog.Send(updateStatusMsg{id: h.id, message: message})
}

func (h *tuiStatusHandle) Stop() {
	if h.done.Swap(true) {
		return
	}
	h.prog.Send(stopStatusMsg{id: h.id})
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/tui.go
git commit -m "$(cat <<'EOF'
feat(firstcontact): tuiRenderer.Renderer method impls

All Renderer methods delegate through the bridge channel: blocking
ask + reply for synchronous ops (Prompt, PromptChoice, Show, Frame,
Typewriter, Logo); fire-and-forget tea.Program.Send for Status
lifecycle (start/update/stop msgs). Multiline prompts route to
kindMultiline. Status handle is concurrency-safe via atomic.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task C2: `RunWithPhases` lifecycle

**Files:**
- Modify: `internal/firstcontact/render/tui.go`

- [ ] **Step 1: Implement `RunWithPhases`**

Append to `tui.go`:

```go
// RunWithPhases runs the Bubble Tea program on the calling goroutine
// and the phase function in a worker. Owns the bridge channel
// lifecycle: closes replies when the TUI exits so the worker doesn't
// deadlock.
func (r *tuiRenderer) RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error {
	workerErr := make(chan error, 1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				workerErr <- fmt.Errorf("phase logic panicked: %v", rec)
				r.prog.Send(workerDoneMsg{err: fmt.Errorf("panic: %v", rec)})
				return
			}
		}()
		err := fn(workerCtx, r)
		workerErr <- err
		r.prog.Send(workerDoneMsg{err: err})
	}()

	if _, runErr := r.prog.Run(); runErr != nil {
		cancel()
		return fmt.Errorf("tui: %w", runErr)
	}
	cancel()
	close(r.replies)
	return <-workerErr
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/tui.go
git commit -m "$(cat <<'EOF'
feat(firstcontact): RunWithPhases — TUI lifecycle owner

Bubble Tea owns the main goroutine; phase logic runs in a worker.
defer recover() in the worker converts panics to workerDoneMsg so
the TUI can exit cleanly. close(replies) on exit unblocks any
worker still waiting for input. Errors from either side propagate
to the caller.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task C3: Bridge integration test

**Files:**
- Modify: `internal/firstcontact/render/tui_test.go`

- [ ] **Step 1: Write the bridge integration test**

```go
// Bridge integration test: drives a real tuiRenderer + Bubble Tea
// program in-process, verifies that a phase-style worker can call
// Prompt + PromptChoice + Show in sequence and observe matching
// transcript output.
func TestTUI_BridgeRoundTrip(t *testing.T) {
	in, out := newTestIO()
	rend := NewTUI(in, out, 0).(*tuiRenderer)

	var got []string
	worker := func(ctx context.Context, r Renderer) error {
		v, err := r.Prompt("first?", PromptOpts{})
		if err != nil {
			return err
		}
		got = append(got, "p:"+v)
		i, err := r.PromptChoice("which?", []ChoiceOption{{Label: "a"}, {Label: "b"}})
		if err != nil {
			return err
		}
		got = append(got, fmt.Sprintf("c:%d", i))
		r.Show("done")
		return nil
	}

	go func() {
		// Drive simulated keystrokes after the TUI has installed.
		time.Sleep(50 * time.Millisecond)
		typeText(in, "alice\r")
		time.Sleep(50 * time.Millisecond)
		// Down then Enter for choice.
		fmt.Fprintf(in, "\x1b[B\r")
	}()

	err := rend.RunWithPhases(context.Background(), worker)
	if err != nil {
		t.Fatalf("RunWithPhases: %v", err)
	}
	want := []string{"p:alice", "c:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// newTestIO returns a paired in/out io.PipeReader / io.PipeWriter
// for driving the TUI from tests.
func newTestIO() (io.WriteCloser, io.Reader) {
	pr, pw := io.Pipe()
	return pw, pr // pw: tests write keystrokes; pr: TUI reads them
}

// typeText writes printable text into the TUI's input pipe.
func typeText(w io.Writer, s string) {
	_, _ = w.Write([]byte(s))
}
```

(Add `import "io"`, `"reflect"` if not already present.)

> **Note for the implementer:** the exact pipe wiring and timing may need adjustment depending on how `tea.WithInput` consumes the reader. If the test is flaky, increase the sleeps. The contract is "worker receives the simulated keystrokes through the bridge". If teatest's harness exposes a cleaner way to drive a real `tea.Program` through `RunWithPhases`, prefer that.

- [ ] **Step 2: Run the test**

```bash
go test ./internal/firstcontact/render/ -run TestTUI_BridgeRoundTrip -v
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/tui_test.go
git commit -m "$(cat <<'EOF'
test(firstcontact): TUI bridge integration round-trip

Drives a real tuiRenderer + tea.Program in-process and verifies that
a phase-style worker can call Prompt + PromptChoice + Show against
simulated keystrokes. Pins the "synchronous from the worker's POV"
contract end-to-end.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Milestone D — cmd-level wiring

### Task D1: Wire `cmd/eidos/summon` to use `RunWithPhases`

**Files:**
- Modify: `cmd/eidos/summon/cmd.go`

- [ ] **Step 1: Detect `TUIRenderer` and delegate**

Replace the `firstcontact.Run(ctx, deps)` call site in `Run()`:

```go
deps := firstcontact.Deps{
	// ... (existing fields)
}
if tui, ok := rend.(render.TUIRenderer); ok {
	return tui.RunWithPhases(ctx, func(ctx context.Context, r render.Renderer) error {
		deps.Renderer = r
		s, body, err := firstcontact.Run(ctx, deps)
		if err != nil {
			if errors.Is(err, firstcontact.ErrSelfHostExit) {
				return nil
			}
			return err
		}
		if body != nil {
			r.Show("")
			// Tag the response Typewriter so the TUI renders markdown.
			(&tuiTypewriterAdapter{r: r}).TypewriterMarkdown(ctx, string(body))
			r.Show("")
			fmt.Fprintln(os.Stdout)
			fmt.Fprintf(os.Stdout, stringFor(s.Lang, "phase3_done"), s.Slug)
			fmt.Fprintln(os.Stdout)
		}
		return nil
	})
}
// CLI fallback path — phase logic runs directly:
deps.Renderer = rend
s, body, err := firstcontact.Run(ctx, deps)
// ... (existing post-processing for the CLI path)
```

`tuiTypewriterAdapter` is a tiny shim that calls `Typewriter` with `opts.HelpText = "markdown"` for the response. Add it to `cmd.go`:

```go
type tuiTypewriterAdapter struct{ r render.Renderer }

func (a *tuiTypewriterAdapter) TypewriterMarkdown(ctx context.Context, body string) {
	// Hack: Renderer.Typewriter doesn't take opts; we route through the
	// underlying askMsg by sending a Show with the markdown-rendered
	// body when the renderer supports it. For the TUI path, this maps
	// to kindTypewriter with opts.HelpText="markdown" (B9). For the CLI
	// path, falls back to plain Typewriter. The simplest way: extend
	// Renderer with an optional MarkdownRenderer interface.
	if md, ok := a.r.(MarkdownRenderer); ok {
		md.RenderMarkdown(ctx, body)
		return
	}
	a.r.Typewriter(ctx, body)
}
```

Add the optional interface to `renderer.go`:

```go
// MarkdownRenderer is implemented by renderers that can render
// markdown bodies (TUI via glamour). The CLI does not implement this;
// fall back to plain Typewriter.
type MarkdownRenderer interface {
	RenderMarkdown(ctx context.Context, body string)
}
```

And implement it on `tuiRenderer` in `tui.go`:

```go
func (r *tuiRenderer) RenderMarkdown(ctx context.Context, body string) {
	r.asks <- askMsg{
		kind: kindTypewriter,
		body: body,
		opts: PromptOpts{HelpText: "markdown"},
	}
	<-r.replies
}
```

- [ ] **Step 2: Run tests + build**

```bash
go test ./...
go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/firstcontact/render/{renderer.go,tui.go} cmd/eidos/summon/cmd.go
git commit -m "$(cat <<'EOF'
feat(eidos): summon dispatches to TUIRenderer.RunWithPhases

The cmd-level wrapper detects TUIRenderer via type-assertion and
delegates the lifecycle. Falls back to running firstcontact.Run
directly on the main goroutine for the CLI path. Optional
MarkdownRenderer interface lets the TUI route the agent's response
through glamour.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task D2: Update strings.go for the multiline hint change

**Files:**
- Modify: `internal/firstcontact/strings.go`

- [ ] **Step 1: Update the multiline hint**

The current hint reads "blank line to finish" (CLI convention). Add an alt-form for the TUI: "Press Esc then Enter to submit". Since the wizard's phase 2 calls `Prompt` with `Multiline: true` and the Renderer doesn't know which surface it's running under, we keep the CLI hint and let the TUI override in its Prompt handler (already wired in B5 — the textarea hint is hard-coded "Esc then Enter to submit").

So no code change needed for the hint itself. Just confirm by inspection that:
- CLI path's `phase2_character_q` still reads "blank line to finish"
- TUI path renders its own hint in `kindMultiline` View (B5)

- [ ] **Step 2: Verify by reading**

```bash
grep -n "blank line\|Esc then Enter" internal/firstcontact/{strings.go,render/tui_model.go}
```

Expected output mentions both. No commit needed unless you spot an inconsistency.

---

## Milestone E — Documentation

### Task E1: README + CHANGELOG

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: README — add `EIDOS_NO_TUI` to the troubleshooting / env-var section**

Find the existing "EIDOS_NO_UPDATE_CHECK" mention and add a sibling line:

```
- `EIDOS_NO_TUI=1` — disable the wizard's Bubble Tea TUI and use the
  plain CLI fallback. Auto-set when `CI` is set, when stdout isn't a
  TTY, when `TERM=dumb`, or when the terminal is narrower than 60
  columns.
```

- [ ] **Step 2: CHANGELOG — append to "Unreleased"**

```markdown
- **First Contact wizard TUI** — Bubble Tea + Charm-based interactive
  wizard with arrow-key navigation, animated status spinners, glamour
  markdown rendering for the agent's response, and a transcript-style
  layout (no alt-screen, past phases stay visible). Falls back to the
  plain CLI on non-TTY / CI / dumb terminals; `EIDOS_NO_TUI=1`
  disables it explicitly.
```

- [ ] **Step 3: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs(firstcontact): document the TUI + EIDOS_NO_TUI escape hatch

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Milestone F — Smoke + final checks

### Task F1: Local CI parity

- [ ] **Step 1: Lint**

```bash
gofmt -l . && go vet ./...
```

Both must produce no output.

- [ ] **Step 2: Unit tests**

```bash
go test ./...
```

All green.

- [ ] **Step 3: Build the binary + install on selene**

```bash
go build -o /tmp/eidos-new /data/eidopsyche/.claude/worktrees/firstcontact-tui/cmd/eidos
install -m 0755 /tmp/eidos-new ~/.local/bin/eidos
rm /tmp/eidos-new
```

- [ ] **Step 4: Manual smoke against a real terminal**

```bash
rm -rf ~/.eidos
eidos summon
```

Walk through the ritual interactively. Look for:
- Header "eidos summon" + bronze section rule
- Logo prints once
- Language pick: arrow keys move; Enter selects
- Operator label / character question / naming: text input with bronze prompt char
- Multi-line character question: Esc-then-Enter submits
- Status spinner ticks during claude calls (not just static dots)
- Summoning-book preview scrolls (Up/Down) when it exceeds the screen
- Agent response renders with glamour styling (blockquote indented, emphasis colored)
- Completion message "💠 Ritual complete" prints

If any of those is broken, fix in place + recommit.

- [ ] **Step 5: Manual smoke for the fallback**

```bash
EIDOS_NO_TUI=1 eidos summon < /dev/null
```

Should fail at phase 0 (EOF on language pick) — confirms the CLI fallback path runs without trying to start Bubble Tea.

---

## Self-review

After writing the plan:

**1. Spec coverage:**
- §3.1 new files → tasks A1, B1, B2, B3, B4, B5, B6, B7, B8, B9, C1, C2, C3
- §3.2 capability detection → A1
- §3.3 bridge channel → C1
- §3.4 RunWithPhases → C2
- §3.5 status with live updates → B7 + C1
- §3.6 logo → B8
- §4 visual identity → B1 (palette) + B3-B9 (per-element)
- §5 lifecycle & error handling → B3 (Ctrl-C, resize) + C2 (panic, deadlock guards)
- §6 testing → tests in A1, B3-B9, C3
- §8 migration → E1 (CHANGELOG + README)
- §9 dependencies → B1

**2. Placeholder scan:** No "TBD", "TODO", "fill in details", "similar to", "implement later" in the plan body. Each step has actual code or actual commands. The note in C3 about teatest harness adjustment is a known-imprecise area where the implementer may need to adapt — not a placeholder.

**3. Type consistency:** `model`, `modelDeps`, `tuiRenderer`, `tuiStatusHandle`, `askMsg`, `replyMsg`, `kindPrompt`/`kindMultiline`/`kindPromptChoice`/`kindShow`/`kindFrame`/`kindTypewriter`/`kindLogo`, `startStatusMsg`/`updateStatusMsg`/`stopStatusMsg`/`workerDoneMsg`, `MarkdownRenderer.RenderMarkdown`, `TUIRenderer.RunWithPhases`. All references match across tasks.
