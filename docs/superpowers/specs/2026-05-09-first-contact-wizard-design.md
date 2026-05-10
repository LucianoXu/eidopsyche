# First Contact wizard

**Date**: 2026-05-09
**Status**: Approved (pending implementation)
**Target**: dev (the next release after v0.5.0)
**Scope**: Implement the First Contact ritual specified in `docs/specs/FirstContact.md` as a CLI wizard that runs when a user types bare `eidos` for the first time. The wizard drives the operator's MindGate identity initialization, summons one MindForm via the existing `forge.orchestrate` path, and triggers a new dedicated boot-wake (`birth`) inside the MindForm container. The boot-wake hands control to the agent for self-finalization, secret generation, and the first response.

This builds on `docs/specs/FirstContact.md` (the canonical script — that doc remains the source of truth for narrative intent; this doc is the implementation design).

## 1. Problem statement

Today the bootstrap from "downloaded `eidos`" to "running MindForm" is two manual commands plus a third one for credentials:

```
eidos gate init --label X --home wss://...
eidos forge create <slug> --owner <npub> --relay <wss://...>
eidos forge login <slug>
eidos forge start <slug>
```

Each command exposes flags whose semantics require reading the SPEC. The first MindForm has no opinion of its own when it wakes — it inherits a generic ontology template. There is no narrative thread connecting the operator's intent ("I want to summon X") with the resulting MindForm's identity.

The FirstContact script reframes this as a single ritual: the operator writes a 召唤书 (summoning book), names the to-be-summoned, and watches it answer. Configuration is collected as part of the narrative; the ritual is one-shot, non-resumable; the MindForm wakes for the first time with a dedicated boot prompt that reads the book, generates a private secret, and replies.

## 2. Design intent

Six commitments shape the rest:

1. **The wizard is the new bootstrap path.** Bare `eidos` runs the wizard when no state.db exists. `eidos summon` is the always-available verb for additional MindForms. The two-command path (`eidos gate init` + `eidos forge create`) remains for advanced/scripted use; the wizard is the recommended UX.
2. **CLI first, share-ready core.** The wizard is structured as a renderer-agnostic state machine; one CLI renderer ships now. WebUI parity (per FirstContact §四) is a future renderer plugged into the same core. No WebUI code in this milestone.
3. **Host `claude -p` headless invocations.** All dramaturge calls during phases 0–3 use the host's `claude` binary (already a hard requirement per the spec) in `--output-format json` mode with the current Sonnet model id (`claude-sonnet-4-6` at the time of writing; lives as a constant in `internal/firstcontact/defaults.go` so it can be bumped without spec churn). No SDK key, no in-container claude during the ritual itself.
4. **Bootstrap exception, single call path otherwise.** Phase 1 (operator identity) and phase 3 (MindForm volume + container) reach internal helpers directly because the daemon is guaranteed to be down. After the daemon is up, ordinary IPC methods are used. This is the sanctioned exception described in `CLAUDE.md` ("bootstrap or diagnostic commands that operate on local files when the daemon is *guaranteed* to be down"); it is documented in code comments at every call site.
5. **One-shot, non-resumable, no in-flight state on disk.** All wizard state lives in a `Summoning` struct on the goroutine stack. Ctrl-C, network drop, or any error discards the in-memory state; operator identity (once written by phase 1) is the only thing that survives, because it is reusable.
6. **Birth is a first-class wake type.** The new `WakeKindBirth` (`Reason = "birth"`) is parallel to the three existing wake reasons (`mindgate / heartbeat / manual`), uses a separate boot prompt, and is consumed exactly once per MindForm. The agent generates its own secret in `essence/secret.md` during birth-wake; that file never leaves the container.

## 3. Architecture

### 3.1 New packages

```
internal/firstcontact/
├── doc.go              package doc — explains the bootstrap exception
├── summoning.go        Summoning struct (in-memory state, never persisted)
├── run.go              Run(ctx, deps) — phase-driver entry point
├── phase0_open.go      logo + language + intro
├── phase1_self.go      operator label + home relay + identity.Bootstrap
├── phase2_book.go      character question → research → displaying → naming
├── phase3_seal.go      封缄 → calling-words → 召唤 → 应答
├── claude.go           host-claude exec wrapper (Sonnet, JSON parsing)
├── logo.go             EIDOPSYCHE letter-circle renderer
├── ready.go            background readiness orchestration
├── slug.go             slug derivation + collision handling
├── strings_zh.go       Chinese narrative copy (literal strings)
├── strings_en.go       English narrative copy
└── render/             renderer interface + CLI implementation
    ├── renderer.go     interface: Prompt, Show, Typewriter, Status, Frame
    └── cli.go          CLI implementation (ANSI, keyed input, typewriter pacing)
```

