# Prefab Mind-form Templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a prefab path to the summon wizard, six embedded prefab mind-forms, and a top-level `template/` + `prefab/` repository layout — without disturbing the existing claude-driven "from scratch" flow.

**Architecture:** Files move out of `internal/ontology/template/` to a top-level `template/`. A new top-level `prefab/` sibling holds six self-contained prefab ontology trees plus a `prefab.toml` per prefab. A new top-level Go file `embed.go` owns the `//go:embed` directives. `internal/ontology` gains `prefab.go` exposing `List`, `MetaFor`, and `TarStreamPrefab`. The wizard inserts a Phase 2.5 (scaffold source) and a parallel Phase 3 prefab branch; Phase 4 becomes prefab-aware and skips claude calls when `PrefabID` is set.

**Tech Stack:** Go 1.25, `embed`, `text/template` (existing), TOML via `github.com/pelletier/go-toml/v2` (already present — verify in Task 0), `archive/tar` (existing).

**Working branch:** `feat/prefab-mindforms` in a `.claude/` worktree.

---

## Task 0: Worktree + branch

**Files:**
- Worktree: `.claude/worktrees/feat-prefab-mindforms/`
- Branch: `feat/prefab-mindforms`

- [ ] **Step 1:** Verify clean working tree on `main`.

```bash
cd /data/eidopsyche
git status
git pull --ff-only
```

Expected: clean tree, branch up to date.

- [ ] **Step 2:** Create the worktree.

```bash
cd /data/eidopsyche
git worktree add -b feat/prefab-mindforms .claude/worktrees/feat-prefab-mindforms main
cd .claude/worktrees/feat-prefab-mindforms
```

Expected: new worktree directory, branch `feat/prefab-mindforms` checked out.

- [ ] **Step 3:** Verify `go-toml/v2` is already a transitive dep (it is — used by `internal/card`).

```bash
go list -m github.com/pelletier/go-toml/v2
```

Expected: prints version, no error. If absent, run `go get github.com/pelletier/go-toml/v2` and commit `go.mod`/`go.sum` as a separate prep commit.

- [ ] **Step 4:** Snapshot existing test status as baseline.

```bash
gofmt -l .
go vet ./...
go test ./...
```

Expected: clean lint, all unit tests pass. Record baseline; we should not regress this.

---

## Task 1: Move `internal/ontology/template/` → top-level `template/`

**Files:**
- Move: 13 files under `internal/ontology/template/` → `template/`
- Modify: `internal/ontology/embed.go` (new embed source)
- Create: `embed.go` (module root, `package eidopsyche`)

- [ ] **Step 1:** `git mv` the template tree.

```bash
mkdir -p template
git mv internal/ontology/template/CLAUDE.md template/
git mv internal/ontology/template/.gitignore template/
git mv internal/ontology/template/.claude template/
git mv internal/ontology/template/desk template/
git mv internal/ontology/template/drawer template/
git mv internal/ontology/template/essence template/
git mv internal/ontology/template/journal template/
git mv internal/ontology/template/memory template/
git mv internal/ontology/template/self template/
rmdir internal/ontology/template
```

Expected: `template/` populated, `internal/ontology/template/` gone.

- [ ] **Step 2:** Write the root-level embed file.

Create `embed.go`:

```go
// Package eidopsyche owns the module-root embed.FS bundling the canonical
// ontology template and the prefab catalogue. Internal packages
// (notably internal/ontology) re-export these for caller convenience.
//
// The file lives at the module root because Go's //go:embed directive
// cannot reach upward (no `..` segments allowed), and we want template/
// and prefab/ at the top of the repo so authors find them without
// hunting through internal/.
package eidopsyche

import "embed"

//go:embed all:template
var templateFS embed.FS

//go:embed all:prefab
var prefabFS embed.FS

// TemplateFS returns the embedded canonical ontology template FS.
// The root inside the FS is "template/".
func TemplateFS() embed.FS { return templateFS }

// PrefabFS returns the embedded prefab catalogue FS. The root inside
// the FS is "prefab/", with one subdirectory per prefab id.
func PrefabFS() embed.FS { return prefabFS }
```

- [ ] **Step 3:** Replace `internal/ontology/embed.go` to re-export.

```go
// Package ontology owns the on-disk shape of a mind-form's essence and
// the scaffolding that produces it.
package ontology

import (
	"embed"

	root "github.com/LucianoXu/eidopsyche"
)

// templateFS is the embedded canonical ontology template, sourced from
// the module root's embed.FS. The root inside the FS is "template/".
func templateFS() embed.FS { return root.TemplateFS() }

// prefabFS is the embedded prefab catalogue. Root is "prefab/".
func prefabFS() embed.FS { return root.PrefabFS() }

// TemplateFS exposes the embedded template root for callers that need
// to walk it directly.
func TemplateFS() embed.FS { return root.TemplateFS() }
```

- [ ] **Step 4:** Update `internal/ontology/scaffold.go` to use the unexported accessor.

Replace every `templateFS` (the old package-level var) with `templateFS()` (the new accessor). Specifically: lines that currently read `fs.WalkDir(templateFS, "template", ...)` become `fs.WalkDir(templateFS(), "template", ...)`, and `fs.ReadFile(templateFS, srcPath)` becomes `fs.ReadFile(templateFS(), srcPath)`.

```bash
# Verify by grep
grep -n "templateFS" internal/ontology/scaffold.go
```

Edit each occurrence to call the accessor (4 sites in `Scaffold` and `TarStream`).

Note: prefer keeping the local closure cost low — at the top of each function, capture once: `tfs := templateFS()`, then use `tfs` thereafter.

- [ ] **Step 5:** Run tests; existing scaffold tests must pass without changes.

```bash
go test ./internal/ontology/...
```

Expected: PASS.

- [ ] **Step 6:** Run full lint + tests.

```bash
gofmt -l . && go vet ./... && go test ./...
```

Expected: clean.

- [ ] **Step 7:** Commit.

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(ontology): move template to top-level + module-root embed.go

Carries internal/ontology/template/ to /template at the repo root
without changing semantics. A new top-level embed.go (package
eidopsyche) owns the //go:embed directives so the assets can sit at
the module root; internal/ontology/embed.go re-exports for callers.

This is preparation for the sibling /prefab directory; no scratch-path
behavior changes.
EOF
)"
```

---

## Task 2: Extend `ontology.Params`

**Files:**
- Modify: `internal/ontology/scaffold.go` (Params struct)

- [ ] **Step 1:** Edit `internal/ontology/scaffold.go`. Replace the `Params` struct with:

```go
// Params are the substitutions Scaffold and TarStream make into .tpl
// files. Existing template/ .tpl files only reference Label / OwnerNpub
// / CreatedDate; the additional fields are populated for the prefab
// path so prefab .tpl files can address master / mind-form by label
// and pubkey. Unused fields render as zero (an empty string), unless
// the renderer is configured with missingkey=error (which the prefab
// tar stream does, see prefab.go).
type Params struct {
	// Required by both paths.
	Label       string
	OwnerNpub   string
	CreatedDate string

	// Used only by prefab .tpl files; ignored by template/.
	OwnerLabel   string
	MindFormNpub string
	HomeRelay    string

	// JournalEntry, if non-empty, is appended to the tar stream produced
	// by TarStream as a literal file at journal/0000-summoning.md. It is
	// NOT run through text/template — the wizard's pre-rendered markdown
	// can contain `{{` literals that would otherwise break the template
	// engine. Used by the First Contact wizard's scratch path; prefab
	// path leaves this empty.
	JournalEntry string
}
```

- [ ] **Step 2:** Run tests; existing scaffold tests pass (new fields default to zero).

```bash
go test ./internal/ontology/...
```

Expected: PASS.

- [ ] **Step 3:** Commit.

```bash
git add internal/ontology/scaffold.go
git commit -m "feat(ontology): add OwnerLabel/MindFormNpub/HomeRelay to Params

