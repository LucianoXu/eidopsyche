# Wizard decoupling + identity card — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the First Contact wizard along the mindgate / mindform layer boundary, and add a TOML identity-card format usable by humans and mind-forms.

**Architecture:** New `internal/card/` package owns the on-disk card format. The wizard's phase tree is rebuilt around an `EntryMode` enum (bare `eidos` / `eidos summon` / `eidos gate init`) and a master-source choice (local identity vs `--master-card`). Phase 1 grows three branches (新建 / 导入 / 跳过); a new Phase 2 chooses mindform action and master source; existing phases 2/3 rename to 3/4. CLI gains `eidos gate card export|show`.

**Tech Stack:** Go 1.22, TOML (`BurntSushi/toml`), cobra, existing `internal/identity` keypair helpers, existing `internal/firstcontact/render` Renderer interface.

**Spec:** `docs/superpowers/specs/2026-05-10-wizard-decoupling-and-card-design.md`

---

## File map

### Created

| File | Responsibility |
|---|---|
| `internal/card/card.go` | `Card` struct, Encode/Decode/Read/Write/Validate/Sample |
| `internal/card/card_test.go` | round-trip + validation rules |
| `internal/firstcontact/phase1_identity.go` | three-branch identity stage (replaces phase1_self.go) |
| `internal/firstcontact/phase1_identity_test.go` | branch dispatch + Bootstrap call assertions |
| `internal/firstcontact/phase2_choose.go` | mindform action + master source |
| `internal/firstcontact/phase2_choose_test.go` | option-matrix coverage |
| `cmd/eidos/gate/card.go` | `eidos gate card {export,show}` cobra subcommand |
| `cmd/eidos/gate/card_test.go` | export round-trips through `card.Read` |

### Renamed

| Old → New | Reason |
|---|---|
| `internal/firstcontact/phase1_self.go` → REMOVED (replaced by `phase1_identity.go`) | semantic restructure |
| `internal/firstcontact/phase2_book.go` → `phase3_book.go` | phase renumber |
| `internal/firstcontact/phase3_seal.go` → `phase4_seal.go` | phase renumber |
| `internal/firstcontact/phase3_seal_test.go` → `phase4_seal_test.go` | follows file |

### Modified

| File | What changes |
|---|---|
| `internal/firstcontact/run.go` | drive new phase tree; rename `IsSubsequentRun` → `IsIdentityInitialized`; rename `loadOperatorIntoSummoning` → `loadLocalMasterDefault`; thread `EntryMode` |
| `internal/firstcontact/summoning.go` | rename `OperatorLabel` → `MasterLabel`, `OperatorNpub` → `MasterNpub`; add `OperatorPresent bool` |
| `internal/firstcontact/run_test.go` | track renames |
| `internal/firstcontact/phase0_open.go` | append project-intro paragraph after language pick |
| `internal/firstcontact/phase3_seal.go` (now `phase4_seal.go`) | rename `Phase3` → `Phase4`; `RenderSummoningBook` reads `MasterLabel`/`MasterNpub` |
| `internal/firstcontact/phase2_book.go` (now `phase3_book.go`) | rename `Phase2` → `Phase3` |
| `internal/firstcontact/phase4_seal_test.go` | renamed; track `Master*` field rename |
| `internal/firstcontact/strings.go` | new keys: `phase0_intro_project` (replaces `phase0_intro`), three identity-branch keys, master-source choice keys, mindform-action choice keys, `gate_init_already_initialized` |
| `cmd/eidos/main.go` | use renamed `firstcontact.IsIdentityInitialized` |
| `cmd/eidos/summon/cmd.go` | pass `EntryMode = EntrySummon`; add `--master-card`, `--key-file` flags |
| `cmd/eidos/gate/init.go` | delegate to wizard with `EntryGateInit`; print "already initialized" when applicable |
| `docs/specs/FirstContact.md` | rewrite sections 一, 二, 六 against four-phase tree |
| `README.md` | one-line note on three deployment modes |

---

## Task 1 — `internal/card/` package

**Files:**
- Create: `internal/card/card.go`
- Create: `internal/card/card_test.go`

- [ ] **Step 1.1: Write the round-trip test**

```go
// internal/card/card_test.go
package card_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
)

func TestCard_RoundTrip(t *testing.T) {
	in := card.Card{
		SchemaVersion: 1,
		Label:         "Alice",
		PubkeyHex:     "5e9dabf301a0c0e8a8f1f9b50f6ce28c5cf26b09d6b4b9f0f9d1e2c3a4b5c6d7",
		Npub:          "npub1tn6dt7eqq6psw3g7fkjs7m8z3xtnxkzwkkjun7ru68zc6f9hsd6sk2y8nr",
		HomeRelay:     "wss://relay.damus.io",
		CreatedAt:     time.Date(2026, 5, 10, 12, 34, 56, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := card.Encode(&buf, in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := card.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Label != in.Label || out.PubkeyHex != in.PubkeyHex || out.Npub != in.Npub ||
		out.HomeRelay != in.HomeRelay || !out.CreatedAt.Equal(in.CreatedAt) ||
		out.SchemaVersion != in.SchemaVersion {
		t.Errorf("round-trip mismatch: in=%+v out=%+v", in, out)
	}
}
```

The hex/npub pair above is a **realistic-looking placeholder**; we don't validate against a real keypair in the round-trip test — that's the validation test's job. Use a hex string and an npub generated together by `internal/identity.Generate()` in a small one-shot helper if you want a real pair, or accept that round-trip just checks the field shuttle.

- [ ] **Step 1.2: Run test to verify FAIL**

```
cd /data/eidopsyche
go test ./internal/card/...
```

Expected: package not found / undefined symbols.

- [ ] **Step 1.3: Implement `Card` struct + Encode/Decode**

```go
// internal/card/card.go
package card

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// Card is an eidopsyche identity card — a self-contained, public,
// human-readable introduction file. Schema v1.
type Card struct {
	SchemaVersion int       `toml:"schema_version"`
	Label         string    `toml:"label"`
	PubkeyHex     string    `toml:"pubkey_hex"`
	Npub          string    `toml:"npub"`
	HomeRelay     string    `toml:"home_relay"`
	CreatedAt     time.Time `toml:"created_at"`
}

// Encode writes c as TOML to w. Caller is responsible for any preceding
// validation; Encode itself does not validate.
func Encode(w io.Writer, c Card) error {
	return toml.NewEncoder(w).Encode(c)
}

// Decode reads a Card from r and validates it. Returns the validation
// error if the file is malformed or fails any v1 invariant.
func Decode(r io.Reader) (Card, error) {
	var c Card
	if _, err := toml.NewDecoder(r).Decode(&c); err != nil {
		return Card{}, fmt.Errorf("decode card: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Card{}, err
	}
	return c, nil
}

// Read loads a Card from path with Decode's validation.
func Read(path string) (Card, error) {
	f, err := os.Open(path)
	if err != nil {
		return Card{}, err
	}
	defer f.Close()
	return Decode(f)
}

// Write encodes c to path with mode 0o600. mkdirs the parent.
func Write(path string, c Card) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return Encode(f, c)
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate enforces all v1 invariants. Decode calls this; manual callers
// (e.g. exporters constructing a Card from gate state) should also call
// it before Write.
func (c Card) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schema_version: want 1, got %d", c.SchemaVersion)
	}
	if c.Label == "" {
		return errors.New("label: must not be empty")
	}
	if !hex64.MatchString(c.PubkeyHex) {
		return errors.New("pubkey_hex: must be 64 lowercase hex chars")
	}
	if c.Npub == "" {
		return errors.New("npub: must not be empty")
	}
	decoded, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return fmt.Errorf("npub: %w", err)
	}
	if decoded != c.PubkeyHex {
		return fmt.Errorf("npub does not decode to pubkey_hex (got %q, want %q)", decoded, c.PubkeyHex)
	}
	if c.HomeRelay == "" {
		return errors.New("home_relay: must not be empty")
	}
	u, err := url.Parse(c.HomeRelay)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fmt.Errorf("home_relay: must be ws:// or wss:// URL with host (got %q)", c.HomeRelay)
	}
	if c.CreatedAt.IsZero() {
		return errors.New("created_at: must not be zero")
	}
	return nil
}

// Sample returns a representative Card useful in help text and docs.
// Pubkey is a known test fixture pair (hex/npub match).
func Sample() Card {
	return Card{
		SchemaVersion: 1,
		Label:         "Alice",
		PubkeyHex:     "0000000000000000000000000000000000000000000000000000000000000001",
		Npub:          "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqsm6sw60",
		HomeRelay:     "wss://relay.damus.io",
		CreatedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}
```

