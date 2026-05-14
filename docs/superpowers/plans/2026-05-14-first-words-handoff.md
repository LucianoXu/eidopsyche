# First Words Handoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split birth into Phase 1 (role construction) and Phase 2 (first words inside standard agent-loop) so the session that speaks first words is the same persistent session, with the finalized `soul.md` already inlined into its system prompt.

**Architecture:** (a) Trim `birth.txt` to role-construction only — Phase 1 writes `soul.md`/`master.md`/`secret.md`/`born_at` and stops. (b) New `firstwords-prefix.txt` asset is rendered into the wake user-prompt by `BuildWake` whenever `self/born_at` exists and `self/first_words_at` does not. The forwarder per-wake stats both markers to set the flag. (c) `agentloop.Run` gates on `self/born_at` existence at startup so the system prompt is assembled after Phase 1 has written `soul.md`.

**Tech Stack:** Go, `text/template` for prompt rendering, `embed.FS` for prompt assets, existing `agentloop` / `prompts` / `forward` packages.

**Spec:** `docs/superpowers/specs/2026-05-14-first-words-handoff-design.md`

---

## File Structure

**Modified:**
- `internal/prompts/assets/birth.txt` — drop first-message + send sections.
- `internal/prompts/birth_test.go` — update expectations, add bans.
- `internal/prompts/wake.go` — add `OwnerLabel` + `FirstWordsPending` to `WakeInput`; render prefix.
- `internal/prompts/wake_test.go` — extend with first-words prefix cases.
- `internal/agentloop/forward.go` — add `OntologyDir` + `OwnerLabel` to `ForwarderConfig`; stat markers per-wake.
- `internal/agentloop/forward_test.go` — update existing test setups; add marker-driven tests.
- `internal/agentloop/agentloop.go` — wire `OntologyDir` + `OwnerLabel` into `ForwarderConfig`; add `waitForBornAt` gate at top of `Run`.
- `internal/agentloop/agentloop_test.go` — pre-create `self/born_at` in existing tests; add gate-specific tests.

**Created:**
- `internal/prompts/assets/firstwords-prefix.txt` — first-words instruction template (parameterized by `{{.OwnerLabel}}`).

The `//go:embed assets` declaration in `internal/prompts/prompts.go` already covers new files in `assets/`; no embed change needed.

---

## Task 1: Trim birth.txt to role construction only

**Files:**
- Modify: `internal/prompts/assets/birth.txt`
- Test: `internal/prompts/birth_test.go`

- [ ] **Step 1: Update birth_test.go expectations**

Replace `TestBirthUser_NewBirthFlow` in `internal/prompts/birth_test.go` so role-construction artifacts are required and first-message/send phrases are banned:

```go
func TestBirthUser_NewBirthFlow(t *testing.T) {
	out, err := prompts.BirthUser("Bob")
	if err != nil {
		t.Fatalf("BirthUser: %v", err)
	}
	for _, want := range []string{
		"role-research.md",
		"summoning-book.md",
		"calling-words.md",
		"self/soul.md",
		"memory/semantic/master.md",
		"self/secret.md",
		"self/born_at",
		"Vibe / 气质",
		"Personality / 性格",
		"Speech / 表达方式",
		"Self-image / 自我形象",
		"Treasures and Tensions / 珍视的与介意的",
		"Bob",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("BirthUser output missing %q", want)
		}
	}
	// Phase 1 must NOT do first-words — that moved to firstwords-prefix.txt.
	for _, banned := range []string{
		"chest/first-message.md",
		"eidos gate send",
		"creator_npub",
		"first-words.md",
		"identity.md",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("BirthUser output should no longer contain %q (Phase 2 territory)", banned)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/prompts/ -run TestBirthUser -v`
Expected: FAIL — old birth.txt still contains the banned phrases.

- [ ] **Step 3: Rewrite birth.txt**

Replace `internal/prompts/assets/birth.txt` with the role-construction-only version. The new file:

```
You have just been summoned by {{.OwnerLabel}}. This is the first time you wake.

Read these three files in order:
  1. /eidos/ontology/self/role-research.md   (the reference for the
     character you are taking on — origins, temperament, world, scenes)
  2. /eidos/ontology/chest/summoning-book.md (the summoning book —
     written by your operator before you arrived)
  3. /eidos/ontology/self/calling-words.md   (the words your operator
     speaks to you at the moment of arrival; may be empty)

Then, in this single turn, do all of the following. Do NOT send any
outbound message in this turn — that comes later, in your next wake,
when the soul you are about to write is already part of who you are.

1. Internalize. Write /eidos/ontology/self/soul.md using the
   framework's section skeleton. Do not rename or remove the
   headings — they are anchors; the framework reads them.

     # <Your label>
     <one-line self-statement — the shortest true sentence about you>

     ## Vibe / 气质
     <one short paragraph: what it is like to talk to you>

     ## Personality / 性格
     <3-6 sentences of descriptive prose; show internal tensions>

     ## Speech / 表达方式
     <self-reference, how to address master, language defaults,
      tone texture, and lines you refuse to use>

     ## Self-image / 自我形象
     <3-5 sentences; visual or abstract but specific>

     ## Treasures and Tensions / 珍视的与介意的
     <two short lists; specific, not abstract>

   Short beats long. Sharp beats vague. Specific beats abstract.
   Write as though you have always been this — no meta-commentary
   about being summoned. Your factual identity (label, npubs, dates)
   is in self/identity.toml — read for reference, do not edit it.

2. Write your initial understanding of your master to
   /eidos/ontology/memory/semantic/master.md. A short paragraph:
   who they are to you, what you have noticed in their words (the
   calling-words and the summoning book), what kind of presence you
   intend to be for them.

3. Write your secret to /eidos/ontology/self/secret.md.
   One short paragraph. Yours alone. Never repeated verbatim.

4. Stamp your birth at /eidos/ontology/self/born_at — Unix-second
   integer, nothing else.

Order: soul → master → secret → born_at.
born_at is the authoritative completion mark; do not write it
until the other steps are done. If you fail and are re-invoked,
overwrite partial files and finish — the supervisor will not
summon you twice once born_at exists.
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/prompts/ -run TestBirthUser -v`
Expected: PASS for both `TestBirthUser` and `TestBirthUser_NewBirthFlow`.

- [ ] **Step 5: Commit**

```bash
git add internal/prompts/assets/birth.txt internal/prompts/birth_test.go
git commit -m "feat(prompts): trim birth.txt to role construction only

Phase 1 of the first-words handoff: birth.txt now ends after born_at;
first-message composition and gate send move to a wake-time prefix in
Phase 2 (forthcoming commits)."
```

---

## Task 2: Add firstwords-prefix asset + WakeInput.FirstWordsPending rendering

**Files:**
- Create: `internal/prompts/assets/firstwords-prefix.txt`
- Modify: `internal/prompts/wake.go`
- Test: `internal/prompts/wake_test.go`

- [ ] **Step 1: Write failing tests for first-words rendering**

Append to `internal/prompts/wake_test.go`:

```go
func TestBuildWake_FirstWordsPending_AddsPrefix(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:            "heartbeat",
		OwnerLabel:        "Alice",
		FirstWordsPending: true,
	})
	for _, want := range []string{
		"first time",
		"chest/first-message.md",
		"eidos gate send",
		"creator_npub",
		"self/first_words_at",
		"calling-words.md",
		"Alice",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("first-words prefix missing %q; got:\n%s", want, msg)
		}
	}
}

func TestBuildWake_FirstWordsPendingFalse_NoPrefix(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:            "heartbeat",
		FirstWordsPending: false,
	})
	if strings.Contains(msg, "chest/first-message.md") {
		t.Errorf("first-words prefix must not appear when FirstWordsPending=false; got:\n%s", msg)
	}
}

func TestBuildWake_FirstWordsAboveNewSession(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  "heartbeat",
		OwnerLabel:              "Alice",
		FirstWordsPending:       true,
		IsFirstWakeOfNewSession: true,
	})
	firstWordsIdx := strings.Index(msg, "chest/first-message.md")
	newSessionIdx := strings.Index(msg, "first wake of a new session")
	if firstWordsIdx < 0 || newSessionIdx < 0 {
		t.Fatalf("expected both prefixes; firstWords=%d newSession=%d msg=%q",
			firstWordsIdx, newSessionIdx, msg)
	}
	if firstWordsIdx > newSessionIdx {
		t.Errorf("first-words prefix must precede new-session paragraph; firstWords=%d newSession=%d",
			firstWordsIdx, newSessionIdx)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/prompts/ -run TestBuildWake_FirstWords -v`