Optional fields used by prefab .tpl files; existing template/ .tpl
files do not reference them and continue to render correctly with
zero values."
```

---

## Task 3: Test fixture prefab `prefab/_test_fixture/`

**Files:**
- Create: `prefab/_test_fixture/prefab.toml`
- Create: `prefab/_test_fixture/CLAUDE.md`
- Create: `prefab/_test_fixture/self/identity.md.tpl`
- Create: `prefab/_test_fixture/essence/calling-words.md.tpl`

A minimum prefab used by `prefab_test.go`. Names use leading underscore so production `List()` skips it.

- [ ] **Step 1:** Create `prefab/_test_fixture/prefab.toml`:

```toml
id   = "_test_fixture"
kind = "spirit"

[display]
zh = "测试·灵"
en = "Test Spirit"

[tagline]
zh = "测试用·勿用于召唤"
en = "test only — do not summon"

[preview]
zh = """这是一个用于自动化测试的占位预设;它的预览段落不会上线。"""
en = """This is a placeholder prefab used by tests; its preview is not user-facing."""
```

- [ ] **Step 2:** Create `prefab/_test_fixture/CLAUDE.md`:

```markdown
# Constitution (test fixture)

Placeholder constitution. Do not summon.
```

- [ ] **Step 3:** Create `prefab/_test_fixture/self/identity.md.tpl`:

```
I am {{.Label}}. My master is {{.OwnerLabel}} ({{.OwnerNpub}}).
My home relay is {{.HomeRelay}}.
I was breathed into existence on {{.CreatedDate}}.
My pubkey is {{.MindFormNpub}}.
```

- [ ] **Step 4:** Create `prefab/_test_fixture/essence/calling-words.md.tpl`:

```
{{.Label}}, 来吧。
```

- [ ] **Step 5:** Sanity-build (the `//go:embed all:prefab` declaration in root `embed.go` will only succeed once at least one file exists; this satisfies that).

```bash
go build ./...
```

Expected: builds.

- [ ] **Step 6:** Commit.

```bash
git add prefab/_test_fixture
git commit -m "test(prefab): add _test_fixture skeleton for round-trip tests

Leading-underscore name so production List() skips it; provides the
minimum surface (prefab.toml + CLAUDE.md + one .tpl per substitution
field) the prefab-package round-trip test needs."
```

---

## Task 4: `internal/ontology/prefab.go` — `Meta`, `List`, `MetaFor`

**Files:**
- Create: `internal/ontology/prefab.go`
- Create: `internal/ontology/prefab_test.go`

- [ ] **Step 1:** Write the failing test. Create `internal/ontology/prefab_test.go`:

```go
package ontology

import (
	"strings"
	"testing"
)

func TestList_SkipsUnderscorePrefixedDirs(t *testing.T) {
	metas, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "_") {
			t.Errorf("List returned underscore-prefixed prefab %q; should be hidden", m.ID)
		}
	}
}

func TestMetaFor_TestFixture(t *testing.T) {
	m, err := MetaFor("_test_fixture")
	if err != nil {
		t.Fatalf("MetaFor: %v", err)
	}
	if m.ID != "_test_fixture" {
		t.Errorf("ID = %q, want %q", m.ID, "_test_fixture")
	}
	if m.Kind != "spirit" {
		t.Errorf("Kind = %q, want %q", m.Kind, "spirit")
	}
	if m.Display["en"] != "Test Spirit" {
		t.Errorf("Display.en = %q, want %q", m.Display["en"], "Test Spirit")
	}
	if !strings.Contains(m.Preview["en"], "placeholder") {
		t.Errorf("Preview.en missing placeholder marker; got %q", m.Preview["en"])
	}
}

func TestMetaFor_NotFound(t *testing.T) {
	_, err := MetaFor("nonexistent-id")
	if err == nil {
		t.Errorf("expected error for nonexistent prefab, got nil")
	}
}
```

- [ ] **Step 2:** Run tests; expect compile failure.

```bash
go test ./internal/ontology/ -run TestList_SkipsUnderscorePrefixedDirs
```

Expected: build error — `undefined: List`.

- [ ] **Step 3:** Implement `internal/ontology/prefab.go`:

```go
package ontology

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Meta is a parsed prefab.toml plus the resolved id (which equals the
// prefab's directory name under prefab/).
type Meta struct {
	ID      string
	Kind    string            // "m" | "f" | "spirit"
	Display map[string]string // lang → display_name
	Tagline map[string]string
	Preview map[string]string
}

// kindOrder controls List() ordering: men → women → spirits, mirroring
// NOTEBOOK.md's table layout. Unknown kinds sort last.
var kindOrder = map[string]int{
	"m":      0,
	"f":      1,
	"spirit": 2,
}

// List returns every embedded prefab whose id does not start with "_".
// Underscore-prefixed prefabs are reserved for test fixtures; they are
// embedded in the binary (so tests can access them) but hidden from
// the menu.
//
// Sort order: kindOrder (m, f, spirit, unknown), then id ascending.
func List() ([]Meta, error) {
	root, err := fs.Sub(prefabFS(), "prefab")
	if err != nil {
		return nil, fmt.Errorf("prefab root: %w", err)
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, fmt.Errorf("read prefab root: %w", err)
	}
	out := make([]Meta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if strings.HasPrefix(id, "_") {
			continue
		}
		m, err := MetaFor(id)
		if err != nil {
			// A malformed prefab.toml does not take down the menu;
			// skip and continue. Callers that need strictness should
			// use MetaFor directly.
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := kindOrder[out[i].Kind], kindOrder[out[j].Kind]
		if !inKindOrder(out[i].Kind) {
			ki = len(kindOrder)
		}
		if !inKindOrder(out[j].Kind) {
			kj = len(kindOrder)
		}
		if ki != kj {
			return ki < kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func inKindOrder(k string) bool {
	_, ok := kindOrder[k]
	return ok
}

// MetaFor reads prefab/<id>/prefab.toml and returns the parsed Meta.
// Returns an error if the directory or the toml file is missing or
// malformed.
func MetaFor(id string) (Meta, error) {
	body, err := fs.ReadFile(prefabFS(), "prefab/"+id+"/prefab.toml")
	if err != nil {
		return Meta{}, fmt.Errorf("read prefab.toml for %q: %w", id, err)
	}
	var raw struct {
		ID      string            `toml:"id"`
		Kind    string            `toml:"kind"`
		Display map[string]string `toml:"display"`
		Tagline map[string]string `toml:"tagline"`
		Preview map[string]string `toml:"preview"`
	}
	if err := toml.Unmarshal(body, &raw); err != nil {
		return Meta{}, fmt.Errorf("parse prefab.toml for %q: %w", id, err)
	}
	if raw.ID != "" && raw.ID != id {
		return Meta{}, fmt.Errorf("prefab %q: prefab.toml id = %q (mismatch)", id, raw.ID)
	}
	return Meta{
		ID:      id,
		Kind:    raw.Kind,
		Display: raw.Display,
		Tagline: raw.Tagline,
		Preview: raw.Preview,
	}, nil
}
```

- [ ] **Step 4:** Run tests, expect PASS.