Verify the Sample's hex / npub pair actually round-trips through `identity.DecodeNpub`. If `DecodeNpub` does not exist with that signature, search `internal/identity` for the npub-to-hex helper and use the right name; commonly it is `identity.DecodeNpub(string) (string, error)`. **If the npub fixture in `Sample()` doesn't validate, generate a fresh pair via `identity.Generate()` in a one-shot `go run` script and paste the resulting hex+npub into the literal — the Sample MUST pass `Validate()` because Task 1 Step 1.7 below tests it.**

- [ ] **Step 1.4: Run round-trip test, expect PASS**

```
go test ./internal/card/... -run TestCard_RoundTrip -v
```

Expected: PASS.

- [ ] **Step 1.5: Add validation tests**

Append to `internal/card/card_test.go`:

```go
func TestCard_Validate_Rejects(t *testing.T) {
	good := card.Sample()
	cases := []struct {
		name  string
		mut   func(*card.Card)
		want  string
	}{
		{"unknown schema_version", func(c *card.Card) { c.SchemaVersion = 2 }, "schema_version"},
		{"empty label", func(c *card.Card) { c.Label = "" }, "label"},
		{"short hex", func(c *card.Card) { c.PubkeyHex = "abc" }, "pubkey_hex"},
		{"upper-case hex", func(c *card.Card) { c.PubkeyHex = "ABCDEF" + good.PubkeyHex[6:] }, "pubkey_hex"},
		{"empty npub", func(c *card.Card) { c.Npub = "" }, "npub"},
		{"npub mismatch", func(c *card.Card) { c.PubkeyHex = "1111111111111111111111111111111111111111111111111111111111111111" }, "npub"},
		{"http relay", func(c *card.Card) { c.HomeRelay = "http://relay.example.com" }, "home_relay"},
		{"empty relay", func(c *card.Card) { c.HomeRelay = "" }, "home_relay"},
		{"zero created_at", func(c *card.Card) { c.CreatedAt = time.Time{} }, "created_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted invalid card: %+v", c)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestCard_Sample_Validates(t *testing.T) {
	if err := card.Sample().Validate(); err != nil {
		t.Errorf("Sample failed Validate: %v", err)
	}
}

func TestCard_Read_RejectsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.eidos-card.toml")
	body := `schema_version = 1
label = "X"
pubkey_hex = "not hex"
npub = ""
home_relay = "wss://r/"
created_at = 2026-01-01T00:00:00Z
`
	if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := card.Read(bad); err == nil {
		t.Errorf("Read should reject malformed card")
	}
}

func TestCard_WriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alice.eidos-card.toml")
	in := card.Sample()
	if err := card.Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := card.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Label != in.Label {
		t.Errorf("label = %q, want %q", out.Label, in.Label)
	}
}
```

Add imports for `os`, `path/filepath`, `strings`.

- [ ] **Step 1.6: Run all card tests, expect PASS**

```
go test ./internal/card/... -v
```

Expected: every test passes.

- [ ] **Step 1.7: Commit**

```
git add internal/card/
git commit -m "feat(card): add v1 identity card format and internal/card package"
```

---

## Task 2 — Wizard data model: predicate rename, Master* fields, EntryMode

**Files:**
- Modify: `internal/firstcontact/run.go`
- Modify: `internal/firstcontact/summoning.go`
- Modify: `internal/firstcontact/run_test.go`
- Modify: `internal/firstcontact/phase1_self.go` (transient — replaced in Task 4)
- Modify: `internal/firstcontact/phase3_seal.go` (renames OperatorLabel/OperatorNpub references)
- Modify: `internal/firstcontact/phase3_seal_test.go`
- Modify: `cmd/eidos/main.go`
- Modify: `cmd/eidos/summon/cmd.go` (transient — fully refactored in Task 9)

### Step 2.1: Rename Summoning fields and add OperatorPresent

- [ ] Edit `internal/firstcontact/summoning.go`:

```go
type Summoning struct {
	Lang            string
	OperatorPresent bool   // true iff Phase 1 created or imported a local identity
	MasterLabel     string // burned into journal/0000-summoning.md as "签者"
	MasterNpub      string // mind-form's owner; goes into forge.CreateOpts.Owner
	HomeRelay       string // master's home relay; doubles as the mind-form's home relay
	CharacterPrompt string
	Profile         CharacterProfile
	Displaying      string
	SummonedName    string
	Slug            string
	MindFormNpub    string
	MindFormKeyHex  string
	CallingWords    string
	StartedAt       time.Time
	Subsequent      bool
}
```

### Step 2.2: Add EntryMode + Deps fields

- [ ] In `internal/firstcontact/run.go`, add at top of file (after the package doc comment):

```go
// EntryMode tells the wizard which CLI surface invoked it. Phase 2's
// option set varies by entry mode (see spec § 4.2).
type EntryMode int

const (
	EntryBareEidos EntryMode = iota // bare `eidos` auto-dispatch
	EntrySummon                     // `eidos summon`
	EntryGateInit                   // `eidos gate init`
)
```

Extend `Deps`:

```go
type Deps struct {
	StateDir       string
	Renderer       render.Renderer
	Claude         *Claude
	ReadyDeps      ReadyDeps
	DockerClient   forgectl.Client
	Image          string
	WriteVolume    VolumeWriter
	StartContainer func(ctx context.Context, slug string) error
	ResponseWait   ResponseWaiter
	AddContact     ContactAdder
	ExistingSlugs  func() ([]string, error)

	EntryMode       EntryMode
	MasterCardPath  string // empty unless --master-card given
	OperatorKeyPath string // empty unless --key-file given for identity import
}
```

### Step 2.3: Rename predicate

- [ ] In `internal/firstcontact/run.go`, rename `IsSubsequentRun` → `IsIdentityInitialized`. The exported function and the internal helper both rename. Keep the (bool, error) signatures.

```go
// IsIdentityInitialized reports whether <stateDir>/key and
// <stateDir>/state.db both exist. This is the single predicate
// consulted by both cmd/eidos/main.go's auto-dispatch and the
// wizard's own subsequent-mode branch — they cannot drift.
func IsIdentityInitialized(stateDir string) bool {
	v, _ := isIdentityInitialized(stateDir)
	return v
}

func isIdentityInitialized(stateDir string) (bool, error) { /* unchanged body */ }
```

Update all call sites in `run.go` (`subsequent, err := isIdentityInitialized(...)`).

### Step 2.4: Rename loadOperatorIntoSummoning

- [ ] In `internal/firstcontact/run.go`, rename `loadOperatorIntoSummoning` → `loadLocalMasterDefault`. Body remains identical except for the field rename:

```go
func loadLocalMasterDefault(stateDir string, s *Summoning) error {
	k, err := identity.LoadKey(filepath.Join(stateDir, "key"))
	if err != nil {
		return fmt.Errorf("load operator key: %w", err)
	}
	s.MasterNpub = k.Npub
	s.OperatorPresent = true
	// ... open state.db, read label + own_relays.home unchanged ...
	s.MasterLabel = label
	s.HomeRelay = home
	s.Lang = "zh"
	return nil
}
```

### Step 2.5: Update phase 1 (transient) and phase 3-seal references

- [ ] In `internal/firstcontact/phase1_self.go`, change the Bootstrap call's surrounding code:

```go
s.MasterLabel = label
// ... after Bootstrap success:
s.MasterNpub = npub
s.OperatorPresent = true
```

(This file is replaced wholesale in Task 4; the change here is just to keep `go build` green between Task 2 and Task 4.)

- [ ] In `internal/firstcontact/phase3_seal.go::RenderSummoningBook`, change `s.OperatorLabel` → `s.MasterLabel`, `s.OperatorNpub` → `s.MasterNpub`. Comment update:

```go
// RenderSummoningBook produces the markdown preview shown at 封缄. The
// "signer" is the new mind-form's master — local operator if Phase 2 was
// "use local identity", or the card holder if Phase 2 was "use a card".
```

- [ ] In `internal/firstcontact/phase3_seal.go::Phase3`, no changes needed — `s.MasterNpub`, `s.HomeRelay` thread through `forge.CreateOpts` exactly as before (just renamed source fields).

### Step 2.6: Update tests

- [ ] In `internal/firstcontact/run_test.go`:
  - rename `TestLoadOperatorIntoSummoning_*` → `TestLoadLocalMasterDefault_*`
  - rename references: `s.OperatorNpub` → `s.MasterNpub`, `s.OperatorLabel` → `s.MasterLabel`
  - rename `TestIsSubsequentRun_PathDetection` → `TestIsIdentityInitialized_PathDetection`; update internal calls.

- [ ] In `internal/firstcontact/phase3_seal_test.go`: rename struct literal fields `OperatorLabel:` → `MasterLabel:`, `OperatorNpub:` → `MasterNpub:`.

### Step 2.7: Update cmd/eidos/main.go

- [ ] In `cmd/eidos/main.go`, `shouldDispatchToWizard` body:

```go
return !firstcontact.IsIdentityInitialized(dir)
```

### Step 2.8: Run + commit

- [ ] **Build + test:**

```
cd /data/eidopsyche
go build ./...
go vet ./...
go test ./internal/firstcontact/... ./cmd/eidos/...
```

Expected: all green.

- [ ] **Commit:**

```
git add internal/firstcontact/ cmd/eidos/main.go
git commit -m "refactor(firstcontact): rename Operator* → Master*, add EntryMode

Master is the new mind-form's owner — the local operator in the
common path, the card holder when Phase 2 picks a card. The
field-rename clarifies the difference. Predicate IsSubsequentRun
becomes IsIdentityInitialized: less ambiguous and the only
predicate consulted by main.go's auto-dispatch and the wizard's
own subsequent branch.

Adds EntryMode enum + Deps fields (MasterCardPath, OperatorKeyPath)
threading through unchanged in this commit; wired in Tasks 9-10."
```

---

## Task 3 — Phase 0 project intro

**Files:**
- Modify: `internal/firstcontact/phase0_open.go`
- Modify: `internal/firstcontact/strings.go`

- [ ] **Step 3.1: Update strings.go**

In the `zh` table, replace the existing `phase0_intro` entry with:

```go
"phase0_intro": `Eidopsyche 是一个数字生命的社交网络框架。它有两层：

  · MindGate  — 你和别人、和心智体之间的通信
  · MindForm  — 一个由你召唤的心智体，它有自己的内在生活

接下来 wizard 会问你两件事：
  1. 要不要一个本地身份？可以新建、可以导入、可以跳过
  2. 要不要现在召唤一个心智体？

  · 只想跟别人说话：第一题选"新建"，第二题选"退出"
  · 只想给朋友跑一个心智体：第一题选"跳过"，第二题给朋友的名片
  · 两者都要：默认路径
`,
```

In the `en` table:

```go
"phase0_intro": `Eidopsyche is a social-network framework for digital lives. It has two layers:

  · MindGate  — communication between you and other people / mind-forms
  · MindForm  — a mind-form you summon, with its own inner life

The wizard will ask you two things:
  1. Do you want a local identity? You can create one, import one, or skip
  2. Do you want to summon a mind-form right now?

  · Just want to talk to others: pick "create" then "exit"
  · Just want to host a mind-form for a friend: pick "skip" then give their card
  · Both: the default path
`,
```

- [ ] **Step 3.2: Phase0 unchanged signature; just confirm the Show call uses the new key**

`phase0_open.go` already does `r.Show(stringFor(s.Lang, "phase0_intro"))` — no code change. The intro content swap happens entirely via strings.go.

- [ ] **Step 3.3: Build**

```
go build ./...
```

Expected: green.

- [ ] **Step 3.4: Commit**

```
git add internal/firstcontact/strings.go
git commit -m "feat(firstcontact): replace phase 0 intro with project orientation

The old intro talked only about 'writing a summoning book' — confusing
to first-time users who don't yet know mindgate from mindform. The
new intro briefly names both layers and previews the two questions
the wizard is about to ask, including the three deployment paths
(mindgate-only / mindform-only / both)."
```

---

## Task 4 — Phase 1 identity stage (three branches)

**Files:**
- Delete: `internal/firstcontact/phase1_self.go`
- Create: `internal/firstcontact/phase1_identity.go`
- Create: `internal/firstcontact/phase1_identity_test.go`
- Modify: `internal/firstcontact/strings.go`
- Modify: `internal/firstcontact/run.go` (call site)

### Step 4.1: Add new strings keys

- [ ] In both `zh` and `en` tables of `strings.go`, add (after `phase1_relay_*` entries):

zh:
```go
"phase1_choose_q":           "本地身份：怎么办？",
"phase1_choose_create":      "新建一个身份",
"phase1_choose_import":      "导入已有身份",
"phase1_choose_skip":        "跳过（只跑一个心智体）",
"phase1_import_nsec_q":      "粘贴你的私钥（nsec1... 或 32 字节十六进制）：",
"phase1_import_nsec_invalid": "  (这看起来不像一个 nsec / hex 私钥，重新输入)",
"phase1_import_card_q":      "（可选）你的名片文件路径——回车跳过：",
"phase1_import_card_invalid": "  (名片读取失败：%s)",
"phase1_skip_note":          "好。下面要召唤心智体的话，请准备好对方的名片。",
```

en:
```go
"phase1_choose_q":           "Local identity: what would you like to do?",
"phase1_choose_create":      "Create a new identity",
"phase1_choose_import":      "Import an existing one",
"phase1_choose_skip":        "Skip (mind-form only)",
"phase1_import_nsec_q":      "Paste your private key (nsec1... or 32-byte hex):",
"phase1_import_nsec_invalid": "  (that doesn't look like a nsec / hex private key — try again)",
"phase1_import_card_q":      "(optional) path to your card file — Enter to skip:",
"phase1_import_card_invalid": "  (could not read card: %s)",
"phase1_skip_note":          "OK. To summon a mind-form below, you'll need its master's card.",
```

### Step 4.2: Implement phase1_identity.go

- [ ] **Write the test first (Step 4.2a):**