### 3.2 New helper extracted from `gate.runInit`

`internal/identity/bootstrap.go` exposes:

```go
func Bootstrap(stateDir, label, homeRelay string) (npub string, err error)
```

It performs the same actions `cmd/eidos/gate/init.go:runInit` does today: mkdir state dir, generate keypair, save key, open + migrate state.db, set meta rows, insert home relay. It is **idempotent only in failure mode** — if the state dir already has a key file it returns a typed `ErrAlreadyInitialized`. The cobra wrapper in `gate/init.go` is rewritten to call `Bootstrap` directly; behavior from the user's perspective is unchanged.

### 3.3 Wizard entry points

```
cmd/eidos/main.go             dispatches bare eidos → wizard if no state, else cobra help
cmd/eidos/summon/cmd.go       new top-level subcommand `eidos summon`, always launches wizard
```

Detection of "first run" lives in `internal/firstcontact/run.go`: the wizard checks for `<state-dir>/state.db` and `<state-dir>/key`. If both exist, runs in **subsequent-run mode** (skip phases 0 and 1).

### 3.4 `Summoning` state shape (in-memory only)

```go
type Summoning struct {
    Lang             string             // "zh" | "en"
    OperatorLabel    string             // phase 1
    OperatorNpub     string             // computed at phase 1 end
    HomeRelay        string             // phase 1
    CharacterPrompt  string             // phase 2 step 1, raw user text
    Profile          CharacterProfile   // phase 2 step 2, claude-generated
    Displaying       string             // phase 2 step 3, claude-generated paragraph
    SummonedName     string             // phase 2 step 4, user-given
    Slug             string             // derived, user-confirmed
    MindFormNpub     string             // generated by background readiness
    CallingWords     string             // phase 3, claude-generated
    StartedAt        time.Time
    Subsequent       bool               // true when phases 0/1 were skipped
}

type CharacterProfile struct {
    Archetype    string   `json:"archetype"`
    Temperament  string   `json:"temperament"`
    World        string   `json:"world"`
    Settings     []string `json:"settings"`
    Imagery      []string `json:"imagery"`
    Sources      []string `json:"sources"` // debug only, not shown
}
```

The struct lives on the stack of `Run()`. There is no persistence path — `Run()` returning or its context cancelling discards everything.

### 3.5 Renderer interface

```go
type Renderer interface {
    // Frame renders a fullscreen frame (clear screen + place content)
    Frame(content string)
    // Prompt asks for input — single line or multi-line per options
    Prompt(question string, opts PromptOpts) (string, error)
    // PromptChoice asks for one of N options (arrow-key navigated)
    PromptChoice(question string, options []ChoiceOption) (int, error)
    // Show prints a paragraph with default pacing (no typewriter)
    Show(text string)
    // Typewriter prints text character-by-character at the configured rate
    Typewriter(text string)
    // Status begins a non-blocking status line (returns a handle to stop/update)
    Status(message string) StatusHandle
    // Logo renders the rotating EIDOPSYCHE letter-circle for d seconds
    Logo(ctx context.Context, d time.Duration)
    // Capabilities returns rendering capabilities (TTY, ANSI, color)
    Capabilities() Capabilities
}
```

The CLI implementation owns all ANSI sequences. Capabilities-gated: pipe / dumb terminal falls back to static text for the logo and instant rendering for the typewriter.

## 4. Phase walkthrough

### 4.1 Phase 0 — Opening

- **Frame 1**: rotating EIDOPSYCHE letter-circle for ~3 seconds, settles to static.
- **Frame 2**: language pick (`zh` / `en`), arrow-key navigated.
- **Frame 3**: intro paragraph in the chosen language explaining the one-shot ritual. Hardcoded literal — no claude call.

Background tasks start here (`ready.StartBackground`). See §6.

### 4.2 Phase 1 — Operator self (skipped on subsequent runs)

Two sequential prompts:

1. **Operator label** — single-line free text, trimmed, non-empty.
2. **Home relay** — `PromptChoice` with three options:
   - "Use the public station" → defaults to `wss://relay.damus.io` (constant in `firstcontact/defaults.go`; can be re-pointed by a future config option)
   - "Self-host" → wizard prints the two-line `eidos relay init` instructions and exits cleanly with code 0. The user runs that on their own and re-enters the wizard. The wizard **does not** embed `eidos relay init` flags.
   - "Custom URL" → free-text prompt, validated for `ws://` or `wss://` scheme + non-empty host.

End of phase 1: wizard calls `identity.Bootstrap(stateDir, label, homeRelay)`. If `ErrAlreadyInitialized` is returned, wizard prints "this looks like a returning operator — re-run with `eidos summon`" and exits non-zero. Otherwise, `OperatorNpub` is read back from the freshly written state.db.

### 4.3 Phase 2 — The summoning book

#### Step 1 — Character question

`Renderer.Prompt` with multi-line input mode (terminator: blank line + Enter). The literal question:

- zh: 「什么样的角色在你心中？」
- en: "What character is in your heart?"

Validated only as non-empty.

#### Step 2 — Research (claude call #1)

Status spinner: 「正在让世界回想这个角色…」 / "Searching the world's memory…"

`firstcontact.Call` invokes:

```
claude -p --model claude-sonnet-4-6 --output-format json
```

with a research prompt that embeds the operator's free text and asks for a `CharacterProfile` JSON object plus permission to use claude's `WebSearch` tool. The prompt also instructs: return ONLY the JSON object, no explanatory prose.

JSON-parse failure: retry once. Second failure: abort with diagnostic. Hard timeout: 90 seconds (`exec.CommandContext`).

#### Step 3 — Displaying (claude call #2)

Status spinner. Wizard sends a "dramaturge" prompt embedding the `CharacterProfile` and the full set of taboos from FirstContact §二 step 3:

- 3–5 sentence paragraph
- Second-person or descriptive POV (claude picks)
- Setting echoes the world's imagery, does not name source works
- The summoned has not arrived yet — does not speak
- No attribute lists, no bold

The result is rendered via `Typewriter` at ~30 chars/second.

#### Step 4 — Naming + slug confirmation

Single-line prompt:
- zh: 「你愿意以什么名字唤它来？」
- en: "By what name will you summon it?"

After the name is given, `slug.Derive(summonedName, existingSlugs)` produces an ASCII slug:

1. Transliterate to ASCII via `golang.org/x/text/unicode/norm` + a manual fallback table for CJK→pinyin if available, otherwise just strip non-Latin → if empty, fall back to `mindform`.
2. Lowercase, replace non-`[a-z0-9]` with `-`, collapse repeated `-`, trim leading/trailing `-`.
3. Truncate to 30 chars.
4. If colliding with an existing volume `eidos-mindform-<slug>`, append `-2`, `-3`...
5. Validate against `forgectl.ValidateName`. If somehow still invalid (pathological inputs), fall back to `mindform-<n>`.

Wizard shows: 「会以系统句柄 `<slug>` 称呼。回车接受，或键入想要的句柄：」 — user accepts (Enter on empty input) or types a replacement. The replacement is re-validated against `forgectl.ValidateName` and re-checked for collision; on rejection the prompt repeats with a one-line reason.

### 4.4 Phase 3 — Sealing, calling-words, response

#### Step 1 — Seal (封缄)

The wizard renders the summoning-book preview. Markdown template (zh shown; en mirror in `strings_en.go`):

```markdown
# 召唤书

签者：{{.OperatorLabel}}（{{.OperatorNpub}}）
日期：{{.Date}}

{{.Displaying}}

我以「{{.SummonedName}}」之名，召之而来。
被召之者将栖于 {{.MindFormNpub}}。
```

Rendered in a paged view. `PromptChoice` with two options: "封缄" (proceed) / "退出" (quit).

If proceed: wizard awaits `<-ready.Channel()`. While not ready, render the "再等一会儿，世界还在回神…" message; when each background task completes a `Status` line is updated.

Once ready:

- Compose `ontology.Params` (existing) + new `JournalEntry string` field carrying the rendered markdown.
- Call `forge.Orchestrate(ctx, dockerClient, slug, createOpts)` — the existing function, exported (`Orchestrate` capital O) so the wizard can reach it. Internally it pipes the template tar to init-volume; with the new template additions (§5.3) the volume is born with `journal/0000-summoning.md` already in place.

