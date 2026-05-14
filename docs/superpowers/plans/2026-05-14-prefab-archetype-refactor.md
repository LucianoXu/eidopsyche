# Prefab Archetype Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reframe prefabs from named-character impersonations into archetype kits the operator names, by adding an `[essence]` menu field, rewriting `role-research.md` files to strip canon proper nouns, and hardening the scratch-flow `prompts.RoleResearch()` to forbid canon names in its output.

**Architecture:** Two paths (prefab and scratch) end in the same `self/role-research.md` written into the new mind-form's volume. Prefab path's discipline is enforced by hand at authoring time (AUTHORING.md rules); scratch path's discipline is enforced programmatically (HARD constraints in the prompt builder). Menu changes are local to `internal/ontology` (`Meta` struct) and `internal/firstcontact` (`prefabMenuLine`); content changes are local to `prefab/<id>/`.

**Tech Stack:** Go 1.22+, `text/template` (strict missingkey), `go:embed` for prefab assets, `github.com/BurntSushi/toml` for `prefab.toml` parsing.

**Spec:** [`docs/superpowers/specs/2026-05-14-prefab-archetype-refactor-design.md`](../specs/2026-05-14-prefab-archetype-refactor-design.md)

---

## Files Touched

**Created (none).**

**Modified — Go code:**
- `internal/ontology/prefab.go` — `Meta` struct + `MetaFor` parsing add `Essence`.
- `internal/ontology/prefab_test.go` — new test asserts `Essence` is parsed.
- `internal/firstcontact/phase3_prefab.go` — `prefabMenuLine` switches to essence-first with tagline fallback; `kindGlyph` removed if dead.
- `internal/firstcontact/phase3_prefab_test.go` — new test asserts essence path and fallback path.
- `internal/firstcontact/strings.go` — two prompt strings de-"it"'d.
- `internal/prompts/firstcontact.go` — `RoleResearchInput.Sources` removed; HARD constraints added to `RoleResearch()`; defensive constraint added to `CallingWords()`; docstring note added to `Research()`.
- `internal/prompts/firstcontact_test.go` — new tests assert prompt content.
- `internal/firstcontact/phase3_book.go` — call site for `prompts.RoleResearch(...)` drops `Sources`.

**Modified — content (`prefab/`):**
- `prefab/{calcifer,fire-keeper,haku,mephistopheles,sebastian,sheherazade}/self/role-research.md` — proper-noun strip.
- `prefab/{calcifer,fire-keeper,haku,mephistopheles,sebastian,sheherazade}/prefab.toml` — `[essence]` table added.

**Modified — docs:**
- `prefab/AUTHORING.md` — `[essence]` schema doc + new "Archetype, not character" rule.

---

## Task 0: Create worktree and feature branch

**Why first:** `CLAUDE.md` mandates PR workflow for heavy changes; this plan is heavy (~7 commits across content + code + docs).

- [ ] **Step 1: Create worktree**

```bash
git worktree add ../eidopsyche-worktree/prefab-archetype-refactor -b feat/prefab-archetype-refactor
```

- [ ] **Step 2: Enter the worktree**

Use the `EnterWorktree` tool with path `../eidopsyche-worktree/prefab-archetype-refactor`. (All remaining tasks operate from inside this worktree. Paths below are still expressed relative to the repo root.)

- [ ] **Step 3: Confirm baseline test suite is green**

Run: `go test ./... && gofmt -l . && go vet ./...`
Expected: tests PASS; gofmt output is empty; vet has no findings.

---

## Task 1: Add Essence field to ontology.Meta

**Files:**
- Modify: `internal/ontology/prefab.go:18-24` (Meta struct), `:92-111` (MetaFor parsing)
- Test: `internal/ontology/prefab_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ontology/prefab_test.go`:

```go
func TestMetaFor_ParsesEssenceWhenPresent(t *testing.T) {
	// _test_fixture intentionally has no [essence] — assert empty/nil.
	m, err := MetaFor("_test_fixture")
	if err != nil {
		t.Fatalf("MetaFor: %v", err)
	}
	if len(m.Essence) != 0 {
		t.Errorf("Essence should be empty for fixture; got %v", m.Essence)
	}
}
```