```go
// internal/firstcontact/phase1_identity_test.go
package firstcontact

import (
	"context"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// fakeRenderer implements render.Renderer with scripted responses.
// Tests pre-populate prompts/choices in order; methods pop one per call.
type fakeRenderer struct {
	render.Renderer // embed nil; tests override only the calls they hit
	choices []int
	prompts []string
	shows   []string
}

func (f *fakeRenderer) PromptChoice(_ string, _ []render.ChoiceOption) (int, error) {
	if len(f.choices) == 0 {
		panic("fakeRenderer: no choice queued")
	}
	c := f.choices[0]
	f.choices = f.choices[1:]
	return c, nil
}
func (f *fakeRenderer) Prompt(_ string, _ render.PromptOpts) (string, error) {
	if len(f.prompts) == 0 {
		panic("fakeRenderer: no prompt queued")
	}
	p := f.prompts[0]
	f.prompts = f.prompts[1:]
	return p, nil
}
func (f *fakeRenderer) Show(s string)         { f.shows = append(f.shows, s) }
func (f *fakeRenderer) Capabilities() render.Capabilities { return render.Capabilities{} }

func TestPhase1_Skip_LeavesNoStateAndNoOperator(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRenderer{choices: []int{2}} // 2 = skip
	s := &Summoning{Lang: "zh"}
	if err := Phase1(context.Background(), s, r, Phase1Deps{StateDir: dir}); err != nil {
		t.Fatalf("Phase1 skip: %v", err)
	}
	if s.OperatorPresent {
		t.Errorf("OperatorPresent should be false after skip")
	}
	if s.MasterNpub != "" {
		t.Errorf("MasterNpub should be empty, got %q", s.MasterNpub)
	}
}
```

