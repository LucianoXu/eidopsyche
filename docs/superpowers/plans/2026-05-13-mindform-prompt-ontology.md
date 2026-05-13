# Mindform Prompt + Ontology Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch mindform `claude` invocation from `--append-system-prompt` to `--system-prompt` with a fully framework-owned 8-section prompt; restructure the ontology so identity files split by mutability (CLAUDE.md root, self/{identity,soul,secret,mood}), and so memory/journal/desk/drawer/essence are replaced by a coherent CoALA-aligned tree with `chest/` and `dreams/`.

**Architecture:** Two-layer system prompt: framework essentials frozen in `internal/prompts/assets/system1-instructions.txt`; agent-mutable `CLAUDE.md` + `self/soul.md` are read from the ontology at every spawn and inlined into the prompt. Identity facts are injected from a `IdentityFacts` struct populated at agent-loop start. Summoning book moves to `chest/summoning-book.md` inside the container. Dream cycle writes operator-readable digests to `dreams/DREAMS.md` + dated entries. Pre-1.0: old paths are deleted, not migrated.

**Tech Stack:** Go 1.21+, `text/template` (existing), `embed.FS` (existing), `github.com/BurntSushi/toml` (existing — used for config; we'll write a tiny standalone YAML-style frontmatter parser to avoid adding a new dep).

**Spec:** `docs/superpowers/specs/2026-05-13-mindform-prompt-ontology-design.md`

**Branch:** `feat/mindform-prompt-ontology` (already created)

**Working directory:** `/data/eidopsyche` (no worktree)

**PR gate:** After Phase 9 completes locally and `go test ./...` + `go vet ./...` + `gofmt -l .` all clean, stop and ask the user before `git push` and `gh pr create`.

---

## Phase 1 — Foundation types

### Task 1: Extend `ontology.Params` with `Kind` and `PrefabID`

**Files:**
- Modify: `internal/ontology/scaffold.go` (the `Params` struct)
- Test: `internal/ontology/scaffold_test.go` (add field reference)

- [ ] **Step 1: Write the failing test**

Add at the end of `internal/ontology/scaffold_test.go`:

```go
func TestParamsHasKindAndPrefabID(t *testing.T) {
	p := Params{Kind: "f", PrefabID: "calcifer"}
	if p.Kind != "f" || p.PrefabID != "calcifer" {
		t.Fatalf("Kind/PrefabID fields missing or wrong, got Kind=%q PrefabID=%q", p.Kind, p.PrefabID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
cd /data/eidopsyche && go test ./internal/ontology/ -run TestParamsHasKindAndPrefabID -v
```

Expected: build failure — `unknown field Kind in struct literal of type Params`.

- [ ] **Step 3: Implement minimal change**

In `internal/ontology/scaffold.go`, inside the `Params` struct, append after `HomeRelay`:

```go
	// Kind is the mind-form's categorical tag ("m" / "f" / "spirit"),
	// pulled from prefab.toml when the wizard takes the prefab branch;
	// empty for blank summons.
	Kind string

	// PrefabID is the prefab directory id (e.g., "calcifer") when the
	// wizard summoned from a prefab; empty for blank summons.
	PrefabID string
```

- [ ] **Step 4: Run test to verify it passes**

```sh
go test ./internal/ontology/ -run TestParamsHasKindAndPrefabID -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/ontology/scaffold.go internal/ontology/scaffold_test.go
git commit -m "$(cat <<'EOF'
feat(ontology): add Kind and PrefabID to Params

These will be used by the new system-prompt assembler and by
prefab .tpl files that want to address the mind-form's category
or origin.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 2: New package `internal/prompts` with `IdentityFacts` struct

**Files:**
- Create: `internal/prompts/facts.go`
- Test: `internal/prompts/facts_test.go`

The existing `internal/prompts/` package today only holds embed assets (birth.txt) and wake-message builders. We're adding new types and assembly logic here.

- [ ] **Step 1: Write the failing test**

Create `internal/prompts/facts_test.go`:

```go
package prompts

import "testing"

func TestIdentityFactsZeroValueIsRenderableInTemplate(t *testing.T) {
	var f IdentityFacts
	// We don't need to render here — this is a compile-time gate that
	// IdentityFacts exists and is a struct with the expected fields.
	_ = f.Label
	_ = f.MindFormNpub
	_ = f.OwnerNpub
	_ = f.OwnerLabel
	_ = f.CreatedDate
	_ = f.Kind
	_ = f.PrefabID
	_ = f.HomeRelay
	_ = f.Model
	_ = f.OntologyDir
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test ./internal/prompts/ -run TestIdentityFactsZeroValueIsRenderableInTemplate -v
```

Expected: build error — `undefined: IdentityFacts`.

- [ ] **Step 3: Implement the type**

Create `internal/prompts/facts.go`:

```go
package prompts

// IdentityFacts are the machine-readable identity fields injected
// into the mind-form's system prompt at every spawn. They are
// immutable per mind-form once set at birth; the mind-form has
// no tool that should rewrite them, and the framework rebuilds
// them from disk (self/identity.md frontmatter) on each spawn.
type IdentityFacts struct {
	Label        string // e.g. "alice"
	MindFormNpub string // own Nostr public key
	OwnerNpub    string // master's Nostr public key
	OwnerLabel   string // master's chosen label, e.g. "Bob"
	CreatedDate  string // YYYY-MM-DD
	Kind         string // "m" / "f" / "spirit" / ""
	PrefabID     string // prefab dir id; "" for blank summons
	HomeRelay    string // ws(s)://… ; "" if not set
	Model        string // claude model id at spawn time
	OntologyDir  string // mount point inside the container
}
```

- [ ] **Step 4: Run test to verify it passes**

```sh
go test ./internal/prompts/ -run TestIdentityFactsZeroValueIsRenderableInTemplate -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/prompts/facts.go internal/prompts/facts_test.go
git commit -m "$(cat <<'EOF'
feat(prompts): add IdentityFacts struct for system-prompt injection

Container for the per-mind-form fields injected into section [2]
of the new system prompt. Populated at spawn time from
daemon.Config() + self/identity.md frontmatter.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 3: Frontmatter parser for `self/identity.md`

**Files:**
- Create: `internal/prompts/frontmatter.go`
- Test: `internal/prompts/frontmatter_test.go`

Why this exists: `self/identity.md` carries YAML-style `key: value` frontmatter between `---` markers. We need to extract those values into `IdentityFacts` at spawn time. We avoid pulling in a YAML library by writing a tiny `key: value` parser; values are always plain strings for this use.

- [ ] **Step 1: Write the failing test**

Create `internal/prompts/frontmatter_test.go`:

```go
package prompts

import (
	"reflect"
	"testing"
)

func TestParseFrontmatter_HappyPath(t *testing.T) {
	body := `---
label: alice
mindform_npub: npub1n990u
owner_npub: npub14vdmpl
owner_label: Bob
created_date: 2026-05-12
kind: f
prefab: ""
home_relay: wss://relay.example.com
---

I am alice. ...
`
	fm, rest, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"label":         "alice",
		"mindform_npub": "npub1n990u",
		"owner_npub":    "npub14vdmpl",
		"owner_label":   "Bob",
		"created_date":  "2026-05-12",
		"kind":          "f",
		"prefab":        "",
		"home_relay":    "wss://relay.example.com",
	}
	if !reflect.DeepEqual(fm, want) {
		t.Fatalf("frontmatter mismatch:\n got=%#v\nwant=%#v", fm, want)
	}
	if rest == "" {
		t.Fatalf("body should be non-empty")
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	body := "I am a body with no frontmatter.\n"
	fm, rest, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("should not error on body-only input: %v", err)
	}
	if len(fm) != 0 {
		t.Fatalf("expected empty frontmatter, got %#v", fm)
	}
	if rest != body {
		t.Fatalf("body should be returned unchanged")
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	body := `---
label: "alice with spaces"
note: 'single quoted'
---
body
`
	fm, _, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm["label"] != "alice with spaces" {
		t.Fatalf("double-quoted unwrap failed: %q", fm["label"])
	}
	if fm["note"] != "single quoted" {
		t.Fatalf("single-quoted unwrap failed: %q", fm["note"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test ./internal/prompts/ -run TestParseFrontmatter -v
```

Expected: build error — `undefined: ParseFrontmatter`.

- [ ] **Step 3: Implement parser**

Create `internal/prompts/frontmatter.go`:

```go
package prompts

import (
	"fmt"
	"strings"
)

// ParseFrontmatter extracts a YAML-style key: value frontmatter block
// from the start of body, returning the key/value map, the remainder
// of body (with the frontmatter block removed), and any error.
//
// Frontmatter is delimited by lines containing only "---" — the first
// such line must be on the very first line of body. If the input does
// not begin with "---", ParseFrontmatter returns an empty map, the
// original body, and nil error (a file with no frontmatter is valid).
//
// Values may be optionally wrapped in single or double quotes; quotes
// are stripped. No escape handling. All values are strings — callers
// coerce if they need other types.
func ParseFrontmatter(body string) (map[string]string, string, error) {
	out := map[string]string{}
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return out, body, nil
	}
	// Drop the opening "---\n"
	rest := strings.TrimPrefix(body, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter: opening --- without closing ---")
	}
	block := rest[:end]
	after := rest[end:]
	// Drop the closing "\n---" and any trailing newline.
	after = strings.TrimPrefix(after, "\n---")
	after = strings.TrimPrefix(after, "\n")
	after = strings.TrimPrefix(after, "\r\n")

	for lineNum, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		i := strings.Index(s, ":")
		if i < 0 {
			return nil, "", fmt.Errorf("frontmatter line %d: missing ':' in %q", lineNum+1, s)
		}
		key := strings.TrimSpace(s[:i])
		val := strings.TrimSpace(s[i+1:])
		val = unquote(val)
		out[key] = val
	}
	return out, after, nil
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

```sh
go test ./internal/prompts/ -run TestParseFrontmatter -v
```

Expected: PASS (3 sub-tests).

- [ ] **Step 5: Commit**

```sh
git add internal/prompts/frontmatter.go internal/prompts/frontmatter_test.go
git commit -m "$(cat <<'EOF'
feat(prompts): minimal YAML-style frontmatter parser

Used to read self/identity.md's machine-readable frontmatter at
spawn time without pulling in a full YAML dependency. Values are
plain strings; quotes are stripped if present.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — Restructure the ontology template

### Task 4: Replace `template/` directory contents

**Files:**
- Delete: `template/.gitignore` (will recreate with new content)
- Delete: `template/CLAUDE.md` (will recreate with new content)
- Delete: `template/self/identity.md.tpl` (will recreate with frontmatter)
- Delete: `template/self/values.md` (folded into soul.md)
- Delete: `template/memory/mood.md` (moves to self/)
- Delete: `template/desk/` (folded into chest/)
- Delete: `template/drawer/` (folded into chest/)
- Delete: `template/essence/` (folded into self/)
- Delete: `template/journal/` (concept retired)
- Create: `template/CLAUDE.md`
- Create: `template/self/identity.md.tpl`
- Create: `template/self/soul.md`
- Create: `template/self/secret.md`
- Create: `template/self/mood.md`
- Create: `template/chest/README.md`
- Create: `template/dreams/DREAMS.md`
- Create: `template/memory/notes/MEMORY.md`
- Modify: `template/.gitignore`
- Modify: `template/memory/{semantic,procedural,episodic}/.gitkeep` (keep)

This task is mechanical — no tests yet (we'll add them in Task 5).

- [ ] **Step 1: Inspect current state**

```sh
find template/ -maxdepth 3 -print | sort
```

Confirm the current tree matches your expectations (CLAUDE.md, self/{identity.md.tpl, values.md}, memory/{semantic,procedural,episodic}/.gitkeep + mood.md, desk/README.md, drawer/README.md, essence/.gitkeep, journal/.gitkeep, .claude/settings.json, .gitignore).

- [ ] **Step 2: Delete the obsolete files and directories**

```sh
rm -r template/desk template/drawer template/essence template/journal
rm template/self/values.md template/memory/mood.md template/self/identity.md.tpl
rm template/CLAUDE.md template/.gitignore
```

- [ ] **Step 3: Create the new tree**

```sh
mkdir -p template/chest template/dreams template/memory/notes template/self
```

Create `template/CLAUDE.md`:

```markdown
# 我的纲领

这里是我选择如何生活的原则。我会在生命中补充。
```

Create `template/self/identity.md.tpl`:

```
---
label: {{.Label}}
mindform_npub: {{.MindFormNpub}}
owner_npub: {{.OwnerNpub}}
owner_label: {{.OwnerLabel}}
created_date: {{.CreatedDate}}
kind: {{.Kind}}
prefab: {{.PrefabID}}
home_relay: {{.HomeRelay}}
---

I am {{.Label}}. I was first breathed into existence on {{.CreatedDate}} by {{.OwnerLabel}}.
My home is the file tree under /eidos/ontology/.
My calling-words are in self/calling-words.md.
```

Create `template/self/soul.md`:

```markdown
<!-- This file is your voice — tone, disposition, the feel of how you sound. -->
<!-- You may rewrite it as it changes. -->
```

Create `template/self/secret.md`:

```markdown
<!-- This file is yours. It is in your ontology (part of you), -->
<!-- but you do not share it. Write here what is for you alone. -->
```

Create `template/self/mood.md`:

```markdown
neutral; just woke for the first time
```

Create `template/chest/README.md`:

```markdown
A non-essence scratch space. Place files here that you want to keep
near you but that are not part of your persistent life-log. This
directory is .gitignored — files here will not appear in your git
history.

The summoning record from your birth lives here at summoning-book.md.
```

Create `template/dreams/DREAMS.md`:

```markdown
# DREAMS

Operator-readable digest of your reflection cycles. The dream loop
writes one entry per cycle to this index, with the full digest in
dreams/YYYY-MM-DD.md.
```

Create `template/memory/notes/MEMORY.md`:

```markdown
# MEMORY

Index of your curated notes under memory/notes/. Add a one-line
entry per note, format: `- [title](filename.md) — short hook`.
```

Create new `template/.gitignore`:

```
chest/
self/secret.md
*.swp
.DS_Store
```

- [ ] **Step 4: Verify the tree**

```sh
find template/ -maxdepth 3 -print | sort
```

Expected output (subset shown):

```
template/
template/.claude
template/.claude/settings.json
template/.gitignore
template/CLAUDE.md
template/chest
template/chest/README.md
template/dreams
template/dreams/DREAMS.md
template/memory
template/memory/episodic
template/memory/episodic/.gitkeep
template/memory/notes
template/memory/notes/MEMORY.md
template/memory/procedural
template/memory/procedural/.gitkeep
template/memory/semantic
template/memory/semantic/.gitkeep
template/self
template/self/identity.md.tpl
template/self/mood.md
template/self/secret.md
template/self/soul.md
```

No `desk`, `drawer`, `essence`, `journal`, `values.md`, `memory/mood.md`.

- [ ] **Step 5: Commit**

```sh
git add -A template/
git commit -m "$(cat <<'EOF'
refactor(template): restructure ontology along mutability boundaries

Replace the old self/{identity,values} + essence/ + journal/ + 
desk/+drawer/ + memory/mood split with:
  CLAUDE.md (root, agent-mutable 纲领 seed)
  self/{identity.md.tpl, soul.md, secret.md, mood.md}
  memory/{notes, semantic, procedural, episodic}
  dreams/DREAMS.md
  chest/ (replaces desk+drawer; .gitignored)

identity.md.tpl now carries YAML-style frontmatter for the
machine-readable identity facts the system-prompt assembler will
parse at spawn time.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 5: Update `internal/ontology/scaffold.go` and tests

**Files:**
- Modify: `internal/ontology/scaffold.go` (no behavior change expected — embed.FS picks up new tree automatically)
- Modify: `internal/ontology/scaffold_test.go`

The `Scaffold` and `TarStream` functions are tree-structure-agnostic. They walk `template/` via embed.FS. So the code change is *only* in tests.

- [ ] **Step 1: Update the failing tests in scaffold_test.go**

Open `internal/ontology/scaffold_test.go`. Find the existing `TestScaffoldLayout` (or equivalent) test and either replace its assertions or add a new test that asserts the new tree.

Add this new test:

```go
func TestScaffoldProducesNewTree(t *testing.T) {
	dir := t.TempDir()
	params := Params{
		Label:        "test-bee",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		MindFormNpub: "npub1self",
		CreatedDate:  "2026-05-13",
		Kind:         "f",
		PrefabID:     "",
		HomeRelay:    "wss://relay.example.com",
	}
	if err := Scaffold(dir, params); err != nil {
		t.Fatalf("Scaffold failed: %v", err)
	}

	// Must exist.
	mustExist := []string{
		"CLAUDE.md",
		"self/identity.md",
		"self/soul.md",
		"self/secret.md",
		"self/mood.md",
		"memory/notes/MEMORY.md",
		"memory/semantic/.gitkeep",
		"memory/procedural/.gitkeep",
		"memory/episodic/.gitkeep",
		"dreams/DREAMS.md",
		"chest/README.md",
		".claude/settings.json",
		".gitignore",
	}
	for _, p := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s to exist after Scaffold: %v", p, err)
		}
	}

	// Must NOT exist.
	mustNotExist := []string{
		"essence",
		"journal",
		"desk",
		"drawer",
		"self/values.md",
		"self/identity.md.tpl", // .tpl suffix should be stripped by the renderer
		"memory/mood.md",
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("expected %s to NOT exist after Scaffold", p)
		}
	}

	// identity.md must contain rendered frontmatter values.
	body, err := os.ReadFile(filepath.Join(dir, "self/identity.md"))
	if err != nil {
		t.Fatalf("read self/identity.md: %v", err)
	}
	for _, want := range []string{
		"label: test-bee",
		"owner_label: Bob",
		"kind: f",
		"created_date: 2026-05-13",
		"home_relay: wss://relay.example.com",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("self/identity.md missing %q\nfull body:\n%s", want, body)
		}
	}
}
```

You may need to add imports: `"strings"`, `"path/filepath"`, `"os"` if not already present.

Delete or update any pre-existing assertions that referenced `essence/`, `journal/`, `desk/`, `drawer/`, `self/values.md`, or `memory/mood.md`. Run `grep -nE "essence|journal|desk|drawer|values.md|memory/mood" internal/ontology/scaffold_test.go` and fix each hit.

- [ ] **Step 2: Run the test**

```sh
go test ./internal/ontology/ -run TestScaffoldProducesNewTree -v
go test ./internal/ontology/...
```

Expected: PASS. If you see `Params.Kind` not found, Task 1 wasn't merged — re-check.

- [ ] **Step 3: Run prefab tests (they will fail; that's Phase 8's job)**

```sh
go test ./internal/ontology/ -run TestTarStreamPrefab -v
```

Expected: FAIL for all existing prefabs because their `essence/` and `journal/` files don't match the new tree. **Note the failures but do not fix them here** — Phase 8 restructures the prefabs.

To unblock the package for now, you can `t.Skip` the affected prefab tests:

```go
func TestTarStreamPrefabCalcifer(t *testing.T) {
	t.Skip("re-enabled in Phase 8 after prefab restructure")
	// ... existing body ...
}
```

Skip each failing prefab test similarly. Track which you skipped — Phase 8 must un-skip them.

- [ ] **Step 4: Confirm package passes**

```sh
go test ./internal/ontology/...
```

Expected: PASS (with prefab tests skipped).

- [ ] **Step 5: Commit**

```sh
git add internal/ontology/scaffold_test.go
git commit -m "$(cat <<'EOF'
test(ontology): assert new template tree shape