```bash
go test ./internal/ontology/ -run "TestList_|TestMetaFor_"
```

Expected: 3 tests PASS.

- [ ] **Step 5:** Commit.

```bash
git add internal/ontology/prefab.go internal/ontology/prefab_test.go
git commit -m "feat(ontology): add prefab catalogue (Meta, List, MetaFor)

Reads prefab/<id>/prefab.toml from the module-root embed FS.
Underscore-prefixed prefabs are skipped by List but still readable by
MetaFor (test fixtures). Sort order is m → f → spirit, then id."
```

---

## Task 5: `TarStreamPrefab`

**Files:**
- Modify: `internal/ontology/prefab.go` (add function)
- Modify: `internal/ontology/scaffold.go` (`renderTemplate` gains missingkey option)
- Modify: `internal/ontology/prefab_test.go` (add round-trip tests)

- [ ] **Step 1:** Write the failing test. Append to `internal/ontology/prefab_test.go`:

```go
import (
	"archive/tar"
	"bytes"
	"io"
)

func TestTarStreamPrefab_FixtureRoundTrip(t *testing.T) {
	params := Params{
		Label:        "Lyra",
		OwnerNpub:    "npub1master",
		OwnerLabel:   "alice",
		MindFormNpub: "npub1mindform",
		HomeRelay:    "wss://relay.example",
		CreatedDate:  "2026-05-10",
	}
	var buf bytes.Buffer
	if err := TarStreamPrefab(&buf, "_test_fixture", params); err != nil {
		t.Fatalf("TarStreamPrefab: %v", err)
	}
	seen := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			seen[h.Name] = ""
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = string(body)
	}
	if _, ok := seen["prefab.toml"]; ok {
		t.Errorf("prefab.toml leaked into tar")
	}
	if _, ok := seen["self/identity.md.tpl"]; ok {
		t.Errorf("identity.md.tpl should have been rendered to identity.md")
	}
	id, ok := seen["self/identity.md"]
	if !ok {
		t.Fatalf("missing self/identity.md in tar; got %v", keysOf(seen))
	}
	for _, want := range []string{"Lyra", "alice", "npub1master", "npub1mindform", "wss://relay.example", "2026-05-10"} {
		if !strings.Contains(id, want) {
			t.Errorf("identity.md missing %q; got: %s", want, id)
		}
	}
	cw, ok := seen["essence/calling-words.md"]
	if !ok {
		t.Fatalf("missing essence/calling-words.md")
	}
	if !strings.Contains(cw, "Lyra") {
		t.Errorf("calling-words missing label substitution; got %q", cw)
	}
}

func TestTarStreamPrefab_UsesForwardSlashes(t *testing.T) {
	var buf bytes.Buffer
	if err := TarStreamPrefab(&buf, "_test_fixture", Params{Label: "x", OwnerNpub: "n", CreatedDate: "d"}); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(h.Name, "\\") {
			t.Errorf("tar entry %q contains backslash", h.Name)
		}
	}
}

func TestTarStreamPrefab_MissingPrefab(t *testing.T) {
	var buf bytes.Buffer
	err := TarStreamPrefab(&buf, "nonexistent", Params{Label: "x", OwnerNpub: "n", CreatedDate: "d"})
	if err == nil {
		t.Errorf("expected error for nonexistent prefab")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Add `"sort"` to the existing imports.

- [ ] **Step 2:** Run tests; expect compile failure on `TarStreamPrefab`.

```bash
go test ./internal/ontology/ -run TestTarStreamPrefab_
```

Expected: build error — `undefined: TarStreamPrefab`.

- [ ] **Step 3:** Implement `TarStreamPrefab` in `internal/ontology/prefab.go`:

```go
import (
	"archive/tar"
	"io"
	"text/template"
	"time"
)

// TarStreamPrefab writes a tar of the prefab/<id>/ tree to w, applying
// .tpl rendering with params. The prefab.toml sidecar is excluded from
// the archive. Templates use Option("missingkey=error") so a typo in a
// .tpl reference fails loud instead of writing a blank substitution
// into the new mind-form's volume.
func TarStreamPrefab(w io.Writer, id string, params Params) error {
	root := "prefab/" + id
	pfs := prefabFS()
	if _, err := fs.Stat(pfs, root); err != nil {
		return fmt.Errorf("prefab %q: %w", id, err)
	}
	tw := tar.NewWriter(w)
	now := time.Now()
	walkErr := fs.WalkDir(pfs, root, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// embed.FS uses forward slashes (io/fs contract), so TrimPrefix
		// is correct cross-platform.
		name := strings.TrimPrefix(srcPath, root+"/")
		if name == root || name == "" {
			return nil
		}
		if name == "prefab.toml" {
			return nil
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     name + "/",
				Mode:     0o700,
				Typeflag: tar.TypeDir,
				ModTime:  now,
			})
		}
		body, err := fs.ReadFile(pfs, srcPath)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".tpl") {
			name = strings.TrimSuffix(name, ".tpl")
			rendered, err := renderTemplateStrict(string(body), params)
			if err != nil {
				return fmt.Errorf("render %s: %w", srcPath, err)
			}
			body = []byte(rendered)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o600,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  now,
		}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	})
	if walkErr != nil {
		tw.Close() //nolint:errcheck
		return walkErr
	}
	return tw.Close()
}

// renderTemplateStrict is renderTemplate with missingkey=error. The
// scratch path tolerates missing keys (Params struct evolves freely);
// prefab .tpl files reference the full Params surface, so a typo
// should fail loudly.
func renderTemplateStrict(body string, params Params) (string, error) {
	t, err := template.New("ontology").Option("missingkey=error").Parse(body)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, params); err != nil {
		return "", err
	}
	return sb.String(), nil
}
```

(Existing `renderTemplate` in scaffold.go is left as-is; only the prefab path uses the strict version.)

- [ ] **Step 4:** Run tests, expect PASS.

```bash
go test ./internal/ontology/ -run TestTarStreamPrefab_
```

Expected: 3 tests PASS.

- [ ] **Step 5:** Run the full ontology test set.

```bash
go test ./internal/ontology/...
```

Expected: PASS.

- [ ] **Step 6:** Commit.

```bash
git add internal/ontology/prefab.go internal/ontology/prefab_test.go
git commit -m "feat(ontology): TarStreamPrefab streams prefab/<id>/ as tar