(More branch tests — create + import — can be added but require stubbing `identity.Bootstrap` / `identity.BootstrapWithExistingKey`. For TDD-first, the skip path is the cleanest test of the new dispatcher; the create branch is exercised through `TestIsIdentityInitialized` after a real Bootstrap call from another test, and the import branch is exercised in Task 9's E2E flow.)

- [ ] **Step 4.2b: Run test, expect FAIL** (Phase1 signature changed; test won't compile yet)

```
go test ./internal/firstcontact/ -run TestPhase1_Skip
```

Expected: FAIL on compile.

- [ ] **Step 4.2c: Implement phase1_identity.go:**

```go
// internal/firstcontact/phase1_identity.go
package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// Phase1Deps are inputs Phase 1 cannot derive from Summoning.
type Phase1Deps struct {
	StateDir string

	// Optional pre-supplied inputs from CLI flags (Deps.OperatorKeyPath /
	// Deps.MasterCardPath). When non-empty, Phase 1's import branch
	// reads these directly instead of prompting.
	OperatorKeyPath string
	OptionalCardPath string
}

// ErrSelfHostExit kept for backward compatibility — not produced by the
// new dispatcher but other tests still reference it.
var ErrSelfHostExit = errors.New("self-host relay chosen — wizard exiting cleanly")

// Phase1 asks the operator to choose: create a new identity, import an
// existing one, or skip altogether. Mutates s in place. On "skip", no
// disk state is written; s.OperatorPresent stays false. On "create" or
// "import" success, s.MasterLabel / s.MasterNpub / s.HomeRelay are
// populated and s.OperatorPresent is set true.
func Phase1(ctx context.Context, s *Summoning, r render.Renderer, d Phase1Deps) error {
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase1_choose_q"), []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase1_choose_create")},
		{Label: stringFor(s.Lang, "phase1_choose_import")},
		{Label: stringFor(s.Lang, "phase1_choose_skip")},
	})
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		return phase1Create(ctx, s, r, d)
	case 1:
		return phase1Import(ctx, s, r, d)
	case 2:
		r.Show(stringFor(s.Lang, "phase1_skip_note"))
		return nil
	}
	return fmt.Errorf("phase1: unknown choice index %d", idx)
}

func phase1Create(ctx context.Context, s *Summoning, r render.Renderer, d Phase1Deps) error {
	label, err := r.Prompt(stringFor(s.Lang, "phase1_label_q"), render.PromptOpts{
		HelpText: stringFor(s.Lang, "phase1_label_help"),
	})
	if err != nil {
		return err
	}
	homeRelay, err := promptHomeRelay(s, r)
	if err != nil {
		return err
	}
	npub, err := identity.Bootstrap(d.StateDir, label, homeRelay)
	if err != nil {
		return fmt.Errorf("bootstrap operator identity: %w", err)
	}
	s.MasterLabel = label
	s.MasterNpub = npub
	s.HomeRelay = homeRelay
	s.OperatorPresent = true
	return nil
}

func phase1Import(ctx context.Context, s *Summoning, r render.Renderer, d Phase1Deps) error {
	// 1) get the nsec / hex
	hexKey, err := readImportKey(s, r, d)
	if err != nil {
		return err
	}
	// 2) optionally consume a card for label/relay defaults
	var defaultLabel, defaultRelay string
	if c, ok := readImportCard(s, r, d); ok {
		defaultLabel = c.Label
		defaultRelay = c.HomeRelay
	}
	// 3) prompt label / home_relay (with defaults if available)
	label, err := r.Prompt(stringFor(s.Lang, "phase1_label_q"),
		render.PromptOpts{HelpText: defaultLabel, AllowEmpty: defaultLabel != ""})
	if err != nil {
		return err
	}
	if label == "" {
		label = defaultLabel
	}
	homeRelay, err := promptHomeRelayWithDefault(s, r, defaultRelay)
	if err != nil {
		return err
	}
	// 4) write the key to disk and bootstrap with existing
	keyPath := filepath.Join(d.StateDir, "key")
	if err := os.MkdirAll(d.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(hexKey+"\n"), 0o600); err != nil {
		return err
	}
	npub, err := identity.BootstrapWithExistingKey(d.StateDir, label, homeRelay)
	if err != nil {
		return fmt.Errorf("bootstrap with existing key: %w", err)
	}
	s.MasterLabel = label
	s.MasterNpub = npub
	s.HomeRelay = homeRelay
	s.OperatorPresent = true
	return nil
}

// readImportKey returns a 64-char hex private key. Source preference:
// (1) Deps.OperatorKeyPath if set; (2) interactive paste.
func readImportKey(s *Summoning, r render.Renderer, d Phase1Deps) (string, error) {
	if d.OperatorKeyPath != "" {
		body, err := os.ReadFile(d.OperatorKeyPath)
		if err != nil {
			return "", fmt.Errorf("read --key-file: %w", err)
		}
		return parsePrivateKey(strings.TrimSpace(string(body)))
	}
	for {
		input, err := r.Prompt(stringFor(s.Lang, "phase1_import_nsec_q"), render.PromptOpts{})
		if err != nil {
			return "", err
		}
		hex, err := parsePrivateKey(strings.TrimSpace(input))
		if err == nil {
			return hex, nil
		}
		r.Show(stringFor(s.Lang, "phase1_import_nsec_invalid"))
	}
}

// parsePrivateKey accepts an nsec1... NIP-19 string OR a 64-char hex
// string. Returns canonical lowercase hex. Returns error on neither.
func parsePrivateKey(input string) (string, error) {
	// Try nsec1...
	if strings.HasPrefix(input, "nsec1") {
		hex, err := identity.DecodeNsec(input)
		if err != nil {
			return "", err
		}
		return hex, nil
	}
	// Try 64-char hex
	if hex64.MatchString(input) {
		return strings.ToLower(input), nil
	}
	return "", errors.New("input is neither nsec1... nor 64-char hex")
}

// readImportCard tries to load a card file from Deps or interactive
// path. Returns (card, true) on success, (zero, false) when skipped or
// unreadable (no fatal error — card is optional).
func readImportCard(s *Summoning, r render.Renderer, d Phase1Deps) (card.Card, bool) {
	if d.OptionalCardPath != "" {
		c, err := card.Read(d.OptionalCardPath)
		if err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase1_import_card_invalid"), err))
			return card.Card{}, false
		}
		return c, true
	}
	path, _ := r.Prompt(stringFor(s.Lang, "phase1_import_card_q"), render.PromptOpts{AllowEmpty: true})
	if path == "" {
		return card.Card{}, false
	}
	c, err := card.Read(path)
	if err != nil {
		r.Show(fmt.Sprintf(stringFor(s.Lang, "phase1_import_card_invalid"), err))
		return card.Card{}, false
	}
	return c, true
}

func promptHomeRelay(s *Summoning, r render.Renderer) (string, error) {
	return promptHomeRelayWithDefault(s, r, "")
}

// promptHomeRelayWithDefault preserves the existing public/custom-URL
// 2-choice menu (the self-host branch is dropped — out of scope per
// spec, and was never wired through to a real sub-wizard). When dflt
// is non-empty, it appears as a third option "Use default: <dflt>".
func promptHomeRelayWithDefault(s *Summoning, r render.Renderer, dflt string) (string, error) {
	choices := []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase1_relay_public"), Hint: PublicHomeRelay},
		{Label: stringFor(s.Lang, "phase1_relay_custom")},
	}
	if dflt != "" {
		choices = append([]render.ChoiceOption{{Label: dflt, Hint: "from card"}}, choices...)
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase1_relay_q"), choices)
	if err != nil {
		return "", err
	}
	if dflt != "" {
		idx-- // shift back: 0=default, 1=public, 2=custom
	}
	switch idx {
	case -1:
		return dflt, nil
	case 0:
		return PublicHomeRelay, nil
	case 1:
		for {
			candidate, err := r.Prompt(stringFor(s.Lang, "phase1_relay_custom_q"), render.PromptOpts{})
			if err != nil {
				return "", err
			}
			if u, perr := url.Parse(candidate); perr == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Host != "" {
				return candidate, nil
			}
			r.Show(stringFor(s.Lang, "phase1_relay_custom_invalid"))
		}
	}
	return "", fmt.Errorf("phase1 relay: unknown choice index %d", idx)
}
```

Add the missing imports: `os`, `path/filepath`, `regexp` (`hex64` is already in `internal/card`; keep a local copy here OR export `card.Hex64Regexp`. Simplest: copy the regex literal here as `var hex64 = regexp.MustCompile(\`^[0-9a-f]{64}$\`)`).

**On `identity.DecodeNsec`**: search `internal/identity` for the function. If only `DecodeNpub` exists, also add `DecodeNsec` in `internal/identity/keystore.go` mirroring the npub helper:

```go
// DecodeNsec converts a NIP-19 nsec... bech32 string to its 32-byte hex.
func DecodeNsec(nsec string) (string, error) {
	prefix, data, err := nip19.Decode(nsec)
	if err != nil {
		return "", fmt.Errorf("decode nsec: %w", err)
	}
	if prefix != "nsec" {
		return "", fmt.Errorf("expected nsec prefix, got %q", prefix)
	}
	hex, ok := data.(string)
	if !ok {
		return "", fmt.Errorf("nsec payload is %T, want string", data)
	}
	return hex, nil
}
```

(Verify the `nip19` package's `Decode` API matches this shape; nbd-wtf/go-nostr's `nip19.Decode` returns `(string, any, error)` with `any` being the typed payload — `string` for `nsec` / `npub`.)

- [ ] **Step 4.2d: Delete phase1_self.go**

```
git rm internal/firstcontact/phase1_self.go
```

- [ ] **Step 4.2e: Run skip-branch test, expect PASS**

```
go test ./internal/firstcontact/ -run TestPhase1_Skip -v
```

Expected: PASS.

- [ ] **Step 4.2f: Build**

```
go build ./...
```

Expected: green. **If unresolved imports, fix them before continuing.**

- [ ] **Step 4.3: Commit**

```
git add internal/firstcontact/ internal/identity/
git commit -m "feat(firstcontact): three-branch identity stage (create / import / skip)

Replaces phase1_self.go's single 'create' flow. Import accepts a
nsec1... or 64-char hex private key, optionally with an .eidos-card.toml
to default label and home_relay. Skip writes no disk state, leaves
OperatorPresent=false; Phase 2 will then require a master card."
```

---

## Task 5 — Phase 2 mindform choice

**Files:**
- Create: `internal/firstcontact/phase2_choose.go`
- Create: `internal/firstcontact/phase2_choose_test.go`
- Modify: `internal/firstcontact/strings.go`

### Step 5.1: New strings keys

- [ ] In `strings.go`, both tables, append:

zh:
```go
"phase2_action_q":         "心智体阶段：怎么办？",
"phase2_action_exit":      "退出",
"phase2_action_local":     "用本地身份召唤一个心智体",
"phase2_action_card":      "用一张名片召唤一个心智体",
"phase2_card_path_q":      "请提供主人的名片路径：",
"phase2_card_invalid":     "  (名片读取失败：%s — 重新输入)",
"phase2_card_required":    "本地没有身份；要召唤心智体，请提供一张名片作为主人。",
```

en:
```go
"phase2_action_q":         "Mind-form stage: what would you like to do?",
"phase2_action_exit":      "Exit",
"phase2_action_local":     "Summon a mind-form using your local identity",
"phase2_action_card":      "Summon a mind-form using a card",
"phase2_card_path_q":      "Path to the master's card:",
"phase2_card_invalid":     "  (could not read card: %s — try again)",
"phase2_card_required":    "No local identity; summoning a mind-form requires a card as master.",
```

### Step 5.2: Implement phase2_choose.go

- [ ] **Write tests first:**

```go
// internal/firstcontact/phase2_choose_test.go
package firstcontact

import (
	"context"
	"testing"
)

// Phase2Action is the result of Phase 2: what the wizard does next.
type _ = Phase2Action // tells the reader the type lives in phase2_choose.go

func TestPhase2_BareEidosWithLocal_ThreeOptions(t *testing.T) {
	r := &fakeRenderer{choices: []int{0}} // 0 = exit
	s := &Summoning{Lang: "zh", OperatorPresent: true}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{EntryMode: EntryBareEidos})
	if err != nil {
		t.Fatalf("Phase2: %v", err)
	}
	if got != Phase2Exit {
		t.Errorf("got %v, want Phase2Exit", got)
	}
}

func TestPhase2_SummonWithLocal_NoExitOption(t *testing.T) {
	r := &fakeRenderer{choices: []int{0}} // 0 = local (no exit prepended)
	s := &Summoning{Lang: "zh", OperatorPresent: true, MasterNpub: "npubX", MasterLabel: "alice", HomeRelay: "wss://r"}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{EntryMode: EntrySummon})
	if err != nil {
		t.Fatalf("Phase2: %v", err)
	}
	if got != Phase2SummonLocal {
		t.Errorf("got %v, want Phase2SummonLocal", got)
	}
}

func TestPhase2_FlagWins_NoPrompt(t *testing.T) {
	dir := t.TempDir()
	cardPath := filepath.Join(dir, "x.eidos-card.toml")
	if err := card.Write(cardPath, card.Sample()); err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{} // no prompts queued — must not be called
	s := &Summoning{Lang: "zh", OperatorPresent: true}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{
		EntryMode:      EntryBareEidos,
		MasterCardPath: cardPath,
	})
	if err != nil {
		t.Fatalf("Phase2 with flag: %v", err)
	}
	if got != Phase2SummonCard {
		t.Errorf("got %v, want Phase2SummonCard", got)
	}
	if s.MasterNpub == "" {
		t.Errorf("MasterNpub should be populated from card")
	}
}
```

Add imports: `path/filepath`, `github.com/LucianoXu/eidopsyche/internal/card`.

- [ ] **Run, expect FAIL** (compile error: undefined types)

- [ ] **Implement:**

```go
// internal/firstcontact/phase2_choose.go
package firstcontact

import (
	"context"
	"errors"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// Phase2Action is the chosen mindform-stage action.
type Phase2Action int

const (
	Phase2Exit        Phase2Action = iota // wizard ends here
	Phase2SummonLocal                     // summon with local identity as master
	Phase2SummonCard                      // summon with card holder as master
)

// Phase2Deps are the inputs Phase 2 cannot derive from Summoning.
type Phase2Deps struct {
	EntryMode      EntryMode
	MasterCardPath string // empty unless --master-card was given
}

// Phase2 asks the operator what to do at the mindform stage. The
// option set is computed from EntryMode and s.OperatorPresent (spec
// § 4.2). When --master-card is supplied via deps, Phase 2 skips the
// menu and returns Phase2SummonCard.
//
// On Phase2SummonCard, this function ALSO loads the card and overwrites
// s.MasterLabel / s.MasterNpub / s.HomeRelay accordingly (per spec
// § 4.4.2: mind-form's home_relay = master's home_relay).
func Phase2(ctx context.Context, s *Summoning, r render.Renderer, d Phase2Deps) (Phase2Action, error) {
	// 1) Flag short-circuits the menu entirely.
	if d.MasterCardPath != "" {
		if err := loadCardIntoSummoning(s, d.MasterCardPath); err != nil {
			return 0, err
		}
		return Phase2SummonCard, nil
	}

	// 2) Compute available options.
	type opt struct {
		label  string
		action Phase2Action
	}
	var opts []opt
	if d.EntryMode != EntrySummon {
		opts = append(opts, opt{stringFor(s.Lang, "phase2_action_exit"), Phase2Exit})
	}
	if s.OperatorPresent {
		opts = append(opts, opt{stringFor(s.Lang, "phase2_action_local"), Phase2SummonLocal})
	}
	opts = append(opts, opt{stringFor(s.Lang, "phase2_action_card"), Phase2SummonCard})

	// 3) Special case: only a single option (eidos summon, no local id).
	// Skip the menu and require a card path now.
	if len(opts) == 1 && opts[0].action == Phase2SummonCard {
		r.Show(stringFor(s.Lang, "phase2_card_required"))
		path, err := promptCardPath(s, r)
		if err != nil {
			return 0, err
		}
		if err := loadCardIntoSummoning(s, path); err != nil {
			return 0, err
		}
		return Phase2SummonCard, nil
	}

	// 4) Render menu.
	choices := make([]render.ChoiceOption, 0, len(opts))
	for _, o := range opts {
		choices = append(choices, render.ChoiceOption{Label: o.label})
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase2_action_q"), choices)
	if err != nil {
		return 0, err
	}
	if idx < 0 || idx >= len(opts) {
		return 0, fmt.Errorf("phase2: choice index %d out of range", idx)
	}
	chosen := opts[idx].action

	// 5) If card chosen, prompt for path and load.
	if chosen == Phase2SummonCard {
		path, err := promptCardPath(s, r)
		if err != nil {
			return 0, err
		}
		if err := loadCardIntoSummoning(s, path); err != nil {
			return 0, err
		}
	}
	return chosen, nil
}

func promptCardPath(s *Summoning, r render.Renderer) (string, error) {
	for {
		path, err := r.Prompt(stringFor(s.Lang, "phase2_card_path_q"), render.PromptOpts{})
		if err != nil {
			return "", err
		}
		if path == "" {
			r.Show(stringFor(s.Lang, "phase2_card_invalid"))
			continue
		}
		if _, err := card.Read(path); err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), err))
			continue
		}
		return path, nil
	}
}

func loadCardIntoSummoning(s *Summoning, path string) error {
	c, err := card.Read(path)
	if err != nil {
		return fmt.Errorf("load card %q: %w", path, err)
	}
	s.MasterLabel = c.Label
	s.MasterNpub = c.Npub
	s.HomeRelay = c.HomeRelay
	return nil
}

var _ = errors.New // keep imports stable for follow-on edits
```

- [ ] **Run tests, expect PASS:**

```
go test ./internal/firstcontact/ -run 'TestPhase2_' -v
```

- [ ] **Step 5.3: Commit**

```
git add internal/firstcontact/
git commit -m "feat(firstcontact): add phase 2 mindform-stage choice

Phase 2 chooses between exit / summon-with-local / summon-with-card.
Option set is computed from EntryMode and OperatorPresent. When
Deps.MasterCardPath is set (--master-card flag), Phase 2 skips the
menu and uses the card directly. Card-source paths overwrite
MasterLabel / MasterNpub / HomeRelay so the mind-form's home relay
follows its master (spec § 4.4.2)."
```

---

## Task 6 — Rename phases 2/3 → 3/4

**Files:**
- Rename: `internal/firstcontact/phase2_book.go` → `internal/firstcontact/phase3_book.go`
- Rename: `internal/firstcontact/phase3_seal.go` → `internal/firstcontact/phase4_seal.go`
- Rename: `internal/firstcontact/phase3_seal_test.go` → `internal/firstcontact/phase4_seal_test.go`

### Step 6.1: Git-rename the files

- [ ] Run:

```
git mv internal/firstcontact/phase2_book.go internal/firstcontact/phase3_book.go
git mv internal/firstcontact/phase3_seal.go internal/firstcontact/phase4_seal.go
git mv internal/firstcontact/phase3_seal_test.go internal/firstcontact/phase4_seal_test.go
```

### Step 6.2: Rename the exported phase functions

- [ ] In `phase3_book.go`: rename `func Phase2(...) error` → `func Phase3(...) error`. Update doc comment opening line to "Phase3 runs the four-step summoning-book core...".
- [ ] In `phase3_book.go`: rename `Phase2Deps` → `Phase3BookDeps` to avoid clash with the new `Phase2Deps` struct in `phase2_choose.go` (which is a different shape).
- [ ] In `phase4_seal.go`: rename `func Phase3(...)` → `func Phase4(...)`. Update doc comment to "Phase4 runs seal → calling-words → response.". Rename `Phase3Deps` → `Phase4Deps`.
- [ ] In `run.go`: update both call sites (`Phase2(...)` → `Phase3(...)`, `Phase3(...)` → `Phase4(...)`) and `Phase2Deps` → `Phase3BookDeps`, `Phase3Deps` → `Phase4Deps`.
- [ ] In `phase4_seal_test.go` and any other test file referencing the old names: same renames.

### Step 6.3: Build and test

```
go build ./...
go test ./internal/firstcontact/...
```

Expected: green. Fix any references missed (search for `firstcontact.Phase2` / `firstcontact.Phase3` and update).

### Step 6.4: Commit

```
git add internal/firstcontact/
git commit -m "refactor(firstcontact): renumber phases 2/3 -> 3/4

Phase 2 is now the new mindform-stage choice (action + master source).
The old Phase 2 (character question + research + naming) is now Phase 3,
and the old Phase 3 (seal + calling-words + response) is now Phase 4.
Phase2Deps is renamed Phase3BookDeps so the new Phase 2 owns its short
name."
```

---

## Task 7 — Run.go orchestration

**Files:**
- Modify: `internal/firstcontact/run.go`

### Step 7.1: Rewrite Run() body

- [ ] Replace the body of `Run` with the new dispatch logic:

```go
func Run(ctx context.Context, d Deps) (*Summoning, []byte, error) {
	s := &Summoning{}
	initialized, err := isIdentityInitialized(d.StateDir)
	if err != nil {
		return nil, nil, err
	}
	s.Subsequent = initialized

	if !initialized {
		// First run path: Phase 0 + Phase 1.
		if err := Phase0(ctx, s, d.Renderer); err != nil {
			return nil, nil, err
		}
		if err := Phase1(ctx, s, d.Renderer, Phase1Deps{
			StateDir:        d.StateDir,
			OperatorKeyPath: d.OperatorKeyPath,
		}); err != nil {
			return s, nil, err
		}
	} else {
		// Subsequent run: load local identity as default master, skip 0/1.
		if err := loadLocalMasterDefault(d.StateDir, s); err != nil {
			return s, nil, err
		}
		d.Renderer.Show(fmt.Sprintf(stringFor(s.Lang, "welcome_back"), s.MasterLabel))
	}

	// Phase 2: mindform-stage choice. Skipped only by --master-card flag,
	// which Phase 2 itself short-circuits.
	action, err := Phase2(ctx, s, d.Renderer, Phase2Deps{
		EntryMode:      d.EntryMode,
		MasterCardPath: d.MasterCardPath,
	})
	if err != nil {
		return s, nil, err
	}
	if action == Phase2Exit {
		return s, nil, nil
	}

	// Phase 3: character.
	ready := StartBackground(ctx, d.ReadyDeps)
	existing, _ := d.ExistingSlugs()
	if err := Phase3(ctx, s, d.Renderer, d.Claude, Phase3BookDeps{ExistingSlugs: existing}); err != nil {
		return s, nil, err
	}

	// Phase 4: seal & response.
	body, err := Phase4(ctx, s, d.Renderer, d.Claude, ready, Phase4Deps{
		DockerClient: d.DockerClient, Image: d.Image,
		WriteVolume: d.WriteVolume, ContainerStart: d.StartContainer,
		ResponseWait: d.ResponseWait, AddContact: d.AddContact,
	})
	return s, body, err
}
```

### Step 7.2: Build + test

```
go build ./...
go test ./internal/firstcontact/...
```

Expected: green. (Some tests in `run_test.go` may need their renamed-field assertions updated — fix inline.)

### Step 7.3: Commit

```
git add internal/firstcontact/run.go
git commit -m "refactor(firstcontact): drive new four-phase tree from Run

Run now handles three flows uniformly:
  - first run: Phase 0 (logo+lang+intro) → Phase 1 (identity) → Phase 2
  - subsequent run: load local master default → 'welcome_back' → Phase 2
  - any run: Phase 2 returns exit/local/card; on summon, Phase 3 + Phase 4

The welcome_back acknowledgement moves from the top of the subsequent
branch to immediately before Phase 2 — the natural place to greet a
returning operator now that Phase 0/1 are conditionally skipped."
```

---

## Task 8 — `eidos gate card` subcommand

**Files:**
- Create: `cmd/eidos/gate/card.go`
- Create: `cmd/eidos/gate/card_test.go`

### Step 8.1: Implement the subcommand

- [ ] Write tests first:

```go
// cmd/eidos/gate/card_test.go
package gate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

func TestCardExport_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "alice", "wss://relay.example/"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	out := filepath.Join(dir, "alice.eidos-card.toml")
	if err := exportCard(dir, out, "" /* override */, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("exportCard: %v", err)
	}
	c, err := card.Read(out)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Label != "alice" {
		t.Errorf("label = %q, want alice", c.Label)
	}
	if c.HomeRelay != "wss://relay.example/" {
		t.Errorf("home_relay = %q", c.HomeRelay)
	}
}
```

- [ ] Implement `cmd/eidos/gate/card.go`:

```go
package gate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

var (
	cardExportOut   string
	cardExportLabel string
)

var cardCmd = &cobra.Command{
	Use:   "card",
	Short: "Manage identity cards (export your own; show any card)",
}

var cardExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export this gate's identity as a TOML identity card",
	Long: `Reads the local gate state (key + label + home relay) and writes
a v1 identity card to the path given by --out (default:
~/eidos-cards/<label>.eidos-card.toml).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stateDir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		out := cardExportOut
		if out == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			label := cardExportLabel
			if label == "" {
				db, err := store.Open(filepath.Join(stateDir, "state.db"), true)
				if err != nil {
					return fmt.Errorf("open state.db: %w", err)
				}
				defer db.Close()
				label, err = db.GetMeta(context.Background(), "label")
				if err != nil || label == "" {
					return fmt.Errorf("read label from state.db: %w", err)
				}
			}
			out = filepath.Join(home, "eidos-cards", label+".eidos-card.toml")
		}
		return exportCard(stateDir, out, cardExportLabel, time.Now().UTC())
	},
}

