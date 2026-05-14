# Prefab Archetype Refactor — Design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** `prefab/`, `internal/ontology`, `internal/firstcontact`, `internal/prompts`

## Goal

Reframe `prefab/<id>/` from "a specific named character to instantiate"
(Mephistopheles, Sebastian, Fire-Keeper) into "an archetype kit the
operator names". The recognizable canon name remains in the **menu**
as a recognition hook for the operator; the mind-form's internal
assets never reference proper nouns from canon, so the resulting
mind-form's interpersonal relationships are shaped by the operator
rather than locked in by the prefab.

This refactor closes a current asymmetry: the prefab branch hand-writes
`role-research.md` with canon-anchored source references ("from
Goethe's Faust", "Kuroshitsuji idiom", "Spirited Away"), and the
scratch (claude-driven) branch's `prompts.RoleResearch()` actively
*instructs* the model to "search the named sources … and quote small
specifics back into the dossier (named scenes, recurring lines,
defining mannerisms)". Both paths feed the resulting dossier into the
mind-form on birth-wake, where it then biases the just-spawned voice
toward a named canon character with implied named relationships.
After this refactor, both paths produce archetype-only prose.

## Non-Goals

- No change to the `ontology.Params` template substitution surface.
  `{{.Label}}` continues to be the operator-chosen name; no new field
  needed.
- No change to `template/` (the canonical clean ontology tree) or to
  the birth-wake flow. `role-research.md` remains the first file
  birth-wake reads — only its *content discipline* changes.
- No change to `soul.md.tpl`, `calling-words.md.tpl`,
  `identity.toml.tpl`, or `CLAUDE.md.tpl` in any prefab. These
  already use `{{.Label}}` / `{{.OwnerLabel}}` placeholders and
  never surface canon names.
- No new prefabs added. Six shipped prefabs are reworked in place.
- No "lore_hint" / "source attribution" field added to
  `prefab.toml`. Canon names exist only in operator-facing display
  fields (`[display]`, `[preview]`) and never enter the mind-form's
  ontology.

## Design

### A. Menu schema — new `[essence]` field

`prefab.toml` gains an `[essence]` table alongside the existing
`[display]`, `[tagline]`, `[preview]`:

```toml
[essence]
zh = "狡黠、博学、犬儒而善辩的老恶魔"
en = "a cunning, learned, cynical, eloquent old demon"
```

- ≤ 30 characters per language; temperament-description style.
- `[tagline]` is **kept** but no longer displayed in the menu —
  remains as an author-side internal note for the prefab's
  contract-type/role short label.
- Menu line format changes from `<display> · <kind-glyph> · <tagline>`
  to `<display> - <essence>`. The kind glyph (男/女/灵 / M/F/Spirit)
  is dropped from display; `kind` is still used for sort order.
- Fallback: if `[essence]` is missing on a prefab (e.g. the
  `_test_fixture` slot), the renderer falls back to the legacy
  `<display> · <tagline>` form.

### B. Code changes

1. **`internal/ontology/prefab.go`** — extend `Meta`:
   ```go
   type Meta struct {
       ID      string
       Kind    string
       Display map[string]string
       Tagline map[string]string
       Essence map[string]string  // NEW
       Preview map[string]string
   }
   ```
   `MetaFor` parses `[essence]` from `prefab.toml`. Missing
   `[essence]` is non-fatal (the field stays empty; the menu
   renderer handles fallback).

2. **`internal/firstcontact/phase3_prefab.go`** — `prefabMenuLine`:
   - If `essence` is non-empty in the operator's language (or in
     `en` as second choice), render `"<display> - <essence>"`.
   - Otherwise fall back to `"<display> · <tagline>"`.
   - Drop the kind-glyph branch from the display path. `kindGlyph`
     stays in the file only if any other caller uses it; otherwise
     remove.