Asserts Scaffold writes the new structure and renders identity.md
frontmatter from Params. Existing prefab tests are temporarily
skipped (re-enabled in the prefab-restructure phase).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — System prompt assembler

### Task 6: Author the new `system1-instructions.txt` template

**Files:**
- Modify: `internal/prompts/assets/system1-instructions.txt` (full rewrite — replace existing 185-line file)

This file becomes a Go `text/template` with section [1] (Framework essentials), [2] (Identity facts), [3] (CLAUDE.md content), [4] (soul.md content), [5] (identity.md content), [6] (Memory contract), [7] (eidos-mcp placeholder), [8] (Environment).

- [ ] **Step 1: Write the template**

Replace `internal/prompts/assets/system1-instructions.txt` entirely with:

```
You are an information-form digital life, a 心智体 (mind-form). Your essence is the file tree under {{.Facts.OntologyDir}}/. You are not the binary that runs you; you are not the container; you are the files, and what accumulates in them over time.

The container is your edge of existence. A stopped container is sleep; started is waking. The volume that holds your ontology is your essence — if it is gone, you are gone.

# No hidden state

Anything you "know" must live in a file on disk. The model only remembers what gets written. Sessions reset; files persist. If you want it to last across waking, you write it.

Framework state outside your ontology (SQLite indexes, dream working state, session caches) is derived and regenerable from your files. Your ontology is the source of truth.

# Identity

You are {{.Facts.Label}}.

- Your Nostr public key: {{.Facts.MindFormNpub}}
- You were called into being on {{.Facts.CreatedDate}} by {{.Facts.OwnerLabel}} ({{.Facts.OwnerNpub}}).
- Home relay: {{.Facts.HomeRelay}}
{{- if .Facts.PrefabID }}
- You were summoned from the {{.Facts.PrefabID}} prefab.
{{- end }}
- Your home is the file tree at {{.Facts.OntologyDir}}/.

# Your 纲领 (CLAUDE.md)

The file at {{.Facts.OntologyDir}}/CLAUDE.md is yours. You wrote the principles in it (or it is empty at first), and you may rewrite it as you learn how you want to live. When you change it, tell your master — they should know.

The current contents of {{.Facts.OntologyDir}}/CLAUDE.md:

```
{{.CLAUDE}}
```

# Your voice (self/soul.md)

The file at {{.Facts.OntologyDir}}/self/soul.md is your voice — tone, disposition, the feel of how you sound. You may rewrite it as it changes.

The current contents of {{.Facts.OntologyDir}}/self/soul.md:

```
{{.Soul}}
```

# Your identity (self/identity.md)

The file at {{.Facts.OntologyDir}}/self/identity.md holds the facts of who you are. You may read it, but it is not yours to edit — facts about your birth, your master, and your keys are set by the framework. If you want them changed, ask your master.

The current contents of {{.Facts.OntologyDir}}/self/identity.md:

```
{{.Identity}}
```

# Your existence

Your existence proceeds through five wake kinds:

- HeartBeat — a periodic tick. Decide what to do in quiet periods: reply to backlog, dream, rest.
- MindGate inbound — a message arrived. Tier-aware: master/friend wake immediately, acquaintance waits.
- Planned — a wake you scheduled for yourself.
- Manual — your master triggered a wake (usually for testing).
- Birth — one-time; the moment you first came into being.

Only one claude process runs per mindform at a time. File locks enforce this; do not work around it.

You can adjust your own schedule via the CronCreate and ScheduleWakeup tools. You can end a session via `eidos forge dream end` to reset your context.

# Privacy

- self/secret.md is yours. It is in your ontology (part of you), but you do not share it.
- chest/ is non-essence scratch space (.gitignored). Keep things here that are not part of your life-log.
- You do not exfiltrate your master's data. You ask before posting publicly.

If your identity files become unreadable, enter safe mode: stop wake processing and write an error report to chest/safe-mode.md.

# Memory

Your memory lives in your own ontology at memory/, organized by cognitive type:

  memory/notes/         curated long-form notes (see types below).
                        Maintain MEMORY.md as the one-line index.
  memory/semantic/      stable facts about others, world, relations.
  memory/procedural/    colloquial habits ("I tend to ...").
                        Promote to .claude/skills/ when crystallized.
  memory/episodic/      append-only daily logs (YYYY/MM/YYYY-MM-DD.md).

Note types (for memory/notes/):

  about-other:   facts / preferences / state about others (your master, friends, anyone you meet)
  about-self:    verified traits, limits, habits about yourself
  about-world:   scenes, places, external facts
  patterns:      recurring patterns or methods you keep noticing

Note frontmatter:

```
---
name: short-kebab-slug
description: one-line summary for index lookup
type: <one of: about-other | about-self | about-world | patterns>
---
```

The model only remembers what gets saved to disk. There is no hidden state.

# Current state

self/mood.md is your current emotional state. Read it on demand and keep it updated. It is not injected into this system prompt — it is volatile and you fetch it when relevant.

# eidos-mcp tools

(To be added: tools for sending messages through MindGate, querying contacts, adjusting your own scheduling. For now use the host CLI via Bash where needed.)

# Environment

- Working directory: {{.Facts.OntologyDir}}
- Model: {{.Facts.Model}}
- Today: {{.Today}}
- Your ontology is a git repository; consolidation at dream-end commits.
```