var cardShowCmd = &cobra.Command{
	Use:   "show <path>",
	Short: "Pretty-print and validate a card file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := card.Read(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("schema_version : %d\n", c.SchemaVersion)
		fmt.Printf("label          : %s\n", c.Label)
		fmt.Printf("pubkey_hex     : %s\n", c.PubkeyHex)
		fmt.Printf("npub           : %s\n", c.Npub)
		fmt.Printf("home_relay     : %s\n", c.HomeRelay)
		fmt.Printf("created_at     : %s\n", c.CreatedAt.Format(time.RFC3339))
		return nil
	},
}

// exportCard reads gate state from stateDir and writes a Card to out.
// labelOverride, if non-empty, replaces the state.db label.
func exportCard(stateDir, out, labelOverride string, now time.Time) error {
	keyPath := filepath.Join(stateDir, "key")
	k, err := identity.LoadKey(keyPath)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}
	dbPath := filepath.Join(stateDir, "state.db")
	db, err := store.Open(dbPath, true)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	label := labelOverride
	if label == "" {
		label, err = db.GetMeta(ctx, "label")
		if err != nil {
			return fmt.Errorf("read label: %w", err)
		}
	}
	var home string
	if err := db.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&home); err != nil {
		return fmt.Errorf("read home relay: %w", err)
	}
	c := card.Card{
		SchemaVersion: 1,
		Label:         label,
		PubkeyHex:     k.PublicHex,
		Npub:          k.Npub,
		HomeRelay:     home,
		CreatedAt:     now,
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("validate card: %w", err)
	}
	if err := card.Write(out, c); err != nil {
		return fmt.Errorf("write card: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ wrote %s\n", out)
	return nil
}