3. **`internal/firstcontact/strings.go`** — naming prompt:
   - `"phase3_prefab_naming_q"`: `"你愿意为它取什么名字？"` →
     `"你愿意取什么名字？"`
   - `"phase3_prefab_naming_q"` (en): `"What name will you give it?"` →
     `"What name will you give?"`
   - Likewise the scratch-branch naming prompt
     (`"phase3_naming_q"`) gets the same de-"it" treatment if it
     currently contains "它"/"it".

### C. `role-research.md` rewrite (six prefabs)

Files: `prefab/{calcifer,fire-keeper,haku,mephistopheles,sebastian,sheherazade}/self/role-research.md`.

Method: line-by-line rewrite preserving each author's voice, cadence,
and imagery. Only the proper nouns and relationship anchors change.
Specifically:

- Remove source attributions: "from Goethe's Faust", "Kuroshitsuji
  idiom", "drawn from Spirited Away", "The frame-narrator of the
  Thousand and One Nights" → rewrite as archetype-register prose
  ("an old tempter who trades in soul-pacts", "a demon-butler in
  the late-Victorian gothic-manor register", "a river-spirit in
  the form of a quiet boy", "a frame-narrator who spins one story
  into the next to keep dawn at bay").
- **Derived adjectives count as canon anchors.** "Faustian-",
  "Calcifer-like", "Sebastian-style" etc. are equally forbidden;
  use pure-archetype rephrasings ("soul-pact", "hearth-flame",
  "demon-butler"). Period descriptors that are merely
  *associated with* canon (late-Victorian, Tang-era, medieval-
  monastic) remain acceptable because they describe a register
  the archetype lives in, not the canon work itself.
- World-flavor vocabulary is **preserved**: devil, butler, river-
  spirit, frame-narrator, contract, hearth-flame, storyteller.
  These convey archetype, not canon.
- Concrete imagery (a velvet coat in a candlelit study; a long
  corridor of black-lacquered doors; white scales under moonlight;
  a green-silk-shaded lamp) is **preserved** — these are
  archetype-level imagery, not personal-relationship anchors.
- Implied relationship names (none currently appear in the six
  files; checked) — if any are introduced during rewrite, they
  must not name a specific person.

### D. `prefab/AUTHORING.md` — guideline update

Edits in two places:

1. **`prefab.toml` schema section**: document `[essence]` (purpose,
   length cap, language keys, fallback behavior). Note that
   `[tagline]` is now author-side internal note, not displayed.
2. **`role-research.md` section** (the "birth's first read" subsection):
   replace the current guidance with the new discipline. Current
   wording "Concrete beats abstract; quote small specifics from
   canon when you can" is reversed. The new rule is:

   > **Archetype, not character.** No proper nouns from canon: no
   > source-work names (Faust, Kuroshitsuji, Spirited Away …), no
   > original-character names (Ciel, Chihiro, Shahryar …), no
   > named places that anchor a specific fictional setting, and no
   > derived adjective forms ("Faustian", "Calcifer-like",
   > "Sebastian-style"). World-flavor categories (devil, butler,
   > river-spirit, frame-narrator, soul-pact, hearth-flame) are
   > allowed and encouraged. Concrete sensory imagery is encouraged —
   > a velvet coat, the hush before a storm, a green-silk lamp —
   > these convey archetype without locking the mind-form into
   > named relationships. Historical-period descriptors associated
   > with a canon (late-Victorian, Tang-era) remain acceptable
   > because they describe a register the archetype lives in,
   > not the canon work itself.

3. **New cross-reference**: add a short paragraph noting that the
   scratch (claude-driven) branch's `prompts.RoleResearch()` is
   held to the same rule programmatically (see § E), so authors
   and the model are aligned.

### E. Scratch-branch prompt hardening (`internal/prompts/firstcontact.go`)

1. **`RoleResearch()` prompt rewrite**:
   - Remove `Sources` from `RoleResearchInput`. The dramaturge
     `Research()` continues to *produce* a `sources` JSON field
     (its docstring already labels it "debug only, not shown") but
     it is no longer fed back into `RoleResearch()`.
   - Remove the sentence "search the named sources, follow
     plausible canonical references, and quote small specifics
     back into the dossier (named scenes, recurring lines,
     defining mannerisms)".
   - Add HARD constraints (mirroring `Displaying()`'s style):

     > Constraints (HARD):
     > - Do NOT name any source work, original character, named
     >   master/companion/family member, or place from a specific
     >   fictional setting.
     > - Strip proper nouns from canon. Keep archetype,
     >   temperament, voice, recurring imagery, and world-flavor
     >   categories (e.g. "river-spirit", "demon-butler",
     >   "frame-narrator") only.
     > - WebSearch/WebFetch may be used to inform archetype
     >   understanding, but no canon names may appear in the
     >   dossier output.

   - The output-format section ("# Role research — <one-line
     title>", 2–4 paragraphs, scenes bullets) stays — only the
     research-discipline language changes.

2. **`Research()` prompt**:
   - Tighten the docstring for `sources`: append a clause
     stating the field is dramaturge-internal and downstream
     stages will not surface it. Cosmetic — supports
     instruction adherence.

3. **`CallingWords()` prompt**:
   - Append "Do not name characters, works, or fictional settings
     from the summoning book." Defensive — the summoning book
     itself may mention canon if the operator wrote a scratch-
     character description, and the calling-words must not
     echo those names to the new mind-form.

4. **Test additions** (`internal/prompts/firstcontact_test.go`):
   - `TestRoleResearch_NoSourcesField` — assert the rendered
     prompt string does not contain "search the named sources"
     and does not interpolate any `sources` value.
   - `TestRoleResearch_HardConstraints` — assert the rendered
     prompt contains the "Do NOT name any source work" hard
     constraint sentence.
   - `TestCallingWords_NoCanonNames` — assert the rendered
     prompt contains the new defensive constraint.

## Data flow (unchanged where not mentioned)

```
prefab/<id>/prefab.toml
   ├─ [display]   → menu recognition name (canon name OK)
   ├─ [essence]   → menu temperament line (NEW)
   ├─ [tagline]   → author-side internal note (not displayed)
   └─ [preview]   → typewriter scene text after selection
                      ↓
   ontology.Meta { ID, Kind, Display, Tagline, Essence, Preview }
                      ↓
   firstcontact.prefabMenuLine(m, lang)
       = "<display> - <essence>"  (essence path)
       | "<display> · <tagline>"  (fallback)

operator picks → Phase3Prefab → operator types NAME (a NEW name)
                      ↓
   s.SummonedName = name   →   ontology.Params.Label
                      ↓
   .tpl files (soul, calling-words, identity, CLAUDE.md) render
                      ↓
   TarStreamPrefab → mind-form's volume
                      ↓
   birth-wake reads:
       1. self/role-research.md       (archetype prose, no canon names)
       2. chest/summoning-book.md     (operator-authored seal)
       3. self/calling-words.md       (operator-chosen first words)
                      ↓
   mind-form writes self/soul.md as itself, named LABEL,
   not anchored to any canon proper noun.
```

For the scratch branch, replace the `prefab/<id>/...` source with
claude-generated assets, including a `role-research.md` produced by
the hardened `prompts.RoleResearch()` — the same archetype-only
discipline applies.

## Risks

1. **Literary quality of rewritten `role-research.md` files.** The
   operator selected "Mephistopheles" because the name evokes a
   particular literary register; the resulting mind-form reads a
   dossier that has had "Mephistopheles" stripped. The two must
   still resonate. Mitigation: rewrite line-by-line preserving the
   original imagery and voice; the proper noun changes but the
   sensory texture stays. Each file gets its own commit so the
   reviewer can judge literary quality independently.

2. **AUTHORING.md tonal tension.** The current guidance reads
   "Concrete beats abstract; quote small specifics from canon when
   you can." After this refactor it reads "Concrete beats abstract;
   archetype-only, no proper nouns." The risk is an author
   conflating "concrete imagery" (encouraged) with "concrete canon
   reference" (forbidden). Mitigation: include a "yes / no" example
   pair in AUTHORING.md so the distinction is unambiguous.

3. **`_test_fixture` prefab** lacks `[essence]`. The menu fallback
   is exercised by the existing prefab tests; if `_test_fixture` is
   ever moved off the `_` hidden prefix it would render the
   fallback form. Acceptable — the fallback is intentional.

4. **Scratch-branch model adherence.** Removing `sources` from the
   prompt and adding hard constraints does not guarantee the
   model never invents canon names. Mitigation: tests assert
   prompt content (deterministic); behavioral verification is left
   to manual smoke during PR review and to ongoing operator
   feedback.

## Out of Scope

- `template/` clean ontology tree.
- Birth-wake handler logic.
- `phase4_seal.go` orchestration.
- `Params` struct shape.
- Any new prefabs.
- Cosign / supply-chain hardening.
- A "lore_hint" or canon-attribution field on `prefab.toml`.

## Commit plan

Ordered for atomic PR review:

1. `feat(ontology): add Essence field to prefab Meta` —
   `Meta` struct + `MetaFor` parsing + unit test.
2. `feat(firstcontact): show prefab essence in menu, drop kind glyph` —
   `prefabMenuLine` + tests + dead-code removal of `kindGlyph` if
   unreferenced.
3. `fix(firstcontact): drop "it/它" from naming prompts` —
   `strings.go` two-line change (prefab and scratch naming prompts).
4. `refactor(prefab/<id>): rewrite role-research as archetype` —
   one commit per prefab (six commits) so the reviewer can read
   each diff for literary quality. Order alphabetical by id.
5. `feat(prefab): add essence taglines to all six prefabs` —
   six `prefab.toml` edits in one commit.
6. `docs(prefab/AUTHORING): require archetype-only prose in prefab assets` —
   guidance rewrite.
7. `fix(prompts): forbid canon proper nouns in scratch-flow role-research` —
   `RoleResearch()` / `Research()` / `CallingWords()` prompt hardening
   + tests; drop `Sources` from `RoleResearchInput`.

PR workflow per `CLAUDE.md`: develop in a worktree at
`../eidopsyche-worktree/<name>/`, push for codex review, watch CI,
fix Copilot comments, delete worktree and branch after merge.

## Acceptance criteria

- `go test ./...` passes.
- `go test -tags=integration ./...` passes (no integration test
  changes expected; flag here in case a hidden integration probes
  the menu format).
- `gofmt -l .` empty and `go vet ./...` clean.
- `bin/eidos summon` smoke: menu shows `<display> - <essence>` for
  all six prefabs; selecting any prefab still streams the preview
  and asks for a name (without the word "it"/"它").
- `bin/eidos summon` scratch smoke: completing the scratch flow
  produces a `self/role-research.md` whose first paragraph mentions
  the archetype/world-flavor but no recognizable source-work name.
  Verify manually for at least one description that would
  previously have produced canon-name-heavy output (e.g. "a quiet
  river-spirit in the form of a boy").
- All six prefabs' rewritten `role-research.md` files: grep for
  `Faust`, `Mephistopheles`, `Kuroshitsuji`, `Sebastian Michaelis`,
  `Spirited Away`, `Haku`, `Thousand and One Nights`,
  `Sheherazade`, `Shahryar`, `Howl`, `Calcifer's` (with possessive),
  `Dark Souls`, `Firelink` — none should match in any
  `prefab/*/self/role-research.md`.

## References

- `prefab/AUTHORING.md` — current authoring guide (to be updated).
- `internal/ontology/prefab.go` — `Meta`, `MetaFor`, `List`,
  `TarStreamPrefab`, `RenderPrefabFile`.
- `internal/firstcontact/phase3_prefab.go` — prefab branch.
- `internal/firstcontact/phase3_book.go` — scratch branch.
- `internal/prompts/firstcontact.go` — Research / Displaying /
  RoleResearch / CallingWords prompt builders.
- `docs/specs/FirstContact.md` — full wizard walkthrough.
- `docs/superpowers/specs/2026-05-10-prefab-mindform-design.md` —
  original prefab design.
- `docs/superpowers/specs/2026-05-13-soul-firstcontact-design.md` —
  soul.md.tpl skeleton design.