#### Step 2 — Calling-words (claude call #3)

Status spinner. Wizard sends the entire summoning book + a "now write a single short summoning incantation" prompt. Returns one short paragraph (1–3 sentences).

**No editing, no regeneration.** The wizard renders the incantation via `Typewriter`, framed as: 「你即将以这一句话唤它：」 / "These are the words you will speak:"

After a 1-second pause, the wizard:

- Writes the calling-words to `essence/calling-words.md` inside the volume via `forgectl.CopyToContainer` (or volume-direct via a one-shot helper container if the persistent container is still stopped).
- Writes `run/wake/birth.json` (see §5.1) into the volume.

#### Step 3 — First start + response

Wizard calls `forgectl.ContainerStart(slug)`. The supervisor inside the container, on its first wake-loop iteration, sees `birth.json`, promotes it to active, and invokes the agent with the birth prompt (§5.2). The agent writes `journal/0000-response.md` and `essence/born_at`.

The wizard tails `journal/0000-response.md` via a poll loop:

- Interval: 2 seconds.
- Timeout: 90 seconds.
- On appearance: read the file, render via `Typewriter`.
- On timeout: tear down container + volume + `birth.json` (see §7).

After the response renders, the wizard adds the MindForm to the operator's host-side contacts as tier=`friend` (label = SummonedName, relay = HomeRelay, pubkey = MindFormNpub). This makes the operator and the MindForm immediately able to exchange messages once both gates are running. If the host gate daemon is up at that moment, the wizard calls IPC `contact.add`; otherwise (the wizard hasn't started the daemon — which is the default in this milestone), it writes to state.db directly via `contacts.Repo.Add`. The latter is a second sanctioned bootstrap exception, justified inline at the call site. Duplicate-contact errors are ignored.

End: a one-line completion message:

- zh: 「💠 仪式完成。`eidos forge logs <slug>` 看它呼吸。」
- en: "Ritual complete. `eidos forge logs <slug>` to watch it breathe."

### 4.5 Subsequent runs (`eidos summon` after first)