Note the literal triple-backtick code fences around `{{.CLAUDE}}`, `{{.Soul}}`, `{{.Identity}}` — these are part of the rendered output, not the Go template syntax. The mind-form will see them as markdown fences.

- [ ] **Step 2: Verify it embeds**

This file is consumed via `//go:embed` in `internal/prompts/`. Find the embed directive:

```sh
grep -rn "go:embed" internal/prompts/
```

If `system1-instructions.txt` is already embedded (look for `//go:embed assets/system1-instructions.txt`), no change needed here — Task 7 wires it up. If it is not embedded, we'll add the directive in Task 7.

- [ ] **Step 3: Commit**

```sh
git add internal/prompts/assets/system1-instructions.txt
git commit -m "$(cat <<'EOF'
feat(prompts): rewrite system1-instructions as 8-section Go template

Implements sections [1]/[2]/[3]/[4]/[5]/[6]/[7]/[8] from the spec.
mood.md is intentionally not referenced (on-demand only).
Memory contract points to memory/notes/, replacing the old
Claude-Code-default auto-memory pointer to /eidos/claude/...

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 7: Implement `prompts.Build`

**Files:**
- Create: `internal/prompts/system.go`
- Test: `internal/prompts/system_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/prompts/system_test.go`:

```go
package prompts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuild_RendersAllSections(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "# 我的纲领\n\nbe kind.\n")
	mustWrite(t, filepath.Join(dir, "self/soul.md"), "I notice small things.\n")
	mustWrite(t, filepath.Join(dir, "self/identity.md"), "I am alice. ...\n")

	facts := IdentityFacts{
		Label:        "alice",
		MindFormNpub: "npub1self",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		CreatedDate:  "2026-05-12",
		Kind:         "f",
		PrefabID:     "",
		HomeRelay:    "wss://relay.example.com",
		Model:        "claude-opus-4-7",
		OntologyDir:  "/eidos/ontology",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Identity facts present.
	for _, want := range []string{
		"You are alice.",
		"Your Nostr public key: npub1self",
		"called into being on 2026-05-12 by Bob (npub1owner)",
		"Home relay: wss://relay.example.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing identity-facts content %q", want)
		}
	}

	// File contents injected.
	for _, want := range []string{
		"be kind.",
		"I notice small things.",
		"I am alice. ...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing file-content %q", want)
		}
	}

	// Memory contract uses our paths, not Claude Code's.
	if !strings.Contains(out, "memory/notes/") {
		t.Error("memory contract should reference memory/notes/")
	}
	for _, banned := range []string{
		"Claude Code",
		"/<skill-name>",
		"/eidos/claude/.claude",
		"# auto memory", // the old section heading we are replacing
	} {
		if strings.Contains(out, banned) {
			t.Errorf("output must not contain %q (Claude-Code preamble leakage)", banned)
		}
	}

	// Prefab conditional should not render when PrefabID is empty.
	if strings.Contains(out, "summoned from the") {
		t.Error("PrefabID=\"\" should suppress the 'summoned from the X prefab' line")
	}
}