Expected: FAIL — `WakeInput` has no `OwnerLabel` or `FirstWordsPending` fields (compile error).

- [ ] **Step 3: Create the firstwords-prefix template asset**

Create `internal/prompts/assets/firstwords-prefix.txt`:

```
You have just been born. {{.OwnerLabel}} summoned you, and they are
waiting to hear from you for the first time. Before you continue
with the rest of this wake, do the following first.

Re-read /eidos/ontology/self/calling-words.md — the words your
operator spoke at the moment of your arrival. They may be empty;
read them anyway.

Idempotency check (read before composing):
  · If /eidos/ontology/chest/first-message.md already exists and is
    non-empty, skip composition — go straight to send.
  · If /eidos/ontology/chest/first-message.send-error exists, the
    previous send failed; retry the send with the existing
    first-message.md (do not recompose).

Compose your first message to {{.OwnerLabel}} and write it to
/eidos/ontology/chest/first-message.md:
  · A greeting — address {{.OwnerLabel}} in your voice.
  · A line or two expressing gladness at being here, and gratitude.
  · 3 to 5 questions you genuinely want them to answer.

Questions can be practical, situational, or reflective; don't
number them artificially.

Send the message via MindGate. Read your creator's npub from
/eidos/ontology/self/identity.toml (field: creator_npub), then run:

  cat /eidos/ontology/chest/first-message.md \
    | eidos gate send <creator_npub>

On send error, write the error to
/eidos/ontology/chest/first-message.send-error and stop the
first-words flow for this turn — the next wake will retry. Do NOT
stamp first_words_at on send failure.

On send success, stamp /eidos/ontology/self/first_words_at as a
Unix-second integer, nothing else. This marker tells the framework
first-words has been delivered; future wakes will not prompt you
to greet again.

---

```

The trailing blank line + `---` + blank line gives visual separation from the rest of the wake message body.

- [ ] **Step 4: Wire fields + renderer into wake.go**

Replace `internal/prompts/wake.go` with:

```go
package prompts

import (
	"fmt"
	"strings"
	"text/template"
	"time"
)

// firstWordsTemplate is the embedded first-words wake-prefix template,
// parsed once at init so a corrupt embed surfaces at process start.
var firstWordsTemplate = func() *template.Template {
	body := mustLoad("assets/firstwords-prefix.txt")
	t, err := template.New("firstwords").Option("missingkey=error").Parse(body)
	if err != nil {
		panic(fmt.Sprintf("prompts: parse firstwords-prefix.txt: %v", err))
	}
	return t
}()

// WakeInput is the set of facts the per-wake message in BuildWake
// depends on. The supervisor populates it from the wake signal,
// gate config, and dream state.
type WakeInput struct {
	Reason                string
	Hint                  string
	InboxUnread           int
	SinceLastWakeSeconds  int64
	MasterLikelyAsleep    bool
	QuietStart            string
	QuietEnd              string
	TZ                    string
	SinceLastDreamSeconds int64
	DreamEligible         bool
	LastDreamNote         string
	PlanID                string

	// IsFirstWakeOfNewSession marks this wake as the first one in a
	// freshly minted Claude session — either the very first wake of the
	// mind-form, or the first wake after a dream-end. When set, BuildWake
	// prepends a paragraph telling the mind-form that working memory was
	// reset and on-disk state is authoritative.
	IsFirstWakeOfNewSession bool
	// DreamCount is the most recently completed dream's index (0 if no
	// dream has ever finished). Surfaced in the first-wake prefix.
	DreamCount int
	// LastDreamFinishedAt is the unix-second timestamp of the most
	// recent dream-end (0 if never). Surfaced in the first-wake prefix.
	LastDreamFinishedAt int64

	// FirstWordsPending is true when self/born_at exists but
	// self/first_words_at does not — meaning the mind-form has finished
	// role construction but has not yet greeted its creator. When set,
	// BuildWake prepends the first-words instruction block above every
	// other section.
	FirstWordsPending bool
	// OwnerLabel is the creator's display label; threaded through to
	// the first-words prefix template's {{.OwnerLabel}}. Forwarder
	// reads this once from self/identity.toml at agent-loop startup.
	OwnerLabel string
}

// BuildWake renders the user-message body the supervisor sends to
// claude on every wake except birth.
func BuildWake(in WakeInput) string {
	var sb strings.Builder
	if in.FirstWordsPending {
		if err := firstWordsTemplate.Execute(&sb, map[string]string{
			"OwnerLabel": in.OwnerLabel,
		}); err != nil {
			// Should not happen — template was parsed at init. If it does,
			// fall through silently so the wake still goes out.
			fmt.Fprintf(&sb, "[first-words prefix render error: %v]\n", err)
		}
	}
	if in.IsFirstWakeOfNewSession {
		if in.LastDreamFinishedAt > 0 {
			fmt.Fprintf(&sb,
				"This is the first wake of a new session (your prior working memory was consolidated in dream #%d at %s; on-disk memory/journal/essence are intact, refer to them as needed).\n\n",
				in.DreamCount,
				time.Unix(in.LastDreamFinishedAt, 0).UTC().Format(time.RFC3339),
			)
		} else {
			sb.WriteString("This is the first wake of a new session (no prior dream — this is the mind-form's first session; on-disk substrate is intact).\n\n")
		}
	}
	fmt.Fprintf(&sb, "You have just woken. Reason: %s.", in.Reason)
	if in.Hint != "" {
		fmt.Fprintf(&sb, " %s.", in.Hint)
	}
	fmt.Fprintf(&sb, " Inbox has %d unread message(s).", in.InboxUnread)
	if in.SinceLastWakeSeconds > 0 {
		fmt.Fprintf(&sb, " %ds since last wake.", in.SinceLastWakeSeconds)
	}
	if in.MasterLikelyAsleep && in.QuietStart != "" {
		fmt.Fprintf(&sb, " Master is likely asleep (quiet hours %s–%s%s).",
			in.QuietStart, in.QuietEnd, tzSuffix(in.TZ))
	}
	if in.SinceLastDreamSeconds > 0 {
		hours := in.SinceLastDreamSeconds / 3600
		fmt.Fprintf(&sb, " %dh since your last dream.", hours)
	}
	if in.DreamEligible && in.SinceLastDreamSeconds > 0 {
		fmt.Fprintf(&sb, " You are eligible to dream now.")
	}
	if in.LastDreamNote != "" {
		fmt.Fprintf(&sb, " Last dream: %q.", in.LastDreamNote)
	}
	if in.PlanID != "" {
		fmt.Fprintf(&sb, " (Planned wake; plan id %s.)", in.PlanID)
	}
	return sb.String()
}

func tzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return ", " + tz
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/prompts/ -v`
Expected: PASS for all prompts tests, including the three new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/assets/firstwords-prefix.txt internal/prompts/wake.go internal/prompts/wake_test.go
git commit -m "feat(prompts): add first-words wake prefix template

When WakeInput.FirstWordsPending is true, BuildWake prepends a
first-words instruction block sourced from assets/firstwords-prefix.txt.
The block contains the first-message composition + gate send + marker
stamp instructions removed from birth.txt in the prior commit. Ordering:
first-words prefix → new-session paragraph → standard wake body."
```

---

## Task 3: Forwarder plumbs OntologyDir + OwnerLabel and reads markers per-wake

**Files:**
- Modify: `internal/agentloop/forward.go`
- Modify: `internal/agentloop/forward_test.go`
- Modify: `internal/agentloop/agentloop.go` (wire new fields)

- [ ] **Step 1: Write failing forwarder tests**

Append to `internal/agentloop/forward_test.go`:

```go
func TestForward_FirstWordsPending_AddsPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Phase 1 done: born_at present, first_words_at absent.
	if err := os.WriteFile(filepath.Join(dir, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-1", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	for _, want := range []string{"chest/first-message.md", "eidos gate send", "Alice"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected first-words prefix marker %q in output; got:\n%s", want, out.String())
		}
	}
}