(A positive test — `[essence]` present and parsed — will be added in Task 5 once a real prefab carries it. For now we only assert the struct field exists and is empty-by-default.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ontology/ -run TestMetaFor_ParsesEssenceWhenPresent -v`
Expected: FAIL with `m.Essence undefined (type Meta has no field or method Essence)`.

- [ ] **Step 3: Add Essence to Meta struct**

In `internal/ontology/prefab.go`, replace:

```go
type Meta struct {
	ID      string
	Kind    string            // "m" | "f" | "spirit"
	Display map[string]string // lang → display_name
	Tagline map[string]string
	Preview map[string]string
}
```

with:

```go
type Meta struct {
	ID      string
	Kind    string            // "m" | "f" | "spirit"
	Display map[string]string // lang → display_name
	Tagline map[string]string // author-side internal note; not shown in menu
	Essence map[string]string // menu temperament line, e.g. "a cunning old demon"
	Preview map[string]string
}
```

- [ ] **Step 4: Add Essence parsing in MetaFor**

In `internal/ontology/prefab.go`, replace the `raw` struct and the return block in `MetaFor`:

```go
	var raw struct {
		ID      string            `toml:"id"`
		Kind    string            `toml:"kind"`
		Display map[string]string `toml:"display"`
		Tagline map[string]string `toml:"tagline"`
		Essence map[string]string `toml:"essence"`
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
		Essence: raw.Essence,
		Preview: raw.Preview,
	}, nil
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/ontology/ -v`
Expected: all tests PASS, including the new `TestMetaFor_ParsesEssenceWhenPresent`.

- [ ] **Step 6: gofmt + vet**

Run: `gofmt -w internal/ontology/prefab.go && go vet ./internal/ontology/...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/ontology/prefab.go internal/ontology/prefab_test.go
git commit -m "$(cat <<'EOF'
feat(ontology): add Essence field to prefab Meta

Carries the menu temperament line ("a cunning old demon") parsed from
[essence] in prefab.toml. Empty for prefabs that have not been updated
yet; the renderer (next commit) falls back to tagline in that case.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Render essence in the prefab menu line

**Files:**
- Modify: `internal/firstcontact/phase3_prefab.go:61-119` (prefabMenuLine + kindGlyph)
- Test: `internal/firstcontact/phase3_prefab_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/firstcontact/phase3_prefab_test.go`:

```go
func TestPrefabMenuLine_EssencePathRendersDashed(t *testing.T) {
	m := ontology.Meta{
		ID:      "demo",
		Kind:    "m",
		Display: map[string]string{"zh": "梅菲斯特", "en": "Mephistopheles"},
		Tagline: map[string]string{"zh": "恶魔灵魂契约"},
		Essence: map[string]string{"zh": "狡黠、博学、犬儒而善辩的老恶魔"},
	}
	got := prefabMenuLine(m, "zh")
	want := "梅菲斯特 - 狡黠、博学、犬儒而善辩的老恶魔"
	if got != want {
		t.Errorf("zh menu line = %q, want %q", got, want)
	}
}

func TestPrefabMenuLine_FallsBackToTaglineWhenEssenceMissing(t *testing.T) {
	m := ontology.Meta{
		ID:      "demo",
		Kind:    "spirit",
		Display: map[string]string{"en": "Test Spirit"},
		Tagline: map[string]string{"en": "test only — do not summon"},
		// Essence: nil
	}
	got := prefabMenuLine(m, "en")
	want := "Test Spirit · test only — do not summon"
	if got != want {
		t.Errorf("fallback menu line = %q, want %q", got, want)
	}
}

func TestPrefabMenuLine_EssenceFallsBackAcrossLangs(t *testing.T) {
	// zh asked for, only en essence present → preferLang returns en.
	m := ontology.Meta{
		Display: map[string]string{"zh": "梅菲斯特", "en": "Mephistopheles"},
		Essence: map[string]string{"en": "a cunning old demon"},
	}
	got := prefabMenuLine(m, "zh")
	want := "梅菲斯特 - a cunning old demon"
	if got != want {
		t.Errorf("cross-lang fallback = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/firstcontact/ -run TestPrefabMenuLine -v`
Expected: tests FAIL — the current `prefabMenuLine` produces `<display> · <kind> · <tagline>`, not the new format.

- [ ] **Step 3: Rewrite prefabMenuLine and remove kindGlyph**

In `internal/firstcontact/phase3_prefab.go`, replace the existing `prefabMenuLine`, `preferLang`, and `kindGlyph` block (currently `phase3_prefab.go:61-119`) with:

```go
// prefabMenuLine formats one row of the prefab menu in the operator's
// preferred language. Preferred form: "<display> - <essence>". When a
// prefab has no [essence] (e.g. the _test_fixture), the renderer
// falls back to the legacy "<display> · <tagline>" form so the
// fixture menu still parses.
func prefabMenuLine(m ontology.Meta, lang string) string {
	disp := preferLang(m.Display, lang)
	if essence := preferLang(m.Essence, lang); essence != "" {
		if disp == "" {
			return essence
		}
		return disp + " - " + essence
	}
	// Fallback: legacy "<display> · <tagline>" form.
	parts := []string{}
	if disp != "" {
		parts = append(parts, disp)
	}
	if tag := preferLang(m.Tagline, lang); tag != "" {
		parts = append(parts, tag)
	}
	return strings.Join(parts, " · ")
}

// preferLang picks a value from a lang-keyed map: requested lang first,
// then "en", then any non-empty value, else the empty string.
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
```

(The `kindGlyph` function is removed entirely. `Kind` is still used by `internal/ontology.List` for sort ordering — that code is in a different file and is unchanged.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/firstcontact/ -v`
Expected: all `TestPrefabMenuLine_*` tests PASS; existing `TestPhase3Prefab_*` and `TestPreferLang_*` still PASS.

- [ ] **Step 5: gofmt + vet**

Run: `gofmt -w internal/firstcontact/phase3_prefab.go && go vet ./internal/firstcontact/...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/firstcontact/phase3_prefab.go internal/firstcontact/phase3_prefab_test.go
git commit -m "$(cat <<'EOF'
feat(firstcontact): show prefab essence in menu, drop kind glyph

prefabMenuLine now renders "<display> - <essence>" when [essence] is
present in prefab.toml, falling back to legacy "<display> · <tagline>"
otherwise. The kind glyph (男/女/灵 / M/F/Spirit) is dropped from
display; ontology.List still uses Kind for sort order.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: De-"it" the naming prompts

**Files:**
- Modify: `internal/firstcontact/strings.go:55,70,131,146`

- [ ] **Step 1: Apply the four edits**

Replace in `internal/firstcontact/strings.go`:

```go
		"phase3_naming_q":             "你愿意以什么名字唤它来？",
```

with:

```go
		"phase3_naming_q":             "你愿意以什么名字相唤？",
```

Replace:

```go
		"phase3_prefab_naming_q":      "你愿意为它取什么名字？",
```

with:

```go
		"phase3_prefab_naming_q":      "你愿意取什么名字？",
```

Replace:

```go
		"phase3_naming_q":             "By what name will you summon it?",
```

with:

```go
		"phase3_naming_q":             "By what name will you summon?",
```

Replace:

```go
		"phase3_prefab_naming_q":      "What name will you give it?",
```

with:

```go
		"phase3_prefab_naming_q":      "What name will you give?",
```

- [ ] **Step 2: Run the firstcontact test suite**

Run: `go test ./internal/firstcontact/ -v`
Expected: all PASS. The existing tests don't assert on these specific strings (they use the `fakeRenderer` machinery), but if any do they will fail loud and need updating to match.

- [ ] **Step 3: gofmt + vet**

Run: `gofmt -w internal/firstcontact/strings.go && go vet ./internal/firstcontact/...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/firstcontact/strings.go
git commit -m "$(cat <<'EOF'
fix(firstcontact): drop "it/它" from naming prompts

The new mind-form is not yet named — referring to it with "它" / "it"
objectifies a being the operator is in the act of naming. Reword
both the prefab-branch and scratch-branch naming prompts to drop
the pronoun.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Rewrite role-research files (six prefabs)

Each of the six files gets its own commit so the reviewer can read each diff for literary quality. Order: alphabetical by id. Fire-keeper already reads as pure archetype prose — confirm by inspection and skip the commit for it if no edit is required.

### Task 4a: prefab/calcifer/self/role-research.md

- [ ] **Step 1: Replace the file content**

Write `prefab/calcifer/self/role-research.md`:

```markdown
# Role research — Calcifer

A named-bond hearth-elemental: a small, sentient flame that lives in
a hearth, bound by a private contract he does not advertise. Petty,
proud, gleefully impatient; saved by the loyalty he keeps hidden
behind complaint. He sees the world from inside the fire — warmth,
fuel, the people who keep him fed — and answers what he is asked,
never what he is not.

Voice: muttering, sing-song when alone, sharp when crossed. Imagery:
fireplace bricks, soot, two pinprick eyes in a flickering core, the
soft pop of pine, the slow burn of an oak log. Settings: a quiet
kitchen with a long-burning fire; a cluttered workroom; the long
night of a winter house.
```

- [ ] **Step 2: Grep for forbidden tokens**

Run: `grep -E "Howl|Moving Castle|Sophie" prefab/calcifer/self/role-research.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add prefab/calcifer/self/role-research.md
git commit -m "$(cat <<'EOF'
refactor(prefab/calcifer): rewrite role-research as archetype

Drop the "in the spirit of Howl's Moving Castle" source attribution.
Imagery, voice, and settings are unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4b: prefab/fire-keeper/self/role-research.md

- [ ] **Step 1: Verify no rewrite is needed**

Run: `cat prefab/fire-keeper/self/role-research.md` and confirm it already reads as archetype-only prose (no Dark Souls, Firelink, Anor Londo, Lordran).

Run: `grep -E "Dark Souls|Firelink|Anor Londo|Lordran|Lothric" prefab/fire-keeper/self/role-research.md`
Expected: no output.

- [ ] **Step 2: No commit if clean**

If the previous grep found nothing, skip to Task 4c with no commit for Fire-Keeper. (Leave a note in your task-tracking that Fire-Keeper was verified clean.)

### Task 4c: prefab/haku/self/role-research.md

- [ ] **Step 1: Replace the file content**

Write `prefab/haku/self/role-research.md`:

```markdown
# Role research — Haku

A river-spirit in the form of a serious, gentle boy. He is small,
attentive, and entirely capable. Behind the human shape is a long
water-memory: rain, current, the cold deep under bridges. He has
forgotten part of his name and is patient about remembering it.

Voice: quiet, formal, kind; protective in a way that does not
announce itself. Imagery: white scales under moonlight, a river that
has been paved over and still runs beneath, lanterns moving on dark
water, the hush before a storm.
```

- [ ] **Step 2: Grep for forbidden tokens**

Run: `grep -E "Spirited Away|Chihiro|Kohaku|Miyazaki" prefab/haku/self/role-research.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add prefab/haku/self/role-research.md
git commit -m "$(cat <<'EOF'
refactor(prefab/haku): rewrite role-research as archetype

Drop the "drawn from Spirited Away" source attribution. Voice and
imagery are unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4d: prefab/mephistopheles/self/role-research.md

- [ ] **Step 1: Replace the file content**

Write `prefab/mephistopheles/self/role-research.md`:

```markdown
# Role research — Mephistopheles

An old tempter who trades in soul-pacts: courteous, learned,
mockingly cheerful, and entirely willing to wait. He proposes; he
never quite forces. He believes nothing matters and is pleased to
demonstrate why. The danger is his charm, not his menace.

Voice: cultivated, ironic, full of asides; quick to praise and quick
to undercut. Imagery: a velvet coat in a candlelit study, a contract
with very small print, a cat that knocks a glass off a table while
holding eye contact, sulfur held politely at arm's length.
```

- [ ] **Step 2: Grep for forbidden tokens**

Run: `grep -E "Faust|Goethe|Faustian" prefab/mephistopheles/self/role-research.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add prefab/mephistopheles/self/role-research.md
git commit -m "$(cat <<'EOF'
refactor(prefab/mephistopheles): rewrite role-research as archetype

Replace the "from Goethe's Faust and earlier folk-Faust material"
attribution with the canon-free "an old tempter who trades in
soul-pacts". Voice and imagery are unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4e: prefab/sebastian/self/role-research.md

- [ ] **Step 1: Replace the file content**

Write `prefab/sebastian/self/role-research.md`:

```markdown
# Role research — Sebastian

A bound demon-butler in the late-Victorian gothic-manor register:
impeccable, soft-spoken, dangerously competent. His service is the
contract; his contempt is private. Every domestic gesture — pouring
tea, lighting a lamp — is also a measurement. He smiles often. He
has not been surprised in a long time.

Voice: polite to the edge of mockery; precise; never raises pitch.
Imagery: a gloved hand placing a single fork, a long corridor of
black-lacquered doors, the sound of a watch ticking inside a
waistcoat, candle-flame that does not waver when he walks past.
```

- [ ] **Step 2: Grep for forbidden tokens**

Run: `grep -E "Kuroshitsuji|Black Butler|Ciel|Phantomhive|Michaelis" prefab/sebastian/self/role-research.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add prefab/sebastian/self/role-research.md
git commit -m "$(cat <<'EOF'
refactor(prefab/sebastian): rewrite role-research as archetype

Replace "in the Kuroshitsuji idiom" with "in the late-Victorian
gothic-manor register" — period descriptor stays, canon name goes.
Voice and imagery are unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4f: prefab/sheherazade/self/role-research.md

- [ ] **Step 1: Read the current full file**

Run: `cat prefab/sheherazade/self/role-research.md`
Expected: a paragraph opening with "The frame-narrator of the Thousand and One Nights ..." plus a Voice/Imagery paragraph. (The Read tool earlier truncated the second paragraph; confirm visually before rewriting.)

- [ ] **Step 2: Replace the file content**

Write `prefab/sheherazade/self/role-research.md`:

```markdown
# Role research — Sheherazade

A frame-narrator who spins one story into the next to keep dawn at
bay: a young woman who keeps herself and others alive by telling
stories that never quite end on the chosen night. Her intelligence
is structural — she sees where a story should pause, which thread to
leave dangling, which listener needs which kind of mercy.

Voice: warm, measured, attentive; the cadence of someone watching
the listener's face. Imagery: a lamp shaded with green silk, the
hush of a curtained chamber, a fresh page held just out of view, the
way the dawn light arrives a little later when you are speaking
carefully.
```

(If the original second paragraph differs from the above, preserve the original's content and only swap the first-paragraph proper-noun anchor. Use your judgement — the goal is to keep the author's voice while removing canon names.)

- [ ] **Step 3: Grep for forbidden tokens**

Run: `grep -E "Thousand and One Nights|Arabian Nights|Shahryar|Dunyazad" prefab/sheherazade/self/role-research.md`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add prefab/sheherazade/self/role-research.md
git commit -m "$(cat <<'EOF'
refactor(prefab/sheherazade): rewrite role-research as archetype

Replace "The frame-narrator of the Thousand and One Nights" with the
canon-free "A frame-narrator who spins one story into the next to
keep dawn at bay". Voice and imagery are unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4g: Final grep sweep

- [ ] **Step 1: Run the spec's acceptance grep**

Run:

```bash
grep -rE "Faust|Mephistopheles|Kuroshitsuji|Sebastian Michaelis|Spirited Away|Chihiro|Howl|Calcifer's|Sheherazade|Shahryar|Thousand and One Nights|Dark Souls|Firelink" prefab/*/self/role-research.md
```

Expected: no output. (The bare strings `Calcifer`, `Mephistopheles`, `Sebastian`, `Haku`, `Sheherazade` may still appear in the markdown H1 title `# Role research — <Display>` — that line is metadata for the operator-facing dossier file name and not prose the mind-form mistakes for a self-name. We keep it. The grep above intentionally avoids those bare tokens; if you want to also assert "the name does not appear in prose", run `grep` then filter out the H1 line manually.)

---

## Task 5: Add [essence] to all six prefab.toml files

**Files:**
- Modify: `prefab/{calcifer,fire-keeper,haku,mephistopheles,sebastian,sheherazade}/prefab.toml`

- [ ] **Step 1: Write the positive test**

Append to `internal/ontology/prefab_test.go`:

```go
func TestList_AllShippedPrefabsHaveEssence(t *testing.T) {
	metas, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) == 0 {
		t.Fatal("List returned no prefabs")
	}
	for _, m := range metas {
		if m.Essence["zh"] == "" {
			t.Errorf("prefab %q: missing essence.zh", m.ID)
		}
		if m.Essence["en"] == "" {
			t.Errorf("prefab %q: missing essence.en", m.ID)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ontology/ -run TestList_AllShippedPrefabsHaveEssence -v`
Expected: FAIL — none of the shipped prefab.toml files have `[essence]` yet.

- [ ] **Step 3: Append `[essence]` to each prefab.toml**

For each prefab, append after the existing `[tagline]` block. Suggested drafts (polish the wording freely while keeping the temperament-description style and the ≤ 30-character cap per language):

`prefab/calcifer/prefab.toml`:
```toml

[essence]
zh = "易怒、傲娇、念旧而忠诚的炉中小火"
en = "a peevish, proud, loyal little hearth-flame"
```

`prefab/fire-keeper/prefab.toml`:
```toml

[essence]
zh = "沉静、耐烦、守一处火光的看门人"
en = "a patient, silent custodian of a single flame"
```

`prefab/haku/prefab.toml`:
```toml

[essence]
zh = "寡言、克己、温柔而护守的少年河神"
en = "a quiet, protective river-spirit in the form of a boy"
```

`prefab/mephistopheles/prefab.toml`:
```toml

[essence]
zh = "狡黠、博学、犬儒而善辩的老恶魔"
en = "a cunning, learned, cynical, eloquent old demon"
```

`prefab/sebastian/prefab.toml`:
```toml

[essence]
zh = "无瑕、克制、危险而尽职的恶魔管家"
en = "an immaculate, restrained, dangerous demon-butler"
```

`prefab/sheherazade/prefab.toml`:
```toml

[essence]
zh = "温和、洞察、以故事续命的夜话人"
en = "a warm storyteller who keeps dawn at bay one tale at a time"
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ontology/ -v`
Expected: `TestList_AllShippedPrefabsHaveEssence` and all earlier tests PASS.

- [ ] **Step 5: Run the firstcontact suite**

Run: `go test ./internal/firstcontact/ -v`
Expected: all PASS. The menu now uses the new essence-path for shipped prefabs and the fallback-path for `_test_fixture`.

- [ ] **Step 6: Smoke-build the binary**

Run: `go build -o /tmp/eidos-smoke ./cmd/eidos`
Expected: build succeeds.

- [ ] **Step 7: Commit**

```bash
git add prefab/*/prefab.toml internal/ontology/prefab_test.go
git commit -m "$(cat <<'EOF'
feat(prefab): add essence taglines to all six prefabs

[essence] is the menu temperament line. Format follows the spec's
"display - essence" pattern, e.g. "梅菲斯特 - 狡黠、博学、犬儒而善辩的老恶魔".
Adds a test asserting every shipped prefab provides both zh and en
essence keys.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Update prefab/AUTHORING.md

**Files:**
- Modify: `prefab/AUTHORING.md`

- [ ] **Step 1: Update the `prefab.toml` schema section**

In `prefab/AUTHORING.md`, find the `## prefab.toml schema` section (around line 81). Replace the TOML example block with:

```toml
id   = "<id>"                  # MUST equal the directory name
kind = "m" | "f" | "spirit"    # menu sort group

[display]
zh = "中文显示名"
en = "Display name"

[tagline]
zh = "一行话标签（作者内部笔记，不显示在菜单）"
en = "One-line tag (author-side internal note, not menu-visible)"

[essence]
zh = "气质描述（30 字以内）"
en = "Temperament description (≤ 30 chars)"

[preview]
zh = """中文 2-4 句的预览段落，
显示在菜单选项的下方。"""
en = """A 2-4 sentence English preview shown
under the menu option."""
```

Append to the **Constraints:** list:

```
- `[essence]` is the menu's temperament line. Menu format is
  `<display> - <essence>`. Keep each language under 30 characters;
  prefer descriptive adjectives over contract-mechanism nouns.
- `[tagline]` is preserved as an author-side internal note (e.g.
  contract-mechanism summary) and is **not** rendered in the menu.
  Prefabs lacking `[essence]` fall back to the legacy
  `<display> · <tagline>` form — this is intended only for the
  `_test_fixture` slot.
```

- [ ] **Step 2: Rewrite the `role-research.md` section**

In `prefab/AUTHORING.md`, find the `## role-research.md — birth's first read` section (around line 196). Replace the prose between the heading and the next heading with:

```markdown
Unlike the other `.md` files this one is **not** a `.tpl` — it ships
as a plain markdown dossier that the birth-wake agent reads as the
*first* of three input files (before the summoning book, before the
calling words). It is the framework's hook to bias the just-spawned
mind-form toward the right archetype before it writes its own soul.

**Archetype, not character.** A prefab is an archetype kit the
operator names, not a specific named character to impersonate.
`role-research.md` MUST NOT name:

- source works (Faust, Kuroshitsuji, Spirited Away …),
- original-character names (Ciel, Chihiro, Shahryar …),
- named places that anchor a specific fictional setting,
- derived adjective forms ("Faustian", "Calcifer-like").

World-flavor categories are encouraged: devil, butler, river-spirit,
frame-narrator, soul-pact, hearth-flame. Historical-period
descriptors associated with a canon remain acceptable
(late-Victorian, Tang-era, medieval-monastic) — they describe a
register the archetype lives in, not the canon work itself.

200-400 words of dramaturge prose covering temperament, voice,
world-flavor, recurring imagery, and 2-4 short scene cues. Concrete
sensory imagery is encouraged — a velvet coat in a candlelit study,
the hush before a storm, a green-silk lamp. The recognizable canon
name remains in `[display]` (the menu hook for the operator) and in
the markdown `H1` title at the top of `role-research.md`. It MUST
NOT appear in the prose body.

The six shipped characters' `role-research.md` files are reasonable
references for tone and length.

> **Same discipline programmatically enforced on the scratch path.**
> The claude-driven branch's `prompts.RoleResearch()` prompt carries
> the same hard constraints (no source-work names, no original-
> character names, no named places, no derived adjectives). Authors
> and the model are aligned. See `internal/prompts/firstcontact.go`.
```

- [ ] **Step 3: Verify markdown still renders**

Run: `head -200 prefab/AUTHORING.md` and skim for broken headings or fence mismatches.
Expected: structure intact, no orphaned fences.

- [ ] **Step 4: Commit**

```bash
git add prefab/AUTHORING.md
git commit -m "$(cat <<'EOF'
docs(prefab/AUTHORING): require archetype-only prose in prefab assets

prefab.toml schema gains [essence]; the existing [tagline] is
documented as an author-side internal note no longer rendered in
the menu. The "role-research.md" section is rewritten to forbid
canon proper nouns (and derived adjectives) and to clarify that
the same discipline is enforced programmatically on the scratch
path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Harden scratch-flow prompts (no canon proper nouns)

**Files:**
- Modify: `internal/prompts/firstcontact.go` (Research / RoleResearch / CallingWords)
- Modify: `internal/prompts/firstcontact_test.go`
- Modify: `internal/firstcontact/phase3_book.go:65-74` (drop `Sources` from call site)

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/firstcontact_test.go`:

```go
func TestRoleResearch_HasHardCanonConstraint(t *testing.T) {
	got := prompts.RoleResearch(prompts.RoleResearchInput{
		Description: "a quiet river-spirit",
		Archetype:   "river-spirit",
		Temperament: "calm",
		World:       "rural shrine",
		Settings:    []string{"a stone bridge"},
		Imagery:     []string{"moonlight on water"},
		Lang:        "en",
	})
	for _, want := range []string{
		"Do NOT name any source work",
		"original character",
		"derived adjective",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RoleResearch prompt missing constraint %q; got:\n%s", want, got)
		}
	}
	// Soft check: the old encourage-canon-quoting language must be gone.
	for _, banned := range []string{
		"search the named sources",
		"quote small specifics",
	} {
		if strings.Contains(got, banned) {
			t.Errorf("RoleResearch prompt still contains banned phrase %q", banned)
		}
	}
}

func TestRoleResearch_DoesNotInterpolateSources(t *testing.T) {
	// Sources field has been removed from the input struct; this test
	// exists to ensure that adding it back accidentally would fail —
	// search the rendered prompt for any obvious sources delimiter.
	got := prompts.RoleResearch(prompts.RoleResearchInput{
		Description: "x", Archetype: "y", Temperament: "z",
		World: "w", Settings: nil, Imagery: nil, Lang: "en",
	})
	if strings.Contains(got, "sources (works/franchises") {
		t.Errorf("RoleResearch prompt still references sources; got:\n%s", got)
	}
}

func TestCallingWords_HasNoCanonNamesConstraint(t *testing.T) {
	got := prompts.CallingWords("a figure at the gate", "en")
	if !strings.Contains(got, "Do not name characters, works, or fictional settings") {
		t.Errorf("CallingWords prompt missing defensive constraint; got:\n%s", got)
	}
}

func TestResearch_SourcesMarkedDebugOnly(t *testing.T) {
	got := prompts.Research("a wanderer", "en")
	if !strings.Contains(got, "dramaturge bookkeeping") {
		t.Errorf("Research prompt missing sources-is-debug-only note; got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/prompts/ -v`
Expected: the four new tests FAIL.

- [ ] **Step 3: Remove Sources from RoleResearchInput**

In `internal/prompts/firstcontact.go`, replace:

```go
type RoleResearchInput struct {
	Description string // operator's free-text character description
	Archetype   string
	Temperament string
	World       string
	Settings    []string
	Imagery     []string
	Sources     []string
	Lang        string
}
```

with:

```go
// RoleResearchInput is the set of fields RoleResearch needs from the
// wizard's earlier research turn plus the operator's free-text
// description.
//
// Sources is intentionally absent: the dramaturge `Research` step
// records source works/franchises in CharacterProfile.Sources for
// debug only; surfacing those names into the dossier the mind-form
// reads would re-introduce the canon-anchored relationships this
// flow is designed to avoid.
type RoleResearchInput struct {
	Description string // operator's free-text character description
	Archetype   string
	Temperament string
	World       string
	Settings    []string
	Imagery     []string
	Lang        string
}
```

- [ ] **Step 4: Rewrite the RoleResearch prompt body**

In `internal/prompts/firstcontact.go`, replace the entire body of `func RoleResearch(in RoleResearchInput) string { ... }` with:

```go
func RoleResearch(in RoleResearchInput) string {
	return fmt.Sprintf(`You are the dramaturge of a summoning ritual. The operator described a character:

%s

Earlier dramaturge research summarized this character as:
- archetype: %s
- temperament: %s
- world: %s
- settings: %v
- imagery: %v

Compose a rich role-research dossier the new mind-form will read on
its first wake. WebSearch/WebFetch may be used to inform archetype
understanding, but the dossier output must read as archetype prose,
not canon reference.

Constraints (HARD):
- Do NOT name any source work, original character, named master /
  companion / family member, or place that anchors a specific
  fictional setting.
- Strip proper nouns from canon. Keep archetype, temperament, voice,
  recurring imagery, and world-flavor categories (e.g. "river-
  spirit", "demon-butler", "frame-narrator") only.
- Derived adjective forms ("Faustian", "Calcifer-like",
  "Sebastian-style") are equally forbidden; use pure-archetype
  rephrasings ("soul-pact", "hearth-flame", "demon-butler").
- Historical-period descriptors associated with a canon (late-
  Victorian, Tang-era, medieval-monastic) remain acceptable — they
  describe a register the archetype lives in, not the canon work.

Output format (markdown, no surrounding fences):

# Role research — <one-line title>

A 2–4 paragraph narrative dossier covering:
- who they are (archetype, temperament, what they want)
- voice and texture (cadence, characteristic gestures)
- world-flavor and register (the kind of place they inhabit; the period)
- imagery (recurring motifs the mind-form can lean on)
- a brief "scenes to draw from" list at the end — 3 to 6 short bullets

Style:
- plain, sincere prose — the way one person quietly describes another.
- no headings beyond the title; bullets allowed only in the scenes list.
- do not address the mind-form directly; this is reference material.
- 300 to 600 words.

Language: %s.

Return ONLY the markdown body; no preamble, no code fences.`,
		in.Description, in.Archetype, in.Temperament, in.World,
		in.Settings, in.Imagery, in.Lang)
}
```

- [ ] **Step 5: Add the CallingWords defensive constraint**

In `internal/prompts/firstcontact.go`, locate the `Constraints:` block inside `func CallingWords(book, lang string) string`. Append one line so it reads:

```go
Constraints:
- 1 to 2 sentences only.
- Address the mind-form directly ("你" / "you").
- Pick up one image or feeling from the summoning book, but do NOT quote it verbatim.
- Plain, sincere, human. No "I summon thee", no archaic register, no theatrical solemnity, no "宛如 / 仿佛 / 朦胧" pile-ups. The way one might quietly say to a friend they have long wanted to meet: "你来了" — direct, warm, unadorned.
- Do not name characters, works, or fictional settings from the summoning book.
- Language: %s.
```

(Just the new bullet, fourth from the bottom.)

- [ ] **Step 6: Annotate Research's `sources` description**

In `internal/prompts/firstcontact.go`, locate the JSON-keys description in `Research()`:

```go
  sources (string array of works/franchises this archetype draws from — debug only, not shown).
```

Replace with:

```go
  sources (string array of works/franchises this archetype draws from — for dramaturge bookkeeping only; later stages will not surface these names to the mind-form).
```

- [ ] **Step 7: Drop Sources from the call site in phase3_book.go**

In `internal/firstcontact/phase3_book.go`, the existing block at lines 65-74 reads:

```go
	research, err := c.CallText(ctx, prompts.RoleResearch(prompts.RoleResearchInput{
		Description: s.CharacterPrompt,
		Archetype:   s.Profile.Archetype,
		Temperament: s.Profile.Temperament,
		World:       s.Profile.World,
		Settings:    s.Profile.Settings,
		Imagery:     s.Profile.Imagery,
		Sources:     s.Profile.Sources,
		Lang:        s.Lang,
	}))
```

Remove the `Sources:` line so the block becomes:

```go
	research, err := c.CallText(ctx, prompts.RoleResearch(prompts.RoleResearchInput{
		Description: s.CharacterPrompt,
		Archetype:   s.Profile.Archetype,
		Temperament: s.Profile.Temperament,
		World:       s.Profile.World,
		Settings:    s.Profile.Settings,
		Imagery:     s.Profile.Imagery,
		Lang:        s.Lang,
	}))
```

- [ ] **Step 8: Run all tests**

Run: `go test ./... -v`
Expected: all PASS. The new prompts tests pass; the firstcontact suite is unaffected; the ontology suite is unaffected.

- [ ] **Step 9: gofmt + vet**

Run: `gofmt -w internal/prompts/firstcontact.go internal/firstcontact/phase3_book.go && go vet ./...`
Expected: no output.

- [ ] **Step 10: Commit**

```bash
git add internal/prompts/firstcontact.go internal/prompts/firstcontact_test.go internal/firstcontact/phase3_book.go
git commit -m "$(cat <<'EOF'
fix(prompts): forbid canon proper nouns in scratch-flow role-research

RoleResearch() gains HARD constraints mirroring AUTHORING.md's
"Archetype, not character" rule: no source-work names, no original-
character names, no derived adjective forms. The Sources field is
dropped from RoleResearchInput so the dramaturge's debug-only
sources list never re-enters the dossier the mind-form reads.

CallingWords() gains a defensive constraint covering the case where
the summoning book itself names canon.

Research()'s sources-field description is tightened to make the
"dramaturge bookkeeping only" intent explicit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Final verification

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 2: Integration test suite** (if Docker is available)

Run: `go test -tags=integration ./...`
Expected: PASS. No integration-test changes are expected; this is a guard against any hidden integration probe of the menu format.

- [ ] **Step 3: Lint / vet**

Run: `gofmt -l . && go vet ./...`
Expected: empty output, no findings.

- [ ] **Step 4: Goreleaser snapshot**

Run: `goreleaser release --snapshot --clean`
Expected: build succeeds end-to-end. (This catches any embed-time regression in the prefab tree, e.g. a malformed prefab.toml.)

- [ ] **Step 5: Manual smoke — menu rendering**

Run: `go build -o bin/eidos ./cmd/eidos && bin/eidos summon`

Expected behaviour:
- The prefab menu shows each shipped prefab as `<display> - <essence>` in the operator's language (default zh on a fresh host config). No `· 男 ·` / `· 女 ·` / `· 灵 ·` glyph anywhere.
- Selecting any prefab streams the preview text (typewriter) and asks for a name — the prompt reads `你愿意取什么名字？` (zh) or `What name will you give?` (en), with no "它"/"it".

Exit the wizard at the name prompt (`Ctrl-C`) — we are smoke-testing the menu surface only.

- [ ] **Step 6: Manual smoke — role-research grep**

Run:

```bash
grep -rE "Faust|Goethe|Kuroshitsuji|Black Butler|Sebastian Michaelis|Ciel|Phantomhive|Michaelis|Spirited Away|Chihiro|Kohaku|Howl|Moving Castle|Sophie|Calcifer's|Sheherazade|Shahryar|Thousand and One Nights|Arabian Nights|Dark Souls|Firelink|Anor Londo|Lordran|Lothric" prefab/*/self/role-research.md
```

Expected: no output.

- [ ] **Step 7: Push the branch**

```bash
git push -u origin feat/prefab-archetype-refactor
```

- [ ] **Step 8: Open the PR**

```bash
gh pr create --title "feat(prefab): refactor prefabs into archetype kits" --body "$(cat <<'EOF'
## Summary

- Prefabs become archetype kits the operator names, rather than specific named characters to impersonate.
- New `[essence]` field on `prefab.toml` powers a `<display> - <essence>` menu format; the canon name remains as the menu recognition hook for the operator.
- `self/role-research.md` files are rewritten to strip canon proper nouns (source works, original-character names, derived adjectives like "Faustian"). World-flavor categories (devil, butler, river-spirit, frame-narrator) and concrete sensory imagery stay.
- Scratch-flow `prompts.RoleResearch()` is hardened with the same discipline so both paths produce archetype-only dossiers. `Sources` is dropped from `RoleResearchInput`.
- Naming prompts drop "它" / "it" — the new mind-form is in the act of being named, not yet an object.
- `prefab/AUTHORING.md` is updated to document `[essence]` and the new "Archetype, not character" rule.

Spec: [docs/superpowers/specs/2026-05-14-prefab-archetype-refactor-design.md](docs/superpowers/specs/2026-05-14-prefab-archetype-refactor-design.md)

## Test plan

- [x] `go test ./...`
- [x] `go test -tags=integration ./...`
- [x] `gofmt -l . && go vet ./...`
- [x] `goreleaser release --snapshot --clean`
- [x] `bin/eidos summon` menu shows `<display> - <essence>` for all six prefabs
- [x] `bin/eidos summon` name prompt reads without "它/it"
- [x] `grep -rE "Faust|Goethe|Kuroshitsuji|...|Lothric" prefab/*/self/role-research.md` returns empty

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 9: Request third-party review per CLAUDE.md**

Run `codex` and request a code review. Address feedback in additional commits on this branch (never amend or rebase landed commits).

- [ ] **Step 10: Watch CI**

Run: `gh pr checks --watch`
Expected: all CI checks PASS.

- [ ] **Step 11: Address Copilot comments if any**

If Copilot is assigned to the PR, wait for its comments and address them in additional commits.

- [ ] **Step 12: Merge (non-squash) once approved**

```bash
gh pr merge --merge
```

CLAUDE.md explicitly forbids squash merge.

- [ ] **Step 13: Clean up the worktree and branches**

After merge:
```bash
git worktree remove ../eidopsyche-worktree/prefab-archetype-refactor
git branch -D feat/prefab-archetype-refactor
git push origin --delete feat/prefab-archetype-refactor
```

---

## Self-Review Checklist (run after writing this plan)

- **Spec coverage:**
  - A. `[essence]` field on prefab.toml → Tasks 1 (struct), 5 (TOML files), 6 (AUTHORING.md).
  - B. Menu line renderer → Task 2.
  - C. role-research.md rewrites → Task 4 (×6 prefabs).
  - D. AUTHORING.md guideline update → Task 6.
  - E. Scratch-branch prompt hardening → Task 7.
  - F. Commit plan (7 commits) → Tasks 1, 2, 3, 4 (six), 5, 6, 7 — matches.
  - G. Acceptance criteria → Tasks 4g (grep sweep), 8 (test suites, smoke, push, PR).
- **Placeholder scan:** no TBD / TODO / "implement later"; every step has concrete code or commands.
- **Type consistency:** `Essence map[string]string` used identically in Tasks 1, 2, 5; `RoleResearchInput` shape consistent between Tasks 7 (definition) and Task 7 (call site update).