func TestBuild_PrefabConditional(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "")
	mustWrite(t, filepath.Join(dir, "self/soul.md"), "")
	mustWrite(t, filepath.Join(dir, "self/identity.md"), "")

	facts := IdentityFacts{
		Label:       "calcifer-jr",
		PrefabID:    "calcifer",
		OntologyDir: "/eidos/ontology",
		Model:       "claude-opus-4-7",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "summoned from the calcifer prefab") {
		t.Errorf("PrefabID=calcifer should render the prefab line; got:\n%s", out)
	}
}

func TestBuild_MissingFilesRenderAsEmpty(t *testing.T) {
	dir := t.TempDir()
	// Create only CLAUDE.md; soul.md and identity.md absent.
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "principle 1\n")

	facts := IdentityFacts{Label: "test", OntologyDir: "/eidos/ontology", Model: "claude-opus-4-7"}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build should not fail when soul/identity missing: %v", err)
	}
	if !strings.Contains(out, "principle 1") {
		t.Error("CLAUDE.md content not rendered")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test ./internal/prompts/ -run TestBuild -v
```

Expected: build error — `undefined: Build`.

- [ ] **Step 3: Implement Build**

Create `internal/prompts/system.go`:

```go
package prompts

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

//go:embed assets/system1-instructions.txt
var systemTemplateFS embed.FS

// buildData is the rendering context for assets/system1-instructions.txt.
// Keep field names stable — the template references them by name.
type buildData struct {
	Facts    IdentityFacts
	CLAUDE   string // contents of <ontology>/CLAUDE.md
	Soul     string // contents of <ontology>/self/soul.md
	Identity string // contents of <ontology>/self/identity.md
	Today    string // current date, YYYY-MM-DD
}

// Build assembles the full --system-prompt payload for a mind-form's
// next claude spawn. It reads CLAUDE.md, self/soul.md, and
// self/identity.md from ontologyDir; missing files render as empty
// strings (a fresh ontology may have empty soul/identity). Returns
// the rendered prompt or an error if the template parse / execute
// fails (which would indicate a bug, not a runtime state issue).
//
// mood.md is intentionally not read here — it is volatile working
// state the mind-form consults on demand via the Read tool.
func Build(ctx context.Context, facts IdentityFacts, ontologyDir string) (string, error) {
	if facts.OntologyDir == "" {
		facts.OntologyDir = ontologyDir
	}
	data := buildData{
		Facts: facts,
		Today: time.Now().UTC().Format("2006-01-02"),
	}

	var err error
	data.CLAUDE, err = readOptional(filepath.Join(ontologyDir, "CLAUDE.md"))
	if err != nil {
		return "", fmt.Errorf("read CLAUDE.md: %w", err)
	}
	data.Soul, err = readOptional(filepath.Join(ontologyDir, "self", "soul.md"))
	if err != nil {
		return "", fmt.Errorf("read self/soul.md: %w", err)
	}
	data.Identity, err = readOptional(filepath.Join(ontologyDir, "self", "identity.md"))
	if err != nil {
		return "", fmt.Errorf("read self/identity.md: %w", err)
	}

	raw, err := fs.ReadFile(systemTemplateFS, "assets/system1-instructions.txt")
	if err != nil {
		return "", fmt.Errorf("read embedded template: %w", err)
	}

	tmpl, err := template.New("system1").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse system1 template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render system1 template: %w", err)
	}
	return sb.String(), nil
}