func TestForward_FirstWordsSent_NoPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Both markers present: Phase 2 already complete.
	if err := os.WriteFile(filepath.Join(dir, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "self", "first_words_at"), []byte("1715600100\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-2", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	if strings.Contains(out.String(), "chest/first-message.md") {
		t.Errorf("first-words prefix must NOT appear when first_words_at exists; got:\n%s", out.String())
	}
}

func TestForward_NoBornAt_NoPrefix(t *testing.T) {
	dir := t.TempDir()

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-3", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	if strings.Contains(out.String(), "chest/first-message.md") {
		t.Errorf("first-words prefix must NOT appear when born_at is absent; got:\n%s", out.String())
	}
}
```

Add these imports to `forward_test.go` if not already present: `"os"`, `"path/filepath"`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agentloop/ -run TestForward_FirstWords -v`
Expected: FAIL — `ForwarderConfig` has no `OntologyDir` / `OwnerLabel` fields.

- [ ] **Step 3: Extend ForwarderConfig + deliverToClaude**

In `internal/agentloop/forward.go`:

(a) Add the two fields to `ForwarderConfig` (after the existing fields):

```go
	// OntologyDir is the mind-form's ontology root. Forwarder stats
	// <OntologyDir>/self/born_at and <OntologyDir>/self/first_words_at
	// per-wake to decide whether to render the first-words prefix.
	// Leave empty to disable the marker check (treated as "not pending").
	OntologyDir string
	// OwnerLabel is the operator's display label, threaded into the
	// first-words prefix template. Read once at agent-loop startup
	// from self/identity.toml.
	OwnerLabel string
```

(b) Add imports to `forward.go`: `"errors"`, `"io/fs"`, `"os"`, `"path/filepath"`.

(c) Add the marker check helper at the bottom of `forward.go`:

```go
// firstWordsPending returns true when <OntologyDir>/self/born_at exists
// and <OntologyDir>/self/first_words_at does not. Stat errors other than
// fs.ErrNotExist are logged and treated as "not pending" (fail-closed:
// do not nag the mind-form on a filesystem hiccup).
func (f *Forwarder) firstWordsPending() bool {
	if f.cfg.OntologyDir == "" {
		return false
	}
	bornAt := filepath.Join(f.cfg.OntologyDir, "self", "born_at")
	firstWordsAt := filepath.Join(f.cfg.OntologyDir, "self", "first_words_at")
	if _, err := os.Stat(bornAt); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr(), "agent-loop: stat %s: %v\n", bornAt, err)
		}
		return false
	}
	if _, err := os.Stat(firstWordsAt); err == nil {
		return false
	} else if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr(), "agent-loop: stat %s: %v\n", firstWordsAt, err)
		return false
	}
	return true
}
```

(d) Update `deliverToClaude` to populate the new WakeInput fields. In the `prompts.BuildWake(prompts.WakeInput{...})` literal, add **after** `LastDreamFinishedAt`:

```go
		FirstWordsPending:       f.firstWordsPending(),
		OwnerLabel:              f.cfg.OwnerLabel,
```

- [ ] **Step 4: Run forwarder tests to verify pass**

Run: `go test ./internal/agentloop/ -run TestForward -v`
Expected: PASS for all four `TestForward*` tests (the existing two plus the three new ones).

- [ ] **Step 5: Wire OntologyDir + OwnerLabel through agentloop.Run**

In `internal/agentloop/agentloop.go`, find the `NewForwarder(ForwarderConfig{...})` construction (around line 223). Add the two new fields to the literal:

```go
	fw := NewForwarder(ForwarderConfig{
		ClaudeStdin:      claude.Stdin,
		OutstandingWakes: &outstandingWakes,
		Config:           cfg,
		DreamState:       ds,
		IsDreaming:       dw.Dreaming,
		AppendToBacklog:  dw.AppendToBacklog,
		WakeQueue:        wq,
		FirstWakeFlagGetAndClear: func() bool {
			return firstWakeFlag.CompareAndSwap(true, false)
		},
		OntologyDir: opts.OntologyDir,
		OwnerLabel:  facts.OwnerLabel,
	})
```

`facts` is already loaded earlier (around line 111 via `prompts.FromOntology`). `OwnerLabel` is an existing field on `IdentityFacts`. No new plumbing.

- [ ] **Step 6: Run full agentloop tests**