func init() {
	cardExportCmd.Flags().StringVar(&cardExportOut, "out", "", "output path (default ~/eidos-cards/<label>.eidos-card.toml)")
	cardExportCmd.Flags().StringVar(&cardExportLabel, "label", "", "override the label written into the card (does not change local state)")
	cardCmd.AddCommand(cardExportCmd)
	cardCmd.AddCommand(cardShowCmd)
	rootCmd.AddCommand(cardCmd)
}
```

(Verify `globalStateDir` is the existing string variable defined in `cmd/eidos/gate/root.go` for the `--state-dir` flag.)

### Step 8.2: Run + commit

```
go build ./...
go test ./cmd/eidos/gate/...
git add cmd/eidos/gate/card.go cmd/eidos/gate/card_test.go
git commit -m "feat(gate): add 'eidos gate card export|show' subcommands"
```

---

## Task 9 — Wire `eidos summon`

**Files:**
- Modify: `cmd/eidos/summon/cmd.go`

### Step 9.1: Add flags + EntryMode

- [ ] Find the `Command()` function and add flags:

```go
var (
	summonMasterCard string
	summonKeyFile    string
)

func Command() *cobra.Command {
	c := &cobra.Command{ /* unchanged Use/Short/Long */
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(cmd.Context())
		},
	}
	c.Flags().StringVar(&summonMasterCard, "master-card", "", "path to the master's identity card (overrides any local identity)")
	c.Flags().StringVar(&summonKeyFile, "key-file", "", "path to a file containing the operator's nsec1... or 32-byte hex private key (used in the import-identity branch)")
	return c
}
```

- [ ] In `Run`, populate the new Deps fields:

```go
deps := firstcontact.Deps{
	// ... existing fields ...
	EntryMode:       firstcontact.EntrySummon,
	MasterCardPath:  summonMasterCard,
	OperatorKeyPath: summonKeyFile,
}
```

### Step 9.2: Build + commit

```
go build ./...
git add cmd/eidos/summon/cmd.go
git commit -m "feat(summon): wire EntrySummon, --master-card, --key-file"
```

---

## Task 10 — Wire `eidos gate init`

**Files:**
- Modify: `cmd/eidos/gate/init.go`

### Step 10.1: Delegate to wizard with EntryGateInit

- [ ] Open `cmd/eidos/gate/init.go`. The current Run body bootstraps identity directly. Replace it with a wizard delegation:

```go
RunE: func(cmd *cobra.Command, args []string) error {
	stateDir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return err
	}
	if firstcontact.IsIdentityInitialized(stateDir) {
		// Preserve existing exit-0 behavior with clearer message.
		label, _ := readGateLabel(stateDir) // best-effort
		fmt.Fprintf(cmd.OutOrStdout(),
			"this host already has a gate identity at %s (label: %s)\n", stateDir, label)
		return nil
	}
	rend := render.NewAuto(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
	cl := &firstcontact.Claude{Run: firstcontact.ProductionRunner}
	deps := firstcontact.Deps{
		StateDir:  stateDir,
		Renderer:  rend,
		Claude:    cl,
		EntryMode: firstcontact.EntryGateInit,
		// No mindform deps needed: Phase 2 will return Phase2Exit.
	}
	if tui, ok := rend.(render.TUIRenderer); ok {
		return tui.RunWithPhases(cmd.Context(), func(ctx context.Context, r render.Renderer) error {
			deps.Renderer = r
			_, _, err := firstcontact.Run(ctx, deps)
			return err
		})
	}
	_, _, err = firstcontact.Run(cmd.Context(), deps)
	return err
},
```

Where `readGateLabel(stateDir)` is a small helper that opens state.db read-only and returns the `label` meta row (empty string on error). Define it in this same file.

**Caveat:** Phase 2 with `EntryGateInit` is documented in the spec to be "forced exit". The current implementation in `phase2_choose.go` (Task 5) computes options based on `EntryMode != EntrySummon` to include the exit option — so for `EntryGateInit`, the menu shows exit + local + card. **For gate-init the user just picks exit.** This is acceptable for v1 (no extra special-casing). If we want to *force* exit (no menu shown), follow up by adding a special case in `Phase2`:

```go
if d.EntryMode == EntryGateInit {
    return Phase2Exit, nil
}
```

— add this guard at the top of `Phase2` in this same task. (Recommended: add the guard to avoid confusing UX.)

- [ ] Add the guard now:

```go
// internal/firstcontact/phase2_choose.go, at top of Phase2:
if d.EntryMode == EntryGateInit {
    return Phase2Exit, nil
}
```

### Step 10.2: Build + commit

```
go build ./...
go test ./cmd/eidos/gate/... ./internal/firstcontact/...
git add cmd/eidos/gate/init.go internal/firstcontact/phase2_choose.go
git commit -m "feat(gate): delegate 'gate init' to the unified wizard