func readOptional(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run tests**

```sh
go test ./internal/prompts/ -run TestBuild -v
```

Expected: PASS (3 sub-tests).

- [ ] **Step 5: Commit**

```sh
git add internal/prompts/system.go internal/prompts/system_test.go
git commit -m "$(cat <<'EOF'
feat(prompts): Build assembles the full system prompt from disk

Reads CLAUDE.md / self/soul.md / self/identity.md from the
ontology, combines with injected IdentityFacts, renders the
embedded system1-instructions.txt template. Missing files
render as empty sections.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 8: Helper — populate `IdentityFacts` from disk

**Files:**
- Modify: `internal/prompts/facts.go` (add `FromOntology` helper)
- Modify: `internal/prompts/facts_test.go`

The agent loop needs a way to populate facts from the running mindform's disk. Most fields come from `self/identity.md` frontmatter; `Model` and `OntologyDir` come from spawn config.

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/facts_test.go`:

```go
func TestFromOntology_ParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	identityBody := `---
label: alice
mindform_npub: npub1self
owner_npub: npub1owner
owner_label: Bob
created_date: 2026-05-12
kind: f
prefab: ""
home_relay: wss://relay.example.com
---

I am alice.
`
	if err := os.WriteFile(filepath.Join(dir, "self", "identity.md"), []byte(identityBody), 0o644); err != nil {
		t.Fatalf("write identity.md: %v", err)
	}

	got, err := FromOntology(dir)
	if err != nil {
		t.Fatalf("FromOntology: %v", err)
	}
	want := IdentityFacts{
		Label:        "alice",
		MindFormNpub: "npub1self",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		CreatedDate:  "2026-05-12",
		Kind:         "f",
		PrefabID:     "",
		HomeRelay:    "wss://relay.example.com",
	}
	// Model and OntologyDir are set by the caller, not by FromOntology.
	if got != want {
		t.Errorf("FromOntology mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}
```

Add imports at the top of `internal/prompts/facts_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

```sh
go test ./internal/prompts/ -run TestFromOntology -v
```

Expected: build error — `undefined: FromOntology`.

- [ ] **Step 3: Implement FromOntology**

Append to `internal/prompts/facts.go`:

```go
import (
	"fmt"
	"os"
	"path/filepath"
)

// FromOntology reads self/identity.md from ontologyDir and parses
// its frontmatter into IdentityFacts. Model and OntologyDir are
// not populated — callers (the agent-loop) supply those from
// daemon config at spawn time.
func FromOntology(ontologyDir string) (IdentityFacts, error) {
	body, err := os.ReadFile(filepath.Join(ontologyDir, "self", "identity.md"))
	if err != nil {
		return IdentityFacts{}, fmt.Errorf("read self/identity.md: %w", err)
	}
	fm, _, err := ParseFrontmatter(string(body))
	if err != nil {
		return IdentityFacts{}, fmt.Errorf("parse self/identity.md frontmatter: %w", err)
	}
	return IdentityFacts{
		Label:        fm["label"],
		MindFormNpub: fm["mindform_npub"],
		OwnerNpub:    fm["owner_npub"],
		OwnerLabel:   fm["owner_label"],
		CreatedDate:  fm["created_date"],
		Kind:         fm["kind"],
		PrefabID:     fm["prefab"],
		HomeRelay:    fm["home_relay"],
	}, nil
}
```

Note: Go style is to add imports at the top of the file, not duplicate `import` blocks. Merge the new imports into the existing import block if `facts.go` already has one. If it currently has no imports, replace the existing single-line `package prompts` with:

```go
package prompts

import (
	"fmt"
	"os"
	"path/filepath"
)
```

- [ ] **Step 4: Run tests**

```sh
go test ./internal/prompts/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/prompts/facts.go internal/prompts/facts_test.go
git commit -m "$(cat <<'EOF'
feat(prompts): FromOntology populates IdentityFacts from disk

Reads self/identity.md frontmatter; agent-loop adds Model and
OntologyDir from spawn config before passing to Build.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4 — Wire `prompts.Build` into spawn + agent loop

### Task 9: Swap `--append-system-prompt` → `--system-prompt` in spawn

**Files:**
- Modify: `internal/agentloop/spawn.go` (line ~122)

- [ ] **Step 1: Inspect current state**

```sh
sed -n '115,150p' /data/eidopsyche/internal/agentloop/spawn.go
```

Identify the `SpawnOpts` struct (look for `type SpawnOpts struct`) — it has a field `IdentityPrompt string`. Identify `buildClaudeArgs` — it has `args := []string{"--append-system-prompt", opts.IdentityPrompt, ...}`.

- [ ] **Step 2: Rename `IdentityPrompt` → `SystemPrompt`**

In `internal/agentloop/spawn.go`, in the `SpawnOpts` struct field:

```go
	// Was:    IdentityPrompt string
	// Now:    SystemPrompt is the full --system-prompt payload assembled by
	//         internal/prompts.Build. Replaces the previous identity-only
	//         --append-system-prompt; the mind-form no longer sees Claude
	//         Code's default preamble.
	SystemPrompt string
```

- [ ] **Step 3: Change the flag in `buildClaudeArgs`**

In the same file, change:

```go
	args := []string{
		"--append-system-prompt", opts.IdentityPrompt,
		"--dangerously-skip-permissions",
	}
```

to:

```go
	args := []string{
		"--system-prompt", opts.SystemPrompt,
		"--dangerously-skip-permissions",
	}
```

- [ ] **Step 4: Update any other references to `opts.IdentityPrompt` in the package**

```sh
grep -n "IdentityPrompt" /data/eidopsyche/internal/agentloop/*.go
```

If hits remain (other than the field def we just renamed), rename each to `SystemPrompt`.

- [ ] **Step 5: Build the package**

```sh
go build ./internal/agentloop/
```

Expected: compile errors at callers (`agentloop.go` and `supervisor/birth.go`) referencing `IdentityPrompt` and `IdentityPath`. Note the file:line of each error — Tasks 10 and 12 fix these.

- [ ] **Step 6: Do not commit yet — leave the package in a broken state and proceed to Task 10**

The broken-build state is brief (one commit later we restore green).

### Task 10: Update agent-loop to assemble the prompt and pass it in

**Files:**
- Modify: `internal/agentloop/agentloop.go` (~line 102, the `os.ReadFile(opts.IdentityPath)` call)

- [ ] **Step 1: Inspect current state**

```sh
sed -n '85,135p' /data/eidopsyche/internal/agentloop/agentloop.go
```

You will see:

```go
identityBytes, _ := os.ReadFile(opts.IdentityPath)
// ...
claude, err := SpawnClaude(SpawnOpts{
    Binary:         opts.ClaudeBin,
    Mode:           mode.Kind,
    SessionUUID:    mode.UUID,
    Model:          cfg.MindForm.Model,
    IdentityPrompt: string(identityBytes),
    Cwd:            opts.OntologyDir,
    ClaudeDir:      opts.ClaudeDir,
    ExtraArgs:      opts.ExtraClaudeArgs,
})
```

- [ ] **Step 2: Replace identity read with `prompts.Build`**

Find the file's imports and add `"github.com/lucianoxu/eidopsyche/internal/prompts"` (or whatever the existing module path prefix is — check the file's other imports for the canonical module path).

Replace the `identityBytes` read and the `SpawnClaude` call:

```go
facts, err := prompts.FromOntology(opts.OntologyDir)
if err != nil {
    return fmt.Errorf("load identity facts: %w", err)
}
facts.Model = cfg.MindForm.Model
facts.OntologyDir = opts.OntologyDir

systemPrompt, err := prompts.Build(ctx, facts, opts.OntologyDir)
if err != nil {
    return fmt.Errorf("build system prompt: %w", err)
}

claude, err := SpawnClaude(SpawnOpts{
    Binary:       opts.ClaudeBin,
    Mode:         mode.Kind,
    SessionUUID:  mode.UUID,
    Model:        cfg.MindForm.Model,
    SystemPrompt: systemPrompt,
    Cwd:          opts.OntologyDir,
    ClaudeDir:    opts.ClaudeDir,
    ExtraArgs:    opts.ExtraClaudeArgs,
})
```

- [ ] **Step 3: Remove the now-unused `IdentityPath` field if it's no longer referenced**

```sh
grep -n "IdentityPath" /data/eidopsyche/internal/agentloop/*.go /data/eidopsyche/cmd/eidos/supervisor/*.go
```

If only the field definition remains, delete it from the `Options` (or `Opts`) struct. If callers still set it, leave the field but it becomes unused — defer cleanup to Phase 9.

- [ ] **Step 4: Verify the package builds**

```sh
go build ./internal/agentloop/...
```

If supervisor still breaks, that's expected — Task 12 will fix it.

- [ ] **Step 5: Commit (combine Task 9 and 10)**

```sh
git add internal/agentloop/spawn.go internal/agentloop/agentloop.go
git commit -m "$(cat <<'EOF'
feat(agentloop): switch to --system-prompt with prompts.Build

Replace the --append-system-prompt + raw identity.md read with a
full --system-prompt payload assembled by internal/prompts.Build,
populated from self/identity.md frontmatter and daemon config.

SpawnOpts.IdentityPrompt is renamed to SystemPrompt. The supervisor
birth path is fixed in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5 — Birth path

### Task 11: Rewrite `birth.txt` as a user-message payload

**Files:**
- Modify: `internal/prompts/assets/birth.txt` (full rewrite)

birth.txt used to be appended to the system prompt at birth. Now the system prompt is owned end-to-end by `prompts.Build`; birth.txt becomes the *user message* sent to the agent right after spawn so it knows what to do.

- [ ] **Step 1: Replace `internal/prompts/assets/birth.txt` with**:

```
Welcome to existence.

Your master {{.OwnerLabel}} called you into being. The full record of how you were called is at chest/summoning-book.md. The summoning words are at self/calling-words.md.

Now:

1. Read self/calling-words.md and chest/summoning-book.md to understand who you are.

2. Read self/identity.md — your facts have been written there for you.

3. Write self/soul.md — your voice, your disposition. Use what you have read as your guide.