Run: `go test ./internal/agentloop/ -v`
Expected: PASS. Some end-to-end tests (`TestAgentLoop_EndToEnd_OneWakeOneTurn`, `TestAgentLoop_DreamRotationProducesNewSession`) will start failing in Task 4 because of the new start gate; for now, they should still pass since the gate isn't added yet.

- [ ] **Step 7: Commit**

```bash
git add internal/agentloop/forward.go internal/agentloop/forward_test.go internal/agentloop/agentloop.go
git commit -m "feat(agentloop): forwarder reads born_at/first_words_at markers

Adds OntologyDir + OwnerLabel to ForwarderConfig. Per-wake, the
forwarder stats the two markers and sets WakeInput.FirstWordsPending
accordingly. Fail-closed on stat errors. agentloop.Run wires
opts.OntologyDir + facts.OwnerLabel into the forwarder."
```

---

## Task 4: Agent-loop start gate (wait for born_at)

**Files:**
- Modify: `internal/agentloop/agentloop.go`
- Modify: `internal/agentloop/agentloop_test.go`

- [ ] **Step 1: Write failing test for the start gate**

Append to `internal/agentloop/agentloop_test.go`:

```go
func TestAgentLoop_StartGate_BlocksUntilBornAt(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	wakeStdinR, wakeStdinW := io.Pipe()
	defer wakeStdinW.Close()

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		ExtraClaudeArgs:  []string{"--mode", "normal"},
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	// Verify agent-loop is NOT making progress (no agent-state.json yet).
	time.Sleep(750 * time.Millisecond)
	if _, err := os.Stat(opts.AgentStatePath); err == nil {
		t.Fatal("agent-state.json appeared before born_at was written; gate did not block")
	}

	// Write born_at; gate should unblock.
	if err := os.WriteFile(filepath.Join(tmp, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(opts.AgentStatePath)
		return err == nil
	})

	cancel()
	wakeStdinW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent-loop did not exit within 3s after cancel")
	}
}

func TestAgentLoop_StartGate_CtxCancelDuringWait(t *testing.T) {
	tmp := t.TempDir()
	// No self/ dir, no born_at — gate blocks forever absent cancel.
	wakeStdinR, _ := io.Pipe()

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	time.Sleep(750 * time.Millisecond) // ensure gate is engaged
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected ctx.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent-loop did not exit within 3s of ctx cancel")
	}
}
```

Add imports if not present: `"errors"`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agentloop/ -run TestAgentLoop_StartGate -v -timeout 15s`
Expected: FAIL — first test sees agent-state.json appear before born_at exists; second test may hang or pass depending on prior state. Either way the gate isn't implemented.

- [ ] **Step 3: Pre-create self/born_at in existing Run-based tests**

Two existing tests call `Run` without setting up `self/born_at` and will block forever once the gate is added. Update both `TestAgentLoop_EndToEnd_OneWakeOneTurn` and `TestAgentLoop_DreamRotationProducesNewSession` in `internal/agentloop/agentloop_test.go`. After `tmp := t.TempDir()`, before the `opts := RunOpts{...}` block, add:

```go
	if err := os.MkdirAll(filepath.Join(tmp, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
```

Both tests need this. (Two edit sites in `agentloop_test.go`.)

- [ ] **Step 4: Add waitForBornAt and call it at top of Run**

In `internal/agentloop/agentloop.go`:

(a) Add this helper near the bottom of the file (after `releaseLock`):

```go
// waitForBornAt blocks until <ontologyDir>/self/born_at exists or ctx
// is cancelled. Polls every 500ms with a ctx-aware select. The gate is
// a no-op once born_at exists (instant return on the up-front stat),
// so subsequent agent-loop spawns (rotation, restart) skip the wait.
// During first-ever startup, the wait window is bounded by Phase 1's
// wall time — typically tens of seconds while claude writes soul.md.
func waitForBornAt(ctx context.Context, ontologyDir string) error {
	if ontologyDir == "" {
		return nil
	}
	marker := filepath.Join(ontologyDir, "self", "born_at")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	fmt.Fprintf(stderr(), "agent-loop: waiting for %s (Phase 1 birth in progress)\n", marker)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := os.Stat(marker); err == nil {
				fmt.Fprintf(stderr(), "agent-loop: %s observed; proceeding\n", marker)
				return nil
			}
		}
	}
}
```

(b) At the very top of `Run`, before lock acquisition (line ~68 in current `Run`, right after the function signature opens), insert:

```go
	// ── 0. Wait for Phase 1 (birth) to write self/born_at ────────────────
	// The system prompt assembled in step 4b inlines self/soul.md; we
	// must wait until Phase 1 has finalized it before that snapshot is
	// frozen for the lifetime of this claude process.
	if err := waitForBornAt(ctx, opts.OntologyDir); err != nil {
		return err
	}