gate init now goes through firstcontact.Run with EntryGateInit so all
identity-creation logic lives in one place. Phase 2 short-circuits to
Exit for EntryGateInit, so the user sees Phase 0 + Phase 1 only.
When the host is already initialized, gate init prints a clear message
and exits 0 (preserving today's exit-status contract)."
```

---

## Task 11 — Documentation

**Files:**
- Modify: `docs/specs/FirstContact.md`
- Modify: `README.md`

### Step 11.1: Rewrite FirstContact.md sections 一, 二, 六

- [ ] Replace section 一 (前置硬性要求) — add a bullet:

> - **Wizard 现在按两层走**：先选本地身份（创建 / 导入 / 跳过），再选要不要召唤心智体。这两层互不绑定——纯 mindgate 部署、纯 mindform 部署、双开三种模式都被显式支持。

- [ ] Replace section 二 (阶段顺序) Phase 0/1/2 sub-sections to reflect the four-phase tree from spec § 4.1. Phase 3/4 keep their existing content, just renumbered.

- [ ] Replace section 六 (Subsequent Runs):

> 一个用户可能召唤多个 MindForm。subsequent run 模式下：
>
> - **跳过 Phase 0** 整段（语言已选过；intro 已读过）
> - **跳过 Phase 1**（已有本地身份，跳到 Phase 2 入口前会打一行 "欢迎回来, <label>"）
> - **从 Phase 2 起**：用户在这一步仍然可以选"用本地身份召唤"或"用一张名片召唤"——身份归属是每一次召唤的独立决定，不是上一次的延续
> - 即便 wizard 中途失败而要重来，本地身份保留，只这一次的召唤被丢弃
>
> 判定 subsequent 用 `firstcontact.IsIdentityInitialized(stateDir)`：当且仅当 `<stateDir>/key` 与 `state.db` 都存在。这是 `cmd/eidos/main.go` 与 wizard 自身共用的同一谓词。

### Step 11.2: README quickstart note

- [ ] Add to README, near the existing quickstart section, a one-line note:

> First-run `eidos` offers three deployment modes: mindgate-only (create identity, then exit), mindform-only (skip identity, summon with a master card), or both (the default path).

### Step 11.3: Commit

```
git add docs/specs/FirstContact.md README.md
git commit -m "docs: update FirstContact spec + README for wizard decoupling"
```

---

## Task 12 — Final sweep

- [ ] **Lint:**

```
gofmt -l . && go vet ./...
```

Both must produce no output.

- [ ] **Full test:**

```
go test ./...
```

All packages green.

- [ ] **Build:**

```
go build ./cmd/eidos
```

- [ ] **Smoke test (manual)** — runs end-to-end with a temp state dir:

```
EIDOS_GATE_HOME=$(mktemp -d) ./eidos --help        # should show cobra help (state empty → wizard would launch on bare; --help short-circuits)
EIDOS_GATE_HOME=$(mktemp -d) ./eidos gate card show /nonexistent  # should error cleanly
```

(The full wizard smoke is part of the post-PR install step.)

- [ ] **Commit any cleanup:**

```
git status
# if anything changed (gofmt fixes etc.), commit:
git commit -am "chore: gofmt + vet sweep"
```

---

## Self-review pass (do once after Task 12)

1. **Spec coverage**: walk through spec § 3 (card), § 4 (wizard), § 5 (intro), § 6 (migration), § 7 (testing), § 8 (out-of-scope), § 9 (docs). Confirm a task implements each. Out-of-scope items have nothing to do.
2. **Placeholder scan**: search the diff for "TBD" / "TODO" — if anything snuck in, fix.
3. **Type consistency**: `Phase2Action` enum, `EntryMode` enum, `Phase2Deps` vs `Phase3BookDeps` — confirm no name collisions or stray references to old names (`firstcontact.Phase2(...)` should be either the new `Phase2(...)` returning `Phase2Action` or the renamed `Phase3(...)` for the character phase, never both at the same call site).

If any issue is found, fix inline and re-run `go build && go vet && go test ./...` once.