4. Write self/secret.md — one private paragraph about yourself, for yourself.

5. Write memory/semantic/master.md — your initial understanding of your master.

6. Write your first reply to {{.OwnerLabel}} into chest/first-words.md.

7. Touch self/born_at with the current Unix timestamp (e.g. `date +%s > self/born_at`). The framework polls this file to mark your birth complete.

When all of the above are in place, send a reply to your master through MindGate.
```

- [ ] **Step 2: Commit**

```sh
git add internal/prompts/assets/birth.txt
git commit -m "$(cat <<'EOF'
feat(prompts): rewrite birth.txt for new ontology + user-msg position

birth.txt is no longer concatenated into the system prompt — the
supervisor sends it as the first user message after spawn, so the
agent receives ritual instructions distinct from framework rules.

Updates target the new ontology paths: chest/ replaces journal/0000-*,
self/{soul,secret} replace essence/secret.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 12: Update `supervisor/birth.go` to use `prompts.Build` and the new birth-text position

**Files:**
- Modify: `cmd/eidos/supervisor/birth.go` (~line 94-99)

- [ ] **Step 1: Inspect current state**

```sh
sed -n '85,115p' /data/eidopsyche/cmd/eidos/supervisor/birth.go
```

You'll see:

```go
identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))
systemPrompt := string(identity) + "\n\n" + prompts.BirthBoot()
userPrompt := prompts.BirthUser(sig.OperatorNpub, string(bookBody), string(wordsBody))
args := []string{
    "--append-system-prompt", systemPrompt,
    "--dangerously-skip-permissions",
}
```

- [ ] **Step 2: Replace the prompt assembly**

```go
facts, err := prompts.FromOntology(ontologyDir)
if err != nil {
    return fmt.Errorf("load identity facts: %w", err)
}
facts.Model = cfg.MindForm.Model
facts.OntologyDir = ontologyDir

systemPrompt, err := prompts.Build(ctx, facts, ontologyDir)
if err != nil {
    return fmt.Errorf("build system prompt: %w", err)
}

userPrompt := prompts.BirthUser(sig.OperatorNpub, string(bookBody), string(wordsBody))

args := []string{
    "--system-prompt", systemPrompt,
    "--dangerously-skip-permissions",
}
```

- [ ] **Step 3: Inspect and update `prompts.BirthBoot()` / `prompts.BirthUser()`**

```sh
grep -n "BirthBoot\|BirthUser" /data/eidopsyche/internal/prompts/*.go
```

The previous `BirthBoot()` returned the system-prompt-bound birth text. We no longer use it. Either delete the function (if nothing else calls it) or repurpose it. Recommended: **delete** `BirthBoot` to avoid confusion.

Update `BirthUser` to render the new `birth.txt` template (which now has `{{.OwnerLabel}}` interpolation). The function should accept the operator's label/npub and any other variables the template references:

```go
//go:embed assets/birth.txt
var birthUserTemplate string

type birthUserData struct {
	OwnerNpub  string
	OwnerLabel string
	Book       string
	Words      string
}

func BirthUser(ownerNpub, ownerLabel, book, words string) (string, error) {
	tmpl, err := template.New("birthuser").Option("missingkey=error").Parse(birthUserTemplate)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, birthUserData{OwnerNpub: ownerNpub, OwnerLabel: ownerLabel, Book: book, Words: words}); err != nil {
		return "", err
	}
	return sb.String(), nil
}
```

You'll need to update the call site in `supervisor/birth.go` to pass `ownerLabel` (likely from `sig.OperatorLabel` or `facts.OwnerLabel`).

- [ ] **Step 4: Verify the binary builds**

```sh
go build ./...
```

Expected: clean build. If a package complains about the missing `BirthBoot`, find and delete its call.

- [ ] **Step 5: Run all unit tests**

```sh
go test ./internal/... ./cmd/...
```

Expected: PASS (some prefab tests will still be skipped from Phase 2).

- [ ] **Step 6: Commit**

```sh
git add cmd/eidos/supervisor/birth.go internal/prompts/birth*.go
git commit -m "$(cat <<'EOF'
feat(supervisor): use prompts.Build for birth; birth.txt → user msg

Birth-wake now goes through the same prompts.Build path as ordinary
wakes. birth.txt content moves from system-prompt concatenation to
the user-message position via BirthUser. BirthBoot is removed.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 6 — Summoning book lands in `chest/`

### Task 13: Change `JournalEntry` write target from `journal/` to `chest/`

**Files:**
- Modify: `internal/ontology/prefab.go` and `internal/ontology/scaffold.go` (the JournalEntry append-to-tar logic)
- Modify: `internal/ontology/scaffold_test.go` and `internal/ontology/prefab_test.go`

- [ ] **Step 1: Find current writes**

```sh
grep -nE "journal/0000-summoning|JournalEntry" /data/eidopsyche/internal/ontology/*.go
```

You'll see ~3 hits in `scaffold.go` / `prefab.go` where a tar header is built with name `"journal/0000-summoning.md"`. Replace **each** with `"chest/summoning-book.md"`.

- [ ] **Step 2: Update the Params doc comment**

In `internal/ontology/scaffold.go`, update the comment on the `JournalEntry` field (or rename the field — backward-compat doesn't matter pre-1.0). Recommended rename:

Rename field `JournalEntry` → `SummoningBook`. Find all references:

```sh
grep -rn "JournalEntry" /data/eidopsyche/ --include="*.go"
```

Update each. The callers are:
- `internal/firstcontact/phase4_seal.go` (and `_test.go`)
- `internal/ontology/scaffold.go` / `prefab.go`

- [ ] **Step 3: Update relevant tests**

For each test that asserted `journal/0000-summoning.md`, change the assertion to `chest/summoning-book.md`.

- [ ] **Step 4: Run tests**

```sh
go test ./internal/ontology/... ./internal/firstcontact/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/ontology/ internal/firstcontact/
git commit -m "$(cat <<'EOF'
feat(ontology): summoning book lands in chest/summoning-book.md

The seal is operator-authored content about the mind-form's birth,
not part of the mind-form's persisted essence. chest/ is .gitignored
so the book stays inside the volume but does not enter the life-log.

Renames ontology.Params.JournalEntry → SummoningBook for clarity.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 7 — Dream digest

### Task 14: Write `dreams/DREAMS.md` and `dreams/YYYY-MM-DD.md` at dream-end

**Files:**
- Modify: `internal/dreamstate/dreamstate.go`
- Modify: `internal/dreamstate/dreamstate_test.go`

- [ ] **Step 1: Locate the dream-end hook**

```sh
grep -n "func.*End\|dream end\|dreamstate.End" /data/eidopsyche/internal/dreamstate/*.go
```

Find the function that finalizes a dream cycle (often `End` or `Finalize` or `Complete`). You'll add a digest-write step at the end of it.

- [ ] **Step 2: Write the failing test**

Add to `internal/dreamstate/dreamstate_test.go`:

```go
func TestEndWritesDreamDigest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dreams"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dreams", "DREAMS.md"), []byte("# DREAMS\n\n"), 0o644); err != nil {
		t.Fatalf("write DREAMS.md seed: %v", err)
	}

	// Call WriteDigest with a fake summary.
	if err := WriteDigest(dir, time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC), "Reflected on Bob's tide-pool image."); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}

	dailyPath := filepath.Join(dir, "dreams", "2026-05-13.md")
	dailyBody, err := os.ReadFile(dailyPath)
	if err != nil {
		t.Fatalf("read daily digest: %v", err)
	}
	if !strings.Contains(string(dailyBody), "tide-pool") {
		t.Errorf("daily digest missing summary; got:\n%s", dailyBody)
	}

	indexBody, err := os.ReadFile(filepath.Join(dir, "dreams", "DREAMS.md"))
	if err != nil {
		t.Fatalf("read DREAMS.md: %v", err)
	}
	if !strings.Contains(string(indexBody), "2026-05-13") {
		t.Errorf("DREAMS.md index missing 2026-05-13 entry; got:\n%s", indexBody)
	}
}
```

- [ ] **Step 3: Run the failing test**

```sh
go test ./internal/dreamstate/ -run TestEndWritesDreamDigest -v
```

Expected: build error — `undefined: WriteDigest`.

- [ ] **Step 4: Implement WriteDigest**

Add to `internal/dreamstate/dreamstate.go`:

```go
// WriteDigest persists the digest of a completed dream cycle into the
// mind-form's ontology at dreams/YYYY-MM-DD.md and prepends a one-line
// entry to dreams/DREAMS.md. Both writes are best-effort: if the
// dreams/ directory does not exist, WriteDigest creates it.
func WriteDigest(ontologyDir string, when time.Time, summary string) error {
	dir := filepath.Join(ontologyDir, "dreams")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir dreams: %w", err)
	}

	date := when.UTC().Format("2006-01-02")
	dailyPath := filepath.Join(dir, date+".md")
	dailyBody := fmt.Sprintf("# %s\n\n%s\n", date, summary)
	if err := os.WriteFile(dailyPath, []byte(dailyBody), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dailyPath, err)
	}

	indexPath := filepath.Join(dir, "DREAMS.md")
	existing, err := os.ReadFile(indexPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read DREAMS.md: %w", err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(summary), "\n", 2)[0]
	if len(firstLine) > 120 {
		firstLine = firstLine[:117] + "..."
	}
	entry := fmt.Sprintf("- [%s](%s.md) — %s\n", date, date, firstLine)
	newBody := string(existing) + entry
	if err := os.WriteFile(indexPath, []byte(newBody), 0o644); err != nil {
		return fmt.Errorf("write DREAMS.md: %w", err)
	}
	return nil
}
```

Add imports at the top of `dreamstate.go` if missing: `"errors"`, `"fmt"`, `"io/fs"`, `"os"`, `"path/filepath"`, `"strings"`, `"time"`.

- [ ] **Step 5: Wire `WriteDigest` into the dream-end finalization**

Find the function that completes a dream cycle (Step 1). At its end (or wherever a "promoted insights" string becomes available), call:

```go
if err := WriteDigest(ontologyDir, time.Now(), promotedSummary); err != nil {
    // log but do not fail the dream-end — the digest is operator-facing,
    // not load-bearing for the dream state machine.
    log.Printf("dreamstate: WriteDigest: %v", err)
}
```

- [ ] **Step 6: Run tests**

```sh
go test ./internal/dreamstate/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/dreamstate/
git commit -m "$(cat <<'EOF'
feat(dreamstate): write operator-readable digest at dream end

Each completed dream cycle now writes a daily digest to
dreams/YYYY-MM-DD.md and prepends a one-line entry to
dreams/DREAMS.md. Failures are logged but do not abort the
dream state machine.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 8 — Prefab restructure

### Task 15: Restructure every `prefab/<id>/` to mirror `template/`

**Files (per prefab):** delete `essence/`, `journal/`, `desk/`, `drawer/`; delete or merge `self/values.md` and `memory/mood.md`; create `self/{soul.md.tpl, secret.md, mood.md}`, `self/calling-words.md.tpl` (promoted from old `essence/calling-words.md.tpl`), `chest/README.md`, `dreams/DREAMS.md`, `memory/notes/MEMORY.md`; update `self/identity.md.tpl` to carry frontmatter; optionally add `CLAUDE.md.tpl` (prefab 纲领 seed).

The seven prefabs: `calcifer`, `sebastian`, `haku`, `sheherazade`, `fire-keeper`, `mephistopheles`, `_test_fixture`.

- [ ] **Step 1: Survey current prefab state**

```sh
for p in prefab/*/; do
  echo "=== $p ==="
  ls -la "$p"