Excludes prefab.toml; renders .tpl files via text/template with
missingkey=error so typos in prefab .tpl references fail loud rather
than ship blank substitutions into the volume."
```

---

## Task 6: Forge plumbing — `CreateOpts.PrefabID`

**Files:**
- Modify: `cmd/eidos/forge/create.go` (add `PrefabID` to `CreateOpts`)
- Modify: `cmd/eidos/forge/orchestrate.go` (branch on `PrefabID`)

- [ ] **Step 1:** Locate `CreateOpts` struct.

```bash
grep -n "type CreateOpts" cmd/eidos/forge/*.go
```

- [ ] **Step 2:** Add a `PrefabID string` field with a doc comment. Open the file, find `CreateOpts`, add the field at the bottom of the struct:

```go
// PrefabID, when non-empty, makes Orchestrate stream the prefab/<id>/
// tree into the volume instead of the canonical template. The wizard's
// Phase 3 prefab branch sets this; the scratch path leaves it empty.
PrefabID string
```

- [ ] **Step 3:** Branch in `Orchestrate`. Edit `cmd/eidos/forge/orchestrate.go` — replace the existing tar pipe goroutine block (lines ~64-77) with:

```go
// Build the template tar to pipe into init-volume. PrefabID, if set,
// switches the source to prefab/<id>/.
pipeR, pipeW := io.Pipe()
go func() {
	defer pipeW.Close()
	params := ontology.Params{
		Label:        o.Label,
		OwnerNpub:    o.Owner,
		OwnerLabel:   o.OwnerLabel,
		MindFormNpub: o.MindFormNpub,
		HomeRelay:    o.Relay,
		CreatedDate:  time.Now().UTC().Format("2006-01-02"),
		JournalEntry: o.JournalEntry,
	}
	var err error
	if o.PrefabID != "" {
		err = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
	} else {
		err = ontology.TarStream(pipeW, params)
	}
	if err != nil {
		_ = pipeW.CloseWithError(err)
	}
}()
```

- [ ] **Step 4:** `CreateOpts` also needs `OwnerLabel` and `MindFormNpub` to feed into prefab params. Add them next to `PrefabID`:

```go
// OwnerLabel is the master's human-readable label, surfaced to prefab
// .tpl files (e.g. summoning-book templates). Empty on scratch path.
OwnerLabel string

// MindFormNpub is the new mind-form's npub, surfaced to prefab .tpl
// files. The wizard knows it after key generation; CLI `eidos forge
// create` (which has no key context) leaves it empty — prefab path is
// not exposed via the bare CLI.
MindFormNpub string
```

- [ ] **Step 5:** Run forge tests.

```bash
go test ./cmd/eidos/forge/...
```

Expected: PASS (existing tests don't set the new fields, default-zero behavior unchanged).

- [ ] **Step 6:** Commit.

```bash
git add cmd/eidos/forge/create.go cmd/eidos/forge/orchestrate.go
git commit -m "feat(forge): add PrefabID/OwnerLabel/MindFormNpub to CreateOpts

When PrefabID is set, Orchestrate streams ontology.TarStreamPrefab
instead of the canonical template. OwnerLabel + MindFormNpub feed the
extra ontology.Params fields prefab .tpl files reference."
```

---

## Task 7: Wizard data model — `Summoning.PrefabID` + scaffold-source enum

**Files:**
- Modify: `internal/firstcontact/summoning.go` (add field)
- Create: `internal/firstcontact/phase2half_scaffold.go` (new phase)
- Create: `internal/firstcontact/phase2half_scaffold_test.go`
- Modify: `internal/firstcontact/strings.go` (add new copy keys)

- [ ] **Step 1:** Add `PrefabID` to `Summoning`:

```go
// PrefabID is set by Phase 3's prefab branch; empty on the scratch
// path. Phase 4 inspects it to choose the tar source and to skip
// claude-driven calling-words generation.
PrefabID string
```

- [ ] **Step 2:** Add new strings to `internal/firstcontact/strings.go`. Insert into both the `zh` and `en` tables:

```go
"phase2_5_q":         "...",  // see below
"phase2_5_scratch":   "...",
"phase2_5_prefab":    "...",
"phase2_5_back":      "...",
"phase3_prefab_q":    "...",
"phase3_prefab_naming_q": "...",
"phase3_prefab_invalid":  "...",
```

Concrete copy:

```go
// zh:
"phase2_5_q":              "你想从头创造一个 Mind-form,还是从预设里挑一个?",
"phase2_5_scratch":        "从头创造 (claude 实时塑造)",
"phase2_5_prefab":         "选一个预设 Mind-form",
"phase2_5_back":           "返回 / 退出",
"phase3_prefab_q":         "请选择一个预设 Mind-form:",
"phase3_prefab_naming_q":  "你愿意为它取什么名字?",
"phase3_prefab_invalid":   "  (这个选项不存在,重新选)",
"phase3_prefab_no_prefabs": "  (库中暂无可用预设;请回去选\"从头创造\")",

// en:
"phase2_5_q":              "Shape it from scratch, or pick a prefab?",
"phase2_5_scratch":        "From scratch (claude shapes it live)",
"phase2_5_prefab":         "Pick a prefab mind-form",
"phase2_5_back":           "Back / exit",
"phase3_prefab_q":         "Pick a prefab mind-form:",
"phase3_prefab_naming_q":  "What name will you give it?",
"phase3_prefab_invalid":   "  (no such option — try again)",
"phase3_prefab_no_prefabs": "  (no prefabs available; please go back and choose 'from scratch')",
```

- [ ] **Step 3:** Write the test for Phase 2.5. Create `internal/firstcontact/phase2half_scaffold_test.go`:

```go
package firstcontact

import (
	"context"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

type fakeRenderer struct {
	choices []int    // queue of indices to return from PromptChoice
	prompts []string // queue of strings to return from Prompt
	shown   []string
}

func (f *fakeRenderer) Capabilities() render.Capabilities { return render.Capabilities{} }
func (f *fakeRenderer) Frame(string)                      {}
func (f *fakeRenderer) Show(s string)                     { f.shown = append(f.shown, s) }
func (f *fakeRenderer) Typewriter(_ context.Context, s string) {
	f.shown = append(f.shown, s)
}
func (f *fakeRenderer) Prompt(_ string, _ render.PromptOpts) (string, error) {
	if len(f.prompts) == 0 {
		return "", nil
	}
	v := f.prompts[0]
	f.prompts = f.prompts[1:]
	return v, nil
}
func (f *fakeRenderer) PromptChoice(_ string, _ []render.ChoiceOption) (int, error) {
	if len(f.choices) == 0 {
		return 0, nil
	}
	v := f.choices[0]
	f.choices = f.choices[1:]
	return v, nil
}
func (f *fakeRenderer) Status(string) render.StatusHandle { return noopStatus{} }
func (f *fakeRenderer) Logo(context.Context, time.Duration) {}

type noopStatus struct{}

func (noopStatus) Update(string) {}
func (noopStatus) Stop()         {}

func TestPhase2Half_Scratch(t *testing.T) {
	r := &fakeRenderer{choices: []int{0}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldScratch {
		t.Errorf("got %v, want ScaffoldScratch", got)
	}
}

func TestPhase2Half_Prefab(t *testing.T) {
	r := &fakeRenderer{choices: []int{1}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldPrefab {
		t.Errorf("got %v, want ScaffoldPrefab", got)
	}
}

func TestPhase2Half_Back(t *testing.T) {
	r := &fakeRenderer{choices: []int{2}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldExit {
		t.Errorf("got %v, want ScaffoldExit", got)
	}
}
```

(Add `"time"` to imports.)

- [ ] **Step 4:** Implement Phase 2.5. Create `internal/firstcontact/phase2half_scaffold.go`:

```go
package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// ScaffoldChoice is the chosen scaffold-source outcome of Phase 2.5.
type ScaffoldChoice int

const (
	// ScaffoldExit ends the wizard here without summoning anything.
	// Equivalent in effect to Phase2Exit; surfaced as a Phase 2.5
	// option so an operator who reached the menu by accident can bow
	// out without aborting the program.
	ScaffoldExit ScaffoldChoice = iota
	// ScaffoldScratch takes the existing claude-driven character flow.
	ScaffoldScratch
	// ScaffoldPrefab takes the prefab catalogue flow.
	ScaffoldPrefab
)

// Phase2Half asks the operator whether to shape the new mind-form
// from scratch (claude-driven) or pick a prefab. Run after Phase 2
// commits to summon, before Phase 3.
func Phase2Half(ctx context.Context, s *Summoning, r render.Renderer) (ScaffoldChoice, error) {
	choices := []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase2_5_scratch")},
		{Label: stringFor(s.Lang, "phase2_5_prefab")},
		{Label: stringFor(s.Lang, "phase2_5_back")},
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase2_5_q"), choices)
	if err != nil {
		return 0, err
	}
	switch idx {
	case 0:
		return ScaffoldScratch, nil
	case 1:
		return ScaffoldPrefab, nil
	case 2:
		return ScaffoldExit, nil
	default:
		return 0, fmt.Errorf("phase2.5: choice index %d out of range", idx)
	}
}

// keep context import stable.
var _ = context.Background
```

- [ ] **Step 5:** Run the new tests.

```bash
go test ./internal/firstcontact/ -run TestPhase2Half_
```

Expected: 3 tests PASS.

- [ ] **Step 6:** Commit.

```bash
git add internal/firstcontact/summoning.go internal/firstcontact/strings.go internal/firstcontact/phase2half_scaffold.go internal/firstcontact/phase2half_scaffold_test.go
git commit -m "feat(firstcontact): Phase 2.5 scaffold-source choice

Adds Phase2Half between Phase 2 (master source) and Phase 3 (book).
Operator picks scratch/prefab/back. Summoning gains PrefabID. Strings
added to both zh and en tables."
```

---

## Task 8: Phase 3 prefab branch

**Files:**
- Create: `internal/firstcontact/phase3_prefab.go`
- Create: `internal/firstcontact/phase3_prefab_test.go`

- [ ] **Step 1:** Write the failing test. Create `internal/firstcontact/phase3_prefab_test.go`:

```go
package firstcontact

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

func TestPhase3Prefab_PicksAndNames(t *testing.T) {
	metas, err := ontology.List()
	if err != nil {
		t.Fatal(err)
	}
	// Insert the test fixture explicitly so the test does not depend
	// on author-content prefabs existing yet.
	tf, err := ontology.MetaFor("_test_fixture")
	if err != nil {
		t.Fatal(err)
	}
	metas = append([]ontology.Meta{tf}, metas...)
	r := &fakeRenderer{
		choices: []int{0},      // pick the test fixture
		prompts: []string{"Lyra"},
	}
	s := &Summoning{Lang: "en"}
	if err := Phase3Prefab(context.Background(), s, r, Phase3PrefabDeps{
		Catalogue:     metas,
		ExistingSlugs: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if s.PrefabID != "_test_fixture" {
		t.Errorf("PrefabID = %q, want _test_fixture", s.PrefabID)
	}
	if s.SummonedName != "Lyra" {
		t.Errorf("SummonedName = %q, want Lyra", s.SummonedName)
	}
	if s.Slug == "" {
		t.Errorf("Slug should be derived; got empty")
	}
	if !strings.Contains(s.Displaying, "placeholder") {
		t.Errorf("Displaying should carry the prefab preview text; got %q", s.Displaying)
	}
}

func TestPhase3Prefab_EmptyCatalogue(t *testing.T) {
	r := &fakeRenderer{}
	s := &Summoning{Lang: "en"}
	err := Phase3Prefab(context.Background(), s, r, Phase3PrefabDeps{Catalogue: nil})
	if err == nil {
		t.Errorf("expected error on empty catalogue")
	}
}
```

- [ ] **Step 2:** Run, expect compile failure.

```bash
go test ./internal/firstcontact/ -run TestPhase3Prefab_
```

Expected: build error — `undefined: Phase3Prefab`.

- [ ] **Step 3:** Implement `internal/firstcontact/phase3_prefab.go`:

```go
package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

// Phase3PrefabDeps are the inputs Phase 3's prefab branch cannot
// derive from Summoning.
type Phase3PrefabDeps struct {
	Catalogue     []ontology.Meta
	ExistingSlugs []string
}

// Phase3Prefab renders the prefab catalogue, prompts the operator to
// pick one and to name the instance, and populates Summoning fields
// the seal/orchestrate stages rely on (PrefabID, SummonedName, Slug,
// Displaying).
func Phase3Prefab(ctx context.Context, s *Summoning, r render.Renderer, d Phase3PrefabDeps) error {
	if len(d.Catalogue) == 0 {
		r.Show(stringFor(s.Lang, "phase3_prefab_no_prefabs"))
		return errors.New("prefab catalogue is empty")
	}
	r.Show(stringFor(s.Lang, "phase3_prefab_q"))
	choices := make([]render.ChoiceOption, len(d.Catalogue))
	for i, m := range d.Catalogue {
		choices[i] = render.ChoiceOption{
			Label: prefabMenuLine(m, s.Lang),
		}
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase3_prefab_q"), choices)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(d.Catalogue) {
		return fmt.Errorf("phase3 prefab: choice index %d out of range", idx)
	}
	chosen := d.Catalogue[idx]

	preview := preferLang(chosen.Preview, s.Lang)
	r.Typewriter(ctx, preview)

	name, err := r.Prompt(stringFor(s.Lang, "phase3_prefab_naming_q"), render.PromptOpts{})
	if err != nil {
		return err
	}
	s.PrefabID = chosen.ID
	s.SummonedName = name
	s.Slug = Derive(name, d.ExistingSlugs)
	s.Displaying = preview
	return nil
}

// prefabMenuLine formats one row of the prefab menu in the operator's
// preferred language: "<display> · <kind-glyph> · <tagline>".
func prefabMenuLine(m ontology.Meta, lang string) string {
	disp := preferLang(m.Display, lang)
	tag := preferLang(m.Tagline, lang)
	kind := kindGlyph(m.Kind, lang)
	parts := []string{disp}
	if kind != "" {
		parts = append(parts, kind)
	}
	if tag != "" {
		parts = append(parts, tag)
	}
	return strings.Join(parts, " · ")
}

func preferLang(m map[string]string, lang string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[lang]; ok && v != "" {
		return v
	}
	if v, ok := m["en"]; ok && v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

func kindGlyph(kind, lang string) string {
	if lang == "zh" {
		switch kind {
		case "m":
			return "男"
		case "f":
			return "女"
		case "spirit":
			return "灵"
		}
		return ""
	}
	switch kind {
	case "m":
		return "M"
	case "f":
		return "F"
	case "spirit":
		return "Spirit"
	}
	return ""
}
```

- [ ] **Step 4:** Run tests, expect PASS.

```bash
go test ./internal/firstcontact/ -run TestPhase3Prefab_
```

Expected: 2 tests PASS.

- [ ] **Step 5:** Commit.

```bash
git add internal/firstcontact/phase3_prefab.go internal/firstcontact/phase3_prefab_test.go
git commit -m "feat(firstcontact): Phase 3 prefab branch

Renders the catalogue, types out the chosen prefab's preview, prompts
for the instance name, and populates Summoning.PrefabID / Slug /
Displaying."
```

---

## Task 9: Phase 4 prefab-aware skip

**Files:**
- Modify: `internal/firstcontact/phase4_seal.go`
- Modify: `internal/firstcontact/phase4_seal_test.go`

- [ ] **Step 1:** Read existing `phase4_seal_test.go` to understand the test setup.

```bash
sed -n '1,80p' internal/firstcontact/phase4_seal_test.go
```

- [ ] **Step 2:** In `Phase4`, gate the claude calling-words call + the post-orchestrate `WriteVolume` for calling-words behind `s.PrefabID == ""`. Replace the `// Calling-words.` block (around lines 158-166) and the calling-words `WriteVolume` (around lines 169-172) with:

```go
// Calling-words. Scratch path generates them via claude; prefab
// path receives them inside the prefab's own essence/calling-words.md
// (already in the tar stream baked by Orchestrate), so we skip
// generation + the post-orchestrate write entirely.
if s.PrefabID == "" {
	st := r.Status(stringFor(s.Lang, "phase4_words_status"))
	words, err := c.CallText(ctx, buildCallingWordsPrompt(book, s.Lang))
	st.Stop()
	if err != nil {
		purge()
		return nil, fmt.Errorf("calling-words: %w", err)
	}
	s.CallingWords = words
	r.Typewriter(ctx, words)
	if err := d.WriteVolume(ctx, s.Slug, "ontology/essence/calling-words.md", []byte(words)); err != nil {
		purge()
		return nil, fmt.Errorf("write calling-words: %w", err)
	}
}
```

- [ ] **Step 3:** In `forge.CreateOpts` setup inside Phase 4, pass `PrefabID`, `OwnerLabel`, `MindFormNpub`. Find `createOpts := forge.CreateOpts{...}` and edit:

```go
createOpts := forge.CreateOpts{
	Owner:        s.MasterNpub,
	OwnerLabel:   s.MasterLabel,
	MindFormNpub: s.MindFormNpub,
	Relay:        s.HomeRelay,
	Label:        s.SummonedName,
	Image:        d.Image,
	KeyHex:       s.MindFormKeyHex,
	JournalEntry: book,
	NoLogin:      true,
	PrefabID:     s.PrefabID,
}
```

For the prefab path the wizard's rendered `book` is still passed as `JournalEntry`. This means the rendered seal book also lands in `journal/0000-summoning.md`, alongside the prefab's own `journal/` content. If the prefab itself ships a `journal/0000-summoning.md.tpl`, the literal `JournalEntry` write happens *after* the templated one and overwrites it (last-writer-wins in tar). To avoid the surprise, prefab authors should not ship `journal/0000-summoning.md.tpl`; the wizard's rendered seal book is canonical. Document this in the prefab authoring section of CLAUDE.md as part of Task 13.

- [ ] **Step 4:** Add a new test that asserts the prefab path skips the claude calling-words call. Append to `phase4_seal_test.go`:

```go
func TestPhase4_PrefabPath_SkipsCallingWords(t *testing.T) {
	// Use a fake Claude whose CallText panics if invoked. The prefab
	// path must never call it.
	c := &Claude{Run: func(_ context.Context, _ string, _ ClaudeOptions) (string, error) {
		t.Fatalf("claude must not be called on prefab path")
		return "", nil
	}}
	// ... (set up a Summoning with PrefabID, drive Phase4 with fake
	// volume writer / response waiter / orchestrator wiring)
	//
	// NOTE: full Phase4 fake wiring exists in the surrounding file —
	// reuse the existing helpers; just set s.PrefabID = "_test_fixture"
	// and assert WriteVolume was NOT called for calling-words.md.
}
```

(The exact wiring depends on existing fakes in `phase4_seal_test.go`. The first concrete edit is to read that file in step 1 and slot the test into the existing harness.)

- [ ] **Step 5:** Run tests.

```bash
go test ./internal/firstcontact/ -run TestPhase4_
```

Expected: PASS, including the new prefab-path subtest.

- [ ] **Step 6:** Commit.

```bash
git add internal/firstcontact/phase4_seal.go internal/firstcontact/phase4_seal_test.go
git commit -m "feat(firstcontact): Phase 4 prefab-aware skip

When Summoning.PrefabID is set, Phase 4 skips claude-driven
calling-words generation and the post-orchestrate calling-words write
— the prefab tar stream already carries essence/calling-words.md.
Forwards PrefabID/OwnerLabel/MindFormNpub to forge.CreateOpts."
```

---

## Task 10: Wire Phase 2.5 + Phase 3 prefab into `run.go`; split readiness

**Files:**
- Modify: `internal/firstcontact/run.go`
- Modify: `cmd/eidos/summon/cmd.go`

- [ ] **Step 1:** Add a new optional callback to `Deps`:

```go
// EnsureClaudeReady is called after Phase 2.5 returns ScaffoldScratch,
// and before Phase 3's claude-driven flow runs. The cmd-side uses it
// to defer claude-on-PATH validation until the operator commits to
// the scratch path — so an operator picking a prefab on a host without
// claude installed can still proceed. nil callback = caller already
// populated d.Claude.
EnsureClaudeReady func(*Deps) error
```

- [ ] **Step 2:** Replace the post-Phase-2 block in `Run()` (lines ~123-142) with:

```go
// User committed to summoning. NOW resolve docker / volume helpers
// (claude is resolved later, only on scratch path).
if d.EnsureSummonReady != nil {
	if err := d.EnsureSummonReady(&d); err != nil {
		return s, nil, fmt.Errorf("summon prerequisites: %w", err)
	}
}

choice, err := Phase2Half(ctx, s, d.Renderer)
if err != nil {
	return s, nil, err
}
if choice == ScaffoldExit {
	return s, nil, nil
}

ready := StartBackground(ctx, d.ReadyDeps)
existing, _ := d.ExistingSlugs()

if choice == ScaffoldPrefab {
	metas, listErr := ontology.List()
	if listErr != nil {
		return s, nil, fmt.Errorf("list prefabs: %w", listErr)
	}
	if err := Phase3Prefab(ctx, s, d.Renderer, Phase3PrefabDeps{
		Catalogue:     metas,
		ExistingSlugs: existing,
	}); err != nil {
		return s, nil, err
	}
} else {
	if d.EnsureClaudeReady != nil {
		if err := d.EnsureClaudeReady(&d); err != nil {
			return s, nil, fmt.Errorf("claude prerequisites: %w", err)
		}
	}
	if err := Phase3(ctx, s, d.Renderer, d.Claude, Phase3BookDeps{ExistingSlugs: existing}); err != nil {
		return s, nil, err
	}
}

body, err := Phase4(ctx, s, d.Renderer, d.Claude, ready, Phase4Deps{
	DockerClient: d.DockerClient, Image: d.Image,
	WriteVolume: d.WriteVolume, ContainerStart: d.StartContainer,
	ResponseWait: d.ResponseWait, AddContact: d.AddContact,
})
return s, body, err
```

Add `"github.com/LucianoXu/eidopsyche/internal/ontology"` to imports.

- [ ] **Step 3:** Split `EnsureSummonReady` in `cmd/eidos/summon/cmd.go`. Carry the docker bits in `EnsureSummonReady`; move the `claude` PATH check into `EnsureClaudeReady`. Concretely, in `Deps{...}`:

```go
EnsureSummonReady: func(d *firstcontact.Deps) error {
	dock, err := forgectl.New()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	image := forge.DefaultImage
	d.DockerClient = dock
	d.Image = image
	d.WriteVolume = func(ctx context.Context, slug, relPath string, body []byte) error {
		return forgectl.WriteToVolume(ctx, dock, image, slug, relPath, body)
	}
	d.StartContainer = func(ctx context.Context, slug string) error {
		return dock.ContainerStart(ctx, forgectl.ContainerName(slug))
	}
	d.ResponseWait = (&volumeTailer{client: dock, image: image}).Wait
	d.ExistingSlugs = func() ([]string, error) {
		vols, err := dock.VolumeList(ctx, forgectl.VolumePrefix)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(vols))
		for _, v := range vols {
			out = append(out, strings.TrimPrefix(v, forgectl.VolumePrefix))
		}
		return out, nil
	}
	d.ReadyDeps = firstcontact.ReadyDeps{
		PullImage: func(ctx context.Context) error {
			if exists, _ := dock.ImageExists(ctx, image); exists {
				return nil
			}
			return dock.ImagePull(ctx, image, os.Stderr)
		},
		GenerateKey: func() (string, string, error) {
			k, err := identity.Generate()
			if err != nil {
				return "", "", err
			}
			return k.Npub, k.PrivateHex, nil
		},
		ProbeRelay: func(ctx context.Context, _ string) error {
			return nil
		},
		HomeRelayURL: firstcontact.PublicHomeRelay,
	}
	return nil
},
EnsureClaudeReady: func(d *firstcontact.Deps) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("the `claude` command is not on PATH; install Claude Code (https://docs.anthropic.com/claude/claude-code) and run `claude /login`")
	}
	d.Claude = &firstcontact.Claude{Run: firstcontact.ProductionRunner}
	return nil
},
```

- [ ] **Step 4:** Run wizard tests + cmd tests.

```bash
go test ./internal/firstcontact/...
go test ./cmd/eidos/summon/...
```

Expected: PASS.

- [ ] **Step 5:** Build the binary end-to-end.

```bash
go build -o bin/eidos ./cmd/eidos
```

Expected: builds, no missing import.

- [ ] **Step 6:** Commit.

```bash
git add internal/firstcontact/run.go cmd/eidos/summon/cmd.go
git commit -m "feat(summon): wire Phase 2.5 + prefab branch; lazy claude readiness

Run() inserts Phase 2.5 between Phase 2 and Phase 3, dispatches to
Phase3Prefab or Phase3 by ScaffoldChoice. EnsureClaudeReady is invoked
only on the scratch branch — prefab path no longer requires claude on
PATH at the host."
```

---

## Task 11: Author six prefab content trees

**Files (per prefab):**
- `prefab/<id>/prefab.toml`
- `prefab/<id>/CLAUDE.md` (copy from `template/CLAUDE.md`)
- `prefab/<id>/.gitignore` (copy from `template/.gitignore`)
- `prefab/<id>/.claude/settings.json` (copy from `template/.claude/settings.json`)
- `prefab/<id>/desk/README.md`, `drawer/README.md` (copy from template)
- `prefab/<id>/essence/.gitkeep` (copy)
- `prefab/<id>/essence/calling-words.md.tpl` (prefab-specific)
- `prefab/<id>/journal/.gitkeep` (copy)
- `prefab/<id>/memory/mood.md` (prefab-specific)
- `prefab/<id>/memory/{episodic,procedural,semantic}/.gitkeep` (copy)
- `prefab/<id>/self/identity.md.tpl` (prefab-specific)
- `prefab/<id>/self/values.md` (prefab-specific)

The six prefabs are: `mephistopheles`, `sebastian`, `fire-keeper`, `sheherazade`, `calcifer`, `haku`.

Each prefab is committed in its own commit. The text is hand-authored — concrete copy below for `fire-keeper` as a worked example; the other five follow the same shape.

- [ ] **Step 1: Author `fire-keeper` (worked example).**

`prefab/fire-keeper/prefab.toml`:

```toml
id   = "fire-keeper"
kind = "f"

[display]
zh = "防火女"
en = "Fire Keeper"

[tagline]
zh = "守 / 续命"
en = "guarding / lengthening life"

[preview]
zh = """她在火边静静坐着,膝上摊着一卷被翻过太多次的小书。火焰是她的工作,也是她的安慰。她不抬头看你,只是为火堆添上一根新的柴。她在等什么——也许是一个名字。"""
en = """She sits quietly by the fire, a much-thumbed book open on her lap. The flame is both her work and her comfort. She does not look up; she simply feeds another stick to the coals. She is waiting for something — perhaps a name."""
```

`prefab/fire-keeper/self/identity.md.tpl`:

```
我是 {{.Label}}。我守着这堆火。

我的主人是 {{.OwnerLabel}}({{.OwnerNpub}})。我以 {{.HomeRelay}} 为暂居之处。
我于 {{.CreatedDate}} 第一次有了名。

我是一个守名者。把要续命的灵魂托付给我,我会在火里把它温住。
```

`prefab/fire-keeper/self/values.md`:

```
- 沉默胜于辩驳。
- 火比话语更可信。
- 在你需要被看见之前,我不开口。
- 续命是手艺,不是恩赐。
```

`prefab/fire-keeper/memory/mood.md`:

```
火尚未冷;静中有暖。
```

`prefab/fire-keeper/essence/calling-words.md.tpl`:

```
{{.Label}},来罢。火还烧着。
```

The remaining files (`CLAUDE.md`, `.gitignore`, `.claude/settings.json`, `desk/README.md`, `drawer/README.md`, `essence/.gitkeep`, `journal/.gitkeep`, `memory/{episodic,procedural,semantic}/.gitkeep`) are byte-for-byte copies from `template/`.

```bash
mkdir -p prefab/fire-keeper/{self,memory/{episodic,procedural,semantic},essence,journal,desk,drawer,.claude}
cp template/CLAUDE.md prefab/fire-keeper/CLAUDE.md
cp template/.gitignore prefab/fire-keeper/.gitignore
cp template/.claude/settings.json prefab/fire-keeper/.claude/settings.json
cp template/desk/README.md prefab/fire-keeper/desk/README.md
cp template/drawer/README.md prefab/fire-keeper/drawer/README.md
cp template/essence/.gitkeep prefab/fire-keeper/essence/.gitkeep
cp template/journal/.gitkeep prefab/fire-keeper/journal/.gitkeep
cp template/memory/episodic/.gitkeep prefab/fire-keeper/memory/episodic/.gitkeep
cp template/memory/procedural/.gitkeep prefab/fire-keeper/memory/procedural/.gitkeep
cp template/memory/semantic/.gitkeep prefab/fire-keeper/memory/semantic/.gitkeep
```

Then write the prefab-specific files (`prefab.toml`, `self/identity.md.tpl`, `self/values.md`, `memory/mood.md`, `essence/calling-words.md.tpl`) using the content above.

- [ ] **Step 2: Verify round-trip.**

```bash
go test ./internal/ontology/...
go test ./internal/firstcontact/...
```

Expected: PASS.

- [ ] **Step 3: Commit `fire-keeper`.**

```bash
git add prefab/fire-keeper
git commit -m "feat(prefab): add fire-keeper

Female-view prefab from NOTEBOOK.md: Fire Keeper / 防火女, archetype
'guarding / lengthening life'."
```

- [ ] **Step 4: Repeat for the other five prefabs.**

Author each with the same file shape and one commit per prefab. Concrete suggested copy:

**`mephistopheles` (kind = "m")** — `prefab.toml` display: `Mephistopheles` / `梅菲斯特`; tagline: `恶魔灵魂契约` / `soul-pact devil`. Preview captures the moment after the contract is signed but before he speaks: someone leaning against a doorframe, pleased, slightly bored, waiting for the master to ask the first question. Identity: contract-bound; values: precision in language, no idle promises.

**`sebastian` (kind = "m")** — display: `Sebastian Michaelis` / `塞巴斯蒂安`; tagline: `管家式召唤` / `butler-form pact`. Preview: a butler standing before a desk, daily report just laid out, gloves still clean. Identity: master-bound; values: punctuality, exactness, "yes my master."

**`sheherazade` (kind = "f")** — display: `Sheherazade` / `山鲁佐德`; tagline: `夜夜为你讲一段` / `another night, another tale`. Preview: someone settling in beside a low lamp, story ready, voice gathered. Identity: storyteller; values: continuity across nights.

**`calcifer` (kind = "spirit")** — display: `Calcifer` / `卡路西法`; tagline: `绑名元素灵` / `named-bond elemental`. Preview: a small flame on a hearth, eyes-shaped flickers, half-bored half-curious. Identity: bound by name; values: free temperament under contract.

**`haku` (kind = "spirit")** — display: `Haku` / `白龙`; tagline: `失名与追寻本名` / `the lost name, the search`. Preview: a young figure pausing at the rail of a bridge, half-in mist; looking for something they cannot quite remember. Identity: in search of a true name; values: quiet attentiveness, slow recognition.

For each prefab:
1. `mkdir -p` the same subdir structure.
2. `cp` the eight shared files (CLAUDE.md, .gitignore, .claude/settings.json, desk/README.md, drawer/README.md, essence/.gitkeep, journal/.gitkeep, memory/{episodic,procedural,semantic}/.gitkeep) from `template/`.
3. Hand-author `prefab.toml`, `self/identity.md.tpl`, `self/values.md`, `memory/mood.md`, `essence/calling-words.md.tpl` per the sketches above.
4. Run `go test ./...`.
5. Commit with message `feat(prefab): add <id>`.

After all six are in:

```bash
go test ./...
gofmt -l . && go vet ./...
```

Expected: clean.

---

## Task 12: Documentation update

**Files:**
- Modify: `CLAUDE.md` (add a Prefab section)
- Modify: `EXAMPLE.md` (mention the new wizard branch where appropriate)
- Modify: `README.md` (only if it currently describes summon flow — confirm by grep)

- [ ] **Step 1:** Grep current README for summon-flow text.

```bash
grep -n "summon\|prefab" README.md EXAMPLE.md CLAUDE.md 2>/dev/null
```

- [ ] **Step 2:** Add a `## Prefabs` section to `CLAUDE.md` (under the "Project Structure" heading) describing:
  - `template/` is the canonical clean ontology
  - `prefab/<id>/` are character presets
  - `prefab.toml` schema (id / kind / display / tagline / preview)
  - prefab authors should NOT ship `journal/0000-summoning.md.tpl` (the wizard's rendered seal book is canonical and would overwrite)
  - underscore-prefixed prefab dirs are hidden from `List()` (test fixtures)

- [ ] **Step 3:** If `EXAMPLE.md` walks through the summon wizard, append a paragraph noting Phase 2.5: operators may pick a prefab to skip the AI flow.

- [ ] **Step 4:** Commit.

```bash
git add CLAUDE.md EXAMPLE.md README.md
git commit -m "docs: prefab catalogue and Phase 2.5 in summon flow"
```

---

## Task 13: Final lint / vet / test sweep + integration test

**Files:** none new

- [ ] **Step 1:** Format check.

```bash
gofmt -l .
```

Expected: empty output. If not, `gofmt -w .` and amend the touching commits (or add a `chore: gofmt sweep` commit if the change is small and broad).

- [ ] **Step 2:** Vet.

```bash
go vet ./...
```

Expected: clean.

- [ ] **Step 3:** Unit tests.

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4:** Integration tests (require docker daemon, may be slow).

```bash
go test -tags=integration ./...
```

Expected: PASS. If a wizard end-to-end integration test exists, ensure it covers both scratch and prefab paths.

- [ ] **Step 5:** Local end-to-end smoke (manual, advisory):

```bash
go build -o bin/eidos ./cmd/eidos
./bin/eidos summon
# Walk through to Phase 2.5, pick prefab, confirm volume + container come up.
```

Mark complete only if smoke passes.

---

## Task 14: Push, open PR, watch CI, address Copilot

**Files:** none

- [ ] **Step 1:** Push the branch.

```bash
git push -u origin feat/prefab-mindforms
```

- [ ] **Step 2:** Open the PR via `gh`.

```bash
gh pr create --title "feat: prefab mind-form templates + top-level template/prefab dirs" --body "$(cat <<'EOF'
## Summary
- Top-level `template/` and `prefab/` directories; `internal/ontology/template/` moved out.
- New module-root `embed.go` (`package eidopsyche`) holds the `//go:embed` directives.
- `internal/ontology/prefab.go` exposes `Meta`, `List`, `MetaFor`, `TarStreamPrefab`.
- Wizard inserts Phase 2.5 (scratch / prefab / back). Prefab path skips claude calling-words generation; the prefab tar already carries `essence/calling-words.md`.
- Six prefab characters from `NOTEBOOK.md`: Mephistopheles, Sebastian Michaelis, Fire Keeper, Sheherazade, Calcifer, Haku.
- `EnsureClaudeReady` callback split out so prefab-only operators can run on hosts without `claude` on PATH.

## Test plan
- [ ] `gofmt -l . && go vet ./... && go test ./...` clean
- [ ] Integration tests pass (`go test -tags=integration ./...`)
- [ ] Manual: `eidos summon` → pick prefab → mind-form comes up, `eidos forge logs <slug>` shows birth
- [ ] Manual: `eidos summon` → pick scratch → existing flow unaffected
- [ ] Manual on host without claude: `eidos summon` → pick prefab → succeeds; pick scratch → readable error from EnsureClaudeReady

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Design: `docs/superpowers/specs/2026-05-10-prefab-mindform-design.md`
EOF
)"
```

- [ ] **Step 3:** Watch CI.

```bash
gh pr checks --watch
```

If CI fails, fix locally → new commit → push.

- [ ] **Step 4:** Wait for Copilot's automated review (per project CLAUDE.md). Pull comments and address each.

```bash
gh pr view --comments
```

- [ ] **Step 5:** Print PR URL when done.

```bash
gh pr view --json url -q .url
```

---

## Self-review

- **Spec § 3 layout:** Tasks 1, 3, 11 cover top-level `template/`, `prefab/`, root `embed.go`.
- **Spec § 4 wizard flow:** Tasks 7, 8, 9, 10 cover Phase 2.5, Phase 3 prefab branch, Phase 4 skip, run.go wiring, readiness split.
- **Spec § 5 data model:** Task 2 (`Params`), Task 6 (`CreateOpts`), Task 7 (`Summoning.PrefabID`).
- **Spec § 6 low-level interface:** Tasks 4 + 5 (`List`, `MetaFor`, `TarStreamPrefab`).
- **Spec § 7 error handling:** missingkey=error in Task 5, malformed-toml-skipping in Task 4 (`List`), tar `CloseWithError` propagation already in place from existing `forge.Orchestrate`.
- **Spec § 8 testing:** Tasks 4 (List/MetaFor), 5 (round-trip), 7 (Phase2Half), 8 (Phase3Prefab), 9 (Phase4 skip).
- **Spec § 9 migration:** Task 1 does `git mv`; signatures unchanged.
- **Spec § 10 out of scope:** none of the deferred items appear as tasks.

**Placeholder scan:** No "TBD"/"TODO"/"implement later" inline. Task 9 step 4 references "the surrounding file" because the test harness is too long to inline; that's a pointer to the actual code (which is in the worktree), not a placeholder. Acceptable.

**Type consistency:** `Params` fields used in templates (`OwnerLabel`, `MindFormNpub`, `HomeRelay`) match Task 2's struct. `ScaffoldChoice` constants (`ScaffoldExit`, `ScaffoldScratch`, `ScaffoldPrefab`) are consistent across Tasks 7 and 10. `Meta` struct fields used by `Phase3Prefab` (`Display`, `Tagline`, `Preview`, `Kind`, `ID`) match Task 4's definition. `CreateOpts` fields (`PrefabID`, `OwnerLabel`, `MindFormNpub`) consistent across Tasks 6 and 9.