Detection: `state.db` exists AND at least one Docker volume matches `eidos-mindform-*`. Wizard skips phase 0 (no logo, no language pick — language reuses `<state-dir>/config.toml`'s `[wizard].lang` row added in this milestone) and phase 1 (operator label + home relay come from state.db). Starts at phase 2 step 1.

## 5. Boot-wake and ontology additions

### 5.1 New wake type: `WakeKindBirth`

`internal/wake/types.go`:

```go
const ReasonBirth Reason = "birth"
```

A separate envelope file (sibling of `pending.json` / `active.json`):

```go
type BirthSignal struct {
    V                 int    `json:"v"`            // 1
    OperatorNpub      string `json:"operator_npub"`
    SummoningBookPath string `json:"summoning_book_path"`
    CallingWordsPath  string `json:"calling_words_path"`
    ResponsePath      string `json:"response_path"`
    TriggeredAt       int64  `json:"triggered_at"`
}
```

Path: `<wake-dir>/birth.json`.

Supervisor wake-loop change: at the start of each iteration, `birth.json` is checked first. If present AND `essence/born_at` is absent, the supervisor reads it, runs the birth-wake handler, and on success deletes `birth.json`. If `essence/born_at` is present, the supervisor refuses to consume `birth.json` (defense against double-birth) and deletes the stale signal. The existing `pending.json` / `active.json` flow is unchanged.

Birth-wake retry policy: if the agent crashes mid-birth (no `born_at` written, partial `secret.md`), the supervisor's next iteration re-consumes `birth.json` and re-runs the agent. The agent's birth prompt is idempotent — it overwrites `secret.md` and `journal/0000-response.md`.

### 5.2 Birth boot prompt

Location: baked into the docker image at `/usr/local/lib/eidos/prompts/birth.txt`. Distinct from the heartbeat / mindgate prompts.

The supervisor's birth-wake handler concatenates: the constitution (`/eidos/ontology/CLAUDE.md`) + the birth boot prompt + the summoning book + the calling-words, and invokes `claude` with that as initial context.

Birth prompt instructs the agent to:

1. Read the summoning book and the calling-words.
2. Internalize the ontology: rewrite `self/identity.md` and `memory/semantic/master.md` from the book's facts (operator npub, summoned name, displaying-paragraph imagery).
3. **Generate a private secret** — write to `essence/secret.md`. The agent decides format and content; the prompt makes clear: this is yours alone, never to be revealed verbatim, never repeated to the operator or another MindForm.
4. Write the response to `journal/0000-response.md` — directly answering the calling-words. Markdown, freely styled.
5. Set `essence/born_at` to the current Unix-second timestamp.

The normal wake prompts (`heartbeat.txt`, `mindgate.txt`) do **not** mention the secret. Per FirstContact §三, the secret is just a file in `essence/`, not a recurring instruction.

### 5.3 Init-volume keypair injection

The host wizard needs the MindForm's npub at phase 3 step 1 (the 封缄 markdown preview) — *before* the init-volume container runs. Today the init-volume flow at `cmd/eidos/forge/init_volume.go:75-81` generates the keypair inside the container by invoking `eidos gate init` there. We add an opt-in path:

- New env var `EIDOS_FORGE_KEY_HEX`. When set, init-volume **does not** call `eidos gate init` for keypair generation; instead it writes the provided hex to `<gateDir>/key` (using `identity.SaveKey`) and continues with `eidos gate init --key-from-existing` (a new flag on `gate init`) which skips the `identity.Generate` call but still does the state.db / config.toml / own_relays writes.
- The wizard's `forge.Orchestrate` invocation supplies `EIDOS_FORGE_KEY_HEX` populated from a host-side `identity.Generate()` call done in background readiness.
- Existing `eidos forge create` (no env var set) keeps its current behavior — keypair is generated in-container as today. This change is purely additive.

`gate init` gains the `--key-from-existing` flag (default false). When true, it asserts that `<state-dir>/key` already exists (the wizard wrote it there before invoking gate init) and skips key generation; the rest of `runInit` is unchanged.

### 5.4 Ontology template additions

```
internal/ontology/template/
├── essence/
│   └── .keep                       # empty file so tar carries the dir
├── journal/
│   └── .keep                       # empty file so tar carries the dir
└── (existing: CLAUDE.md, self/, memory/, desk/, drawer/)
```

`Params` struct gains a `JournalEntry string` field. `TarStream` is extended: after walking the embedded template tree, **if `params.JournalEntry` is non-empty, append a literal tar entry at `journal/0000-summoning.md` with that content as-is**. This avoids running the markdown through `text/template` (which would break on any `{{` character literals in the user's text or the displaying paragraph).

`essence/secret.md`, `essence/calling-words.md`, `essence/born_at`, and `journal/0000-response.md` are NOT in the template — they are created at runtime (the first three by the wizard via `forgectl` copy or volume-mount, the fourth by the agent).

The rendered ontology's `.gitignore` (or container-side equivalent) adds:

```
essence/secret.md
```

Belt-and-suspenders for the spec's "never leaves" constraint, even though the volume is not pushed anywhere by default.

## 6. Background readiness

`internal/firstcontact/ready.go` exposes:

```go
func StartBackground(ctx context.Context, deps Deps) <-chan ReadyState

type ReadyState struct {
    DockerImage  TaskState
    KeyPair      TaskState   // MindForm secp256k1 keypair
    HomeRelay    TaskState   // ws/wss reachability probe
}

type TaskState struct {
    Status string  // "pending" | "ready" | "failed"
    Error  string  // populated when Status == "failed"
}
```

Spawned from phase 0 (so the work runs while the user reads the intro and does phase 1+2). Each task feeds the channel on every state transition so the renderer can show a live status line during the 封缄 wait.

Hard-fail rules:

| Task | Fails when | Wizard reaction |
|---|---|---|
| Docker image | `docker pull` fails AND image is not present locally (3 retries with backoff) | Abort at phase 3 with diagnostic |
| MindForm keypair | host-side `identity.Generate` returns error (essentially never; the keypair is later injected into init-volume via `EIDOS_FORGE_KEY_HEX` per §5.3) | Abort at phase 3 |
| Home relay probe | TCP+WS handshake fails 3 times | Abort at phase 1 immediately after the user picks the relay (re-prompt for a different choice) |

The relay probe is the only one whose failure is reported during phase 1 (because it's actionable then); the other two fail at phase 3, after 封缄, because that's the earliest moment they're actually needed.

## 7. Failure & rollback

| Failure point | Effect |
|---|---|
| Phase 0 — claude missing, docker daemon down (probed at phase 0) | Hard abort with diagnostic, no state written |
| Phase 1 — `identity.Bootstrap` partial-write failure | `Bootstrap` rolls back any files it created this run; wizard surfaces error and exits non-zero |
| Phase 1 — operator chose self-host relay | Wizard prints `eidos relay init` instructions and exits 0 |
| Phase 1 — relay probe failed | Wizard re-prompts for a different relay choice |
| Phase 2 — claude call fails (any of the three) | Wizard aborts with diagnostic. Operator state remains; in-memory summoning state is gone |
| Phase 3 pre-orchestrate — readiness times out (90s past 封缄 prompt) | "Cannot complete the ritual; the world is not ready." Operator state remains, in-memory state gone |
| Phase 3 mid-orchestrate — `forge.Orchestrate` fails | `Orchestrate` already rolls back the volume on its failure paths (`forge/orchestrate.go` lines 92–94, 105–106); wizard surfaces the error |
| Phase 3 post-orchestrate — boot-wake times out (no `0000-response.md` after 90s) | Wizard calls a new helper `forgectl.PurgeForFailedSummon(slug)` that combines container-rm + volume-rm with an explicit "abandoned-summon" log line. Operator state remains. Next `eidos summon` starts cleanly at phase 2 |
| User Ctrl-C anywhere | Same effect as failure at the corresponding phase. Signal handler in `Run()` cancels the context; all background goroutines exit; `defer` blocks run rollback for whatever was created this session |

**Operator identity is never torn down by failure** — it's the user's npub, theirs forever. Re-running `eidos` after a partial first-contact detects existing state and routes to `eidos summon` mode automatically.

## 8. Claude invocation contract

`internal/firstcontact/claude.go` exposes one function:

```go
func Call(ctx context.Context, prompt string, schema any) error
```

Implementation:

- `exec.CommandContext(ctx, "claude", "-p", "--model", defaults.ClaudeModel, "--output-format", "json", prompt)` — `defaults.ClaudeModel = "claude-sonnet-4-6"`
- Per-call hard timeout: 90 seconds (via `context.WithTimeout` inside `Call`).
- Stdout JSON is parsed into `schema` (which must be a non-nil pointer).
- Non-nil `schema` triggers retry-once on JSON-parse failure (`json.Unmarshal` error). Pass `nil` schema to skip parsing (calling-words is plain text — see below).

**Plain-text variant**: `func CallText(ctx, prompt) (string, error)` — same exec, but reads the JSON envelope's text field and returns it verbatim (claude with `--output-format json` returns `{"type":"result","subtype":"success","is_error":false,"result":"..."}`; we extract `result`). Used for the displaying paragraph and the calling-words.

Failure modes:

| Failure | User-facing reaction |
|---|---|
| `exec.LookPath("claude")` returns ErrNotFound | Phase 0 fails fast: "the `claude` command is not on PATH; install Claude Code and run `claude /login`" |
| Non-zero exit | Stderr is bubbled to the user verbatim; ritual aborts |
| JSON-parse failure (typed schema) | Retry once; on second failure, abort |
| Timeout | "the world is taking too long to remember" — abort |
| Empty result | Treated as JSON-parse failure |

## 9. Logo

`internal/firstcontact/logo.go` renders the 10 letters E-I-D-O-P-S-Y-C-H-E around a circle of ~6 lines × ~24 cols (terminal-character grid). Letters are placed at angles `θ_k = 2π·k/10` for `k ∈ [0, 10)`; cell `(row, col) = (centerRow - r·cos(θ), centerCol + 2·r·sin(θ))` (the `2·` corrects for terminal cell aspect ratio).

CLI animation:

- Refresh rate: 12 fps.
- Total duration: 3 seconds (~36 frames).
- Per-frame θ-advance: `2π / (12·8)` (one full rotation every 8 seconds).
- After 3 seconds, the renderer holds the static frame and proceeds.

Capability fallback: if `Capabilities().IsTTY == false` OR `TERM=dumb` OR no ANSI support, the renderer prints a single static block-art version that contains all 10 letters once, then proceeds without animation.

## 10. Testing

### 10.1 Unit tests

| Package | Tests |
|---|---|
| `internal/firstcontact` | slug derivation (Latin / CJK / emoji / collision / length cap); phase 1 relay validation + already-initialized error path; phase 2 claude-fake malformed-JSON triggers one retry then aborts; phase 3 markdown rendering with all summoning fields populated; ready channel coalesces + propagates failure |
| `internal/firstcontact/render` | CLI capability detection (TTY / pipe / dumb-term); typewriter respects `context.Cancel`; status handle is concurrency-safe |
| `internal/firstcontact` (logo) | Angle math: each letter ends in the expected `(row, col)` for known θ values; static fallback contains all 10 letters; 3-second animation respects context cancellation |
| `internal/identity` | `bootstrap_test.go` — newly extracted helper; ensures `gate.runInit` and `firstcontact` produce identical state.db rows |
| `internal/wake` | `BirthSignal` JSON round-trip; supervisor priority `birth.json > pending.json > active.json` (against existing wake test scaffold); double-birth refusal when `born_at` exists |
| `internal/ontology` | extend `scaffold_test.go` — new `journal/.keep`, `essence/.keep` paths land in tar; non-empty `JournalEntry` produces a literal `journal/0000-summoning.md` tar entry with the bytes preserved (including `{{` literals); empty `JournalEntry` does not produce the entry; `essence/secret.md` is in `.gitignore` |

### 10.2 Fakes

`internal/firstcontact/fakes_test.go`:

- `fakeClaude` — programmable response queue (supports "fail with parse error then succeed" for retry test); records prompts for assertion.
- `fakeRenderer` — captures every method call to a slice; pre-canned answers for prompts.
- `fakeDocker` — reuses existing `forgectl.fakeClient`.
- `fakeIdentity` — deterministic keypair from seed.

`Run()` takes a `Deps` struct (clock, fs, docker client, identity factory, claude caller, renderer); all five fakes drop in without monkey-patching.

### 10.3 Integration tests (`-tags=integration`)

`cmd/eidos/firstcontact_integration_test.go`:

1. **Happy path** — fake claude returning canned profile / displaying / calling-words; real Docker; real ontology scaffold. Asserts: state.db created with expected rows, volume `eidos-mindform-yu` exists, `journal/0000-summoning.md` present in the volume, container started, `essence/born_at` eventually written, `journal/0000-response.md` eventually written. Skipped if Docker daemon absent (existing CI convention).
2. **Boot-wake retry** — fake claude that crashes mid-birth (terminate after writing `secret.md` but before `born_at`). Supervisor restart re-consumes `birth.json`. Verifies `born_at` is eventually set and `journal/0000-response.md` lands.

### 10.4 Out of automated test scope

- Live `claude` calls in CI (claude is faked everywhere). Manual end-to-end smoke is documented in `EXAMPLE.md` for releases.
- Live relay calls (relay reachability is faked).
- Logo visual rendering — terminal capability detection is asserted; the actual rendered output is left to manual visual review during release.

### 10.5 Manual smoke checklist (in CHANGELOG)

For releases:

1. `rm -rf ~/.config/eidos && eidos` → wizard launches.
2. Pick `zh`, enter label "测试", pick public-station relay default.
3. Enter character "庄子", confirm derived slug `zhuangzi`.
4. Watch the typewriter response render.
5. `eidos forge logs zhuangzi` → see the running container.

## 11. Out of scope

- WebUI rendering (designed for, not implemented).
- A self-host-relay sub-wizard (phase 1 prints standalone instructions and exits cleanly).
- A "regenerate calling-words" path (forbidden by spec).
- Resumability of an interrupted ritual (forbidden by spec).
- A `forge.create` IPC method that routes through the daemon (deferred — `forge.Orchestrate` stays direct, marked as the bootstrap exception).
- Configurable claude model (Sonnet hardcoded per spec).
- Configurable typewriter rate (~30 chars/s constant for now).

## 12. Open questions deferred

- Default public relay URL — the spec uses `wss://relay.damus.io` as a placeholder. Whether the project ships its own community relay as the default is a release-time policy decision, not an implementation question. Code reads it from a `firstcontact/defaults.go` constant.
- Whether `essence/secret.md` deserves a kernel-level uppermost-isolation enforcement (e.g. an LSM rule, FUSE overlay) — out of scope for this milestone; we rely on filesystem permissions + agent prompt discipline.

## 13. Migration

No migration: this is an additive feature. Existing users with `~/.config/eidos/state.db` see bare `eidos` print cobra's standard help (existing behavior). They opt into the new wizard explicitly via `eidos summon`.

The two-command bootstrap path (`eidos gate init` + `eidos forge create`) remains supported and documented.