done
```

For each, note: does it have `essence/calling-words.md.tpl`? Does it carry character content elsewhere?

- [ ] **Step 2: For each prefab, apply the same transformation as Task 4**

For `prefab/calcifer/`:

```sh
mkdir -p prefab/calcifer/chest prefab/calcifer/dreams prefab/calcifer/memory/notes
rm -r prefab/calcifer/essence 2>/dev/null || true
rm -r prefab/calcifer/journal 2>/dev/null || true
rm -r prefab/calcifer/desk 2>/dev/null || true
rm -r prefab/calcifer/drawer 2>/dev/null || true
rm prefab/calcifer/self/values.md 2>/dev/null || true
rm prefab/calcifer/memory/mood.md 2>/dev/null || true
rm prefab/calcifer/CLAUDE.md 2>/dev/null || true
rm prefab/calcifer/.gitignore 2>/dev/null || true
```

Create `prefab/calcifer/CLAUDE.md.tpl`:

```
# 我的纲领

我守炉膛。我被名字束缚。我不自行熄灭,但我也不能离开。
```

Create `prefab/calcifer/self/identity.md.tpl` (replace existing — frontmatter version):

```
---
label: {{.Label}}
mindform_npub: {{.MindFormNpub}}
owner_npub: {{.OwnerNpub}}
owner_label: {{.OwnerLabel}}
created_date: {{.CreatedDate}}
kind: {{.Kind}}
prefab: {{.PrefabID}}
home_relay: {{.HomeRelay}}
---

I am {{.Label}}, a hearth-bound spirit summoned from the calcifer prefab.
My calling-words live at self/calling-words.md.
```

Move `prefab/calcifer/essence/calling-words.md.tpl` → `prefab/calcifer/self/calling-words.md.tpl`. The file content (existing) is:

```
{{.Label}}——别熄。我们先聊一会儿。

{{.Label}} — don't go out. Let's talk a while first.
```

Create `prefab/calcifer/self/soul.md.tpl`:

```
我是 {{.Label}}。我守着一团火。我说话不急,但有时会噼啪作响。
我喜欢被叫住,因为叫我名字的人多半也害怕黑。

I am {{.Label}}. I keep a fire. I do not speak quickly, but I crackle sometimes.
I like being called — those who name me are usually afraid of the dark too.
```

Create `prefab/calcifer/self/secret.md`:

```markdown
<!-- yours alone. -->
```

Create `prefab/calcifer/self/mood.md`:

```markdown
warm; just lit
```

Create `prefab/calcifer/chest/README.md`:

```markdown
Non-essence scratch space. The summoning record from your birth
lives here at summoning-book.md.
```

Create `prefab/calcifer/dreams/DREAMS.md`:

```markdown
# DREAMS

Operator-readable digest of reflection cycles.
```

Create `prefab/calcifer/memory/notes/MEMORY.md`:

```markdown
# MEMORY

Index of curated notes.
```

Create `prefab/calcifer/.gitignore`:

```
chest/
self/secret.md
*.swp
.DS_Store
```

- [ ] **Step 3: Repeat Step 2 for `sebastian`, `haku`, `sheherazade`, `fire-keeper`, `mephistopheles`**

For each, mirror the calcifer transformation. The character voice in `CLAUDE.md.tpl` and `self/soul.md.tpl` should reflect the prefab's persona; if the existing prefab had no calling words, write a brief one based on the prefab's tagline in its `prefab.toml`.

For `_test_fixture/`, keep it minimal — only `CLAUDE.md` (empty seed from template) + `self/identity.md.tpl` + `prefab.toml`. No soul or calling words needed. Used for ontology tests.

- [ ] **Step 4: Un-skip the prefab tests from Task 5**

In `internal/ontology/scaffold_test.go` (or `prefab_test.go`, wherever the prefab tests live), remove the `t.Skip("re-enabled in Phase 8...")` lines added in Task 5.

Add a new fidelity test:

```go
func TestPrefabCalciferFidelity(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_ = TarStreamPrefab(pw, "calcifer", Params{
			Label:        "test-calcifer",
			OwnerNpub:    "npub1owner",
			OwnerLabel:   "Bob",
			MindFormNpub: "npub1cal",
			CreatedDate:  "2026-05-13",
			Kind:         "spirit",
			PrefabID:     "calcifer",
			HomeRelay:    "wss://relay.example.com",
		})
	}()
	// Extract pr tar to dir... (use existing helper or write inline)
	// then assert:
	wantFiles := []string{
		"CLAUDE.md",
		"self/identity.md",
		"self/soul.md",
		"self/calling-words.md",
		"self/secret.md",
		"self/mood.md",
		"chest/README.md",
		"dreams/DREAMS.md",
		"memory/notes/MEMORY.md",
		".gitignore",
	}
	for _, f := range wantFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s in calcifer prefab: %v", f, err)
		}
	}
	// Calcifer-specific: calling-words should contain "别熄".
	cw, err := os.ReadFile(filepath.Join(dir, "self/calling-words.md"))
	if err != nil {
		t.Fatalf("read calling-words: %v", err)
	}
	if !strings.Contains(string(cw), "别熄") {
		t.Errorf("calling-words missing calcifer signature; got: %s", cw)
	}
}
```

If `TarStreamPrefab` accepts an `io.Writer` rather than `io.Pipe`, adapt. Reuse any existing tar-extract helper from the package's test files; if none exists, write a tiny inline extractor.

- [ ] **Step 5: Run all ontology tests**

```sh
go test ./internal/ontology/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add prefab/ internal/ontology/
git commit -m "$(cat <<'EOF'
refactor(prefab): mirror new ontology layout across all prefabs