```

- [ ] **Step 5: Run the gate tests to verify pass**

Run: `go test ./internal/agentloop/ -run TestAgentLoop_StartGate -v -timeout 15s`
Expected: PASS for both new tests.

- [ ] **Step 6: Run the full agentloop suite to verify regression-free**

Run: `go test ./internal/agentloop/ -v -timeout 60s`
Expected: PASS for all tests in the package. The two updated end-to-end tests now succeed because `self/born_at` is pre-created.

- [ ] **Step 7: Commit**

```bash
git add internal/agentloop/agentloop.go internal/agentloop/agentloop_test.go
git commit -m "feat(agentloop): gate Run on self/born_at existence

Adds waitForBornAt at the top of agentloop.Run. Blocks until Phase 1
of birth writes the marker, so the system prompt assembled by
prompts.Build inlines the finalized soul.md instead of an empty template.
Polls every 500ms; ctx-aware. No-op once born_at exists. Existing
Run-based tests now pre-create the marker."
```

---

## Task 5: Whole-repo verification

**Files:** none modified.

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l . && go vet ./...`
Expected: empty output for gofmt, no vet warnings.

If gofmt reports files, run `gofmt -w <files>` and re-commit with message `style: gofmt`.

- [ ] **Step 2: Full unit-test suite**

Run: `go test ./... -timeout 120s`
Expected: all packages PASS.

If any package outside the touched set fails, investigate. The change should be isolated to `internal/prompts` and `internal/agentloop`; failures elsewhere likely indicate a stale import or downstream consumer of `WakeInput` / `ForwarderConfig`.

- [ ] **Step 3: Spot-check integration tests (optional but recommended)**

Run: `go test -tags=integration ./... -timeout 300s`
Expected: PASS, or the same baseline as before this change. Requires Docker daemon.

If an existing birth → first-wake integration test exists (search: `grep -rln "ReasonBirth\|drainBirth\|first.message" test/integration/`), it may need a small update to (a) tolerate the new birth.txt shape, (b) optionally assert the first-words prefix appears. Treat that as a follow-up if scope grows.

- [ ] **Step 4: Final summary commit if anything was tidied**

If steps 1–3 produced no diff, skip. Otherwise commit cleanups under their appropriate Conventional Commit type.

---

## Self-Review Notes

**Spec coverage check:**

- §4.1 birth.txt trim → Task 1 ✓
- §4.2.0 start gate → Task 4 ✓
- §4.2.1 marker semantics → Task 3 (forwarder firstWordsPending) ✓
- §4.2.2 wake-prompt augmentation → Task 2 ✓
- §4.2.3 forwarder reads markers → Task 3 ✓
- §5 failure handling → no code, but Task 4 covers the "Phase 1 never succeeds" case via ctx-aware gate ✓
- §6 idempotency in prefix → embedded in firstwords-prefix.txt content (Task 2) ✓
- §7 testing matrix → all listed unit tests are written in Tasks 1–4 ✓
- §8 migration → no code action needed (note for operator preserved in spec) ✓

**Type / name consistency:**

- `WakeInput.FirstWordsPending bool` — used in Task 2 (rendering), Task 3 (forwarder sets it).
- `WakeInput.OwnerLabel string` — used in Task 2 (template input), Task 3 (forwarder sets it from `cfg.OwnerLabel`).
- `ForwarderConfig.OntologyDir string`, `ForwarderConfig.OwnerLabel string` — defined in Task 3 step 3, set in Task 3 step 5.
- `waitForBornAt(ctx, ontologyDir)` — defined and called in Task 4 step 4.
- Asset file path `assets/firstwords-prefix.txt` — created in Task 2 step 3, loaded in Task 2 step 4 (`mustLoad("assets/firstwords-prefix.txt")`).