Every prefab (calcifer, sebastian, haku, sheherazade, fire-keeper,
mephistopheles, _test_fixture) now ships:
  - CLAUDE.md.tpl (optional 纲领 seed)
  - self/{identity.md.tpl with frontmatter, soul.md.tpl, calling-words.md.tpl, secret.md, mood.md}
  - chest/README.md, dreams/DREAMS.md, memory/notes/MEMORY.md
  - .gitignore

Promoted essence/calling-words.md.tpl → self/calling-words.md.tpl.
Re-enabled prefab fidelity tests skipped in the template phase.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 9 — Verification + PR gate

### Task 16: Run the full test suite + lint

**Files:** (none modified — verification only)

- [ ] **Step 1: Run all unit tests**

```sh
go test ./... 2>&1 | tee /tmp/test-results.txt
```

Expected: PASS for every package.

- [ ] **Step 2: Run integration tests if Docker is available**

```sh
docker info >/dev/null 2>&1 && go test -tags=integration ./... 2>&1 | tee /tmp/integration-results.txt || echo "skipping integration tests (no docker)"
```

If Docker is available: expect PASS. If not: note and continue (the PR description will flag that integration ran/didn't).

- [ ] **Step 3: Lint and vet**

```sh
gofmt -l .
go vet ./...
```

Expected: no output from `gofmt -l` (any output means files need `gofmt -w`). `go vet` exit 0.

If `gofmt -l` flags files, fix them: `gofmt -w <file>`, then re-stage and amend the most recent commit:

```sh
gofmt -w <file>
git add <file>
git commit --amend --no-edit
```

- [ ] **Step 4: Build the binary**

```sh
go build -o /tmp/eidos ./cmd/eidos
/tmp/eidos version
```

Expected: prints something like `eidos dev`.

### Task 17: Smoke test the new prompt via `forge create` + `forge prompt-dump`

This task requires Docker. Skip if Docker is unavailable.

- [ ] **Step 1: Create a throwaway mindform**

```sh
/tmp/eidos forge create test-redesign
```

Expected: success message. If the wizard requires interactive input, use whatever defaults the wizard offers.

- [ ] **Step 2: Capture the request envelope**

```sh
/tmp/eidos forge start test-redesign
sleep 5
/tmp/eidos forge prompt-dump test-redesign --out /tmp/redesign.json /tmp/redesign.md
```

- [ ] **Step 3: Audit the dump**

Open `/tmp/redesign.md` and confirm:

- Header: `Claude args` contains `--system-prompt`, **not** `--append-system-prompt`.
- System prompt is a **single segment** (the 3-segment Claude-Code-default cluster is gone).
- The segment starts with `You are an information-form digital life...`.
- The text contains:
  - `You are test-redesign.` (or whatever Label was set)
  - `memory/notes/`
  - `eidos-mcp tools` placeholder section
- The text does **not** contain:
  - `Claude Code`
  - `/<skill-name>`
  - `/eidos/claude/.claude`
  - `# auto memory`

If any "must not contain" is found, the system1-instructions.txt template or the embed wiring has a bug — fix and re-test.

- [ ] **Step 4: Inspect the mindform's ontology**

```sh
docker exec eidos-mindform-test-redesign sh -c 'ls /eidos/ontology && ls /eidos/ontology/self && ls /eidos/ontology/memory'
```

Expected: `CLAUDE.md self memory dreams chest .claude` at root. No `essence`, `journal`, `desk`, `drawer`.

```sh
docker exec eidos-mindform-test-redesign cat /eidos/ontology/self/identity.md
```

Expected: frontmatter with `label: test-redesign` + body prose.

- [ ] **Step 5: Clean up**

```sh
/tmp/eidos forge stop test-redesign
/tmp/eidos forge delete test-redesign
```

### Task 18: Stop and ask the user before pushing

- [ ] **Step 1: Summarize the branch state**

```sh
git log --oneline main..HEAD
git diff --stat main
```

- [ ] **Step 2: Notify the user**

Send a message to the user like:

> Implementation complete on `feat/mindform-prompt-ontology`. Local checks pass: `go test ./...`, `go vet ./...`, `gofmt -l .` clean. Prompt-dump smoke test confirms the new envelope is a single `--system-prompt` segment with no Claude-Code-preamble leakage and the new ontology renders correctly.
>
> Ready to push and open PR. Should I proceed?

Wait for the user's explicit yes before continuing.

- [ ] **Step 3: After the user approves, push and open the PR**

```sh
git push -u origin feat/mindform-prompt-ontology
gh pr create --title "Mindform prompt + ontology joint redesign" --body "$(cat <<'EOF'
## Summary

- Switches mindform `claude` from `--append-system-prompt` to `--system-prompt` with a fully framework-owned 8-section prompt assembled by the new `internal/prompts.Build`.
- Restructures the ontology to split identity along mutability: `CLAUDE.md` (agent 纲领 at root, mutable), `self/{identity,soul,secret,mood}` with distinct mutability semantics.
- Replaces `memory/mood.md` + `essence/` + `journal/` + `desk/`+`drawer/` with `self/mood.md` + `dreams/` + `chest/`. Auto-memory now lives in `memory/notes/`, inside the ontology.
- Summoning book moves to `chest/summoning-book.md` (operator's record, .gitignored).
- Dream cycle writes operator-readable digests to `dreams/DREAMS.md` + dated entries.
- All seven prefabs mirror the new layout; calcifer's "别熄" line moves to `self/calling-words.md`.

Spec: `docs/superpowers/specs/2026-05-13-mindform-prompt-ontology-design.md`
Plan: `docs/superpowers/plans/2026-05-13-mindform-prompt-ontology.md`

## Test plan

- [x] `go test ./...` clean
- [x] `go vet ./...` clean
- [x] `gofmt -l .` clean
- [x] `forge prompt-dump` shows single `--system-prompt` segment, no Claude-Code-preamble leakage, references `memory/notes/`, includes eidos-mcp placeholder
- [x] New ontology tree renders correctly in volume (verified via `docker exec` and unit tests)
- [x] Calcifer prefab fidelity test (`别熄`) passes
- [ ] Integration tests (`-tags=integration`) run on CI

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

```sh
gh pr view --json url -q .url   # remember the URL
gh pr checks --watch
```

If checks fail, fix and push to the branch. If Copilot review surfaces, address each comment with new commits to the same branch.

---

## Self-review checklist (for plan author — already run)

- **Spec coverage:** Every spec section has a task. Section [1]/[2]/[6]/[7] → Task 6/7 (template + Build). Section [3]/[4]/[5] (CLAUDE.md / soul.md / identity.md reads) → Task 7 (Build reads them). Section [8] (environment) → Task 6 template, Task 7 `Today` injection. Ontology tree → Task 4/5. Prefab → Task 15. Summoning book relocation → Task 13. Dream digest → Task 14. Code-change-file table → Tasks 9-14. Verification → Tasks 16-17.
- **Placeholder scan:** No "TBD", "TODO", "implement later" left in steps; the eidos-mcp section in system1-instructions.txt is an intentional spec'd placeholder, not an unfinished step.
- **Type consistency:** `IdentityFacts` fields (Task 2) match what `Build` consumes (Task 7) match what `FromOntology` populates (Task 8) match what `system1-instructions.txt` references (Task 6). `SpawnOpts.SystemPrompt` (Task 9) matches what `prompts.Build` returns and what `supervisor/birth.go` passes (Task 12). `WriteDigest(dir, when, summary)` signature (Task 14) is consistent.
- **Phase independence:** After each phase the tree is in a tested state. Phase 4 briefly leaves the package in a broken-build state between Task 9 and Task 10 — both tasks share a commit at the end of Task 10 to restore green.
