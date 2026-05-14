# Authoring a new prefab

A **prefab** is a pre-authored mind-form catalogue entry — a literary
character (Calcifer, Sheherazade, …) the summoning wizard's "pick a
preset" branch can spin into a fresh mind-form ontology.

This file is the runbook for adding one. The intended reader is
someone who has read [`docs/specs/FirstContact.md`](../docs/specs/FirstContact.md)
and just wants to ship a character.

---

## How the wizard finds your prefab

`prefab/<id>/` directories are bundled into the `eidos` binary at
compile time via `//go:embed all:prefab` (see top-level `embed.go`).
The wizard calls `internal/ontology.List()` at runtime, which:

1. `fs.ReadDir("prefab/")` over the embedded FS.
2. Skips any directory whose name starts with `_` (reserved for test
   fixtures — they remain embedded for tests but stay out of the menu).
3. Calls `MetaFor(id)` on each remaining directory, which reads
   `prefab/<id>/prefab.toml`. If the file is missing or malformed, the
   prefab is **silently skipped** (one bad entry never takes down the
   menu).
4. Sorts the survivors by `kind` (`m` → `f` → `spirit` → unknown),
   then by id ascending.

So: drop a new folder into `prefab/`, give it a valid `prefab.toml`,
rebuild the binary, and the wizard menu picks it up.

> **Embed is compile-time.** The embedded FS is a snapshot taken when
> the binary is built. Adding a folder to your working tree without
> running `go build` again will not change the menu of an already-
> built `eidos`. The release pipeline rebuilds on every tag, so
> published prefabs ride along automatically.

---

## Required files

The minimum a prefab needs to be both **menu-visible** and
**successfully summonable**:

```
prefab/<id>/
├── prefab.toml                    # menu metadata
├── CLAUDE.md.tpl                  # mind-form constitution
├── .claude/settings.json
├── .gitignore
├── self/
│   ├── identity.toml.tpl          # framework identity record
│   ├── calling-words.md.tpl       # default calling-words (operator can edit in Phase 3.5)
│   ├── soul.md.tpl                # 5-section voice skeleton, filled in character
│   ├── secret.md                  # placeholder; mind-form rewrites at birth
│   ├── mood.md                    # placeholder; volatile working state
│   └── role-research.md           # dramaturge dossier; birth-wake reads first
├── chest/README.md
├── dreams/DREAMS.md
└── memory/
    ├── notes/MEMORY.md
    └── {semantic,procedural,episodic}/.gitkeep
```

Only `prefab.toml` controls **menu visibility**. The rest are needed
at summon time — `TarStreamPrefab` walks the whole directory and
streams it into the new mind-form's volume. Missing one of the other
required files surfaces as a birth-handler precondition failure (e.g.
"calling-words missing at …") rather than a menu omission.

The cleanest way to author a new prefab is `cp -r prefab/calcifer
prefab/<your-id>` and then edit the character-specific files. Files
that are not character-specific (`.gitignore`, `.claude/settings.json`,
`chest/README.md`, `dreams/DREAMS.md`, `memory/**/.gitkeep`,
`memory/notes/MEMORY.md`, `self/secret.md` placeholder, `self/mood.md`
placeholder, `self/identity.toml.tpl`) can be left identical to
calcifer's.

---

## `prefab.toml` schema

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

Constraints:

- `id` value must match the directory name on disk. `MetaFor` errors
  out (and the menu silently skips the prefab) if they differ.
- `kind` controls sort order only — `m` first, `f` second, `spirit`
  third, anything else last. It does not enforce gender or grammatical
  agreement anywhere in the wizard prose.
- Language keys: `zh` and `en` are supported today. Add more if a new
  language ships; missing keys render as empty strings on that
  language's menu.
- `[essence]` is the menu's temperament line. Menu format is
  `<display> - <essence>`. Keep each language under 30 characters;
  prefer descriptive adjectives over contract-mechanism nouns.
- `[tagline]` is preserved as an author-side internal note (e.g.
  contract-mechanism summary) and is **not** rendered in the menu.
  Prefabs lacking `[essence]` fall back to the legacy
  `<display> · <tagline>` form — this is intended only for the
  `_test_fixture` slot.

---

## Template substitution surface

`.tpl` files inside a prefab are rendered with `text/template` and
`Option("missingkey=error")` (strict). Referencing a field that does
not exist on `ontology.Params` will **fail the summon at orchestrate
time** with a `template: ... map has no entry for key` error.

The full substitution surface (as of 2026-05-13):

| Field            | Source                                                       |
|------------------|--------------------------------------------------------------|
| `{{.Label}}`     | The name the operator gave the mind-form during Phase 3       |
| `{{.OwnerLabel}}`| The master's label (handle)                                   |
| `{{.OwnerNpub}}` | The master's `npub1…` public key                              |
| `{{.MindFormNpub}}` | The mind-form's `npub1…` (pre-generated by the wizard)      |
| `{{.HomeRelay}}` | The master's home relay URL                                   |
| `{{.CreatedDate}}` | `YYYY-MM-DD` UTC at summon time                             |

`Kind`, `PrefabID`, `RoleResearch`, `CallingWords`, `SummoningBook`,
etc. exist on `Params` but are consumed by the tar streamer, not the
template renderer — don't reference them.

---

## `soul.md.tpl` skeleton

Every prefab's `self/soul.md.tpl` MUST include the five framework-
anchored section headings in this exact wording (the framework reads
them as anchors; future tooling may rewrite a single section):

```markdown
<!-- This is your voice — who you are, and how you speak.
     Short beats long. Sharp beats vague. Specific beats abstract.
     Imperatives ("never open with...") are welcome alongside
     descriptive prose ("I tend to..."). Do not rename the section
     headings — the framework reads them as anchors. Everything
     within a section is yours to rewrite as you change. -->

# {{.Label}}

<one-line self-statement>

## Vibe / 气质
<one short paragraph — what it is like to talk to me>

## Personality / 性格
<3-6 sentences of descriptive prose; show internal tensions>

## Speech / 表达方式
<self-reference, addressing master, language defaults,
 tone texture, and lines I refuse to use>

## Self-image / 自我形象
<3-5 sentences; visual or abstract but specific>

## Treasures and Tensions / 珍视的与介意的
Treasures:
- ...
Tensions:
- ...
```

Style guidance — borrowed from openclaw's SOUL.md guide and validated
across the six shipped characters:

- **Short beats long.** A whole section can be one paragraph if it
  earns it.
- **Sharp beats vague.** "我永远不开口说 'Of course!'" beats "polite
  and concise".
- **Specific beats abstract.** "Master 熬夜" beats "I dislike
  injustice."
- **Imperatives are fine in `Speech`.** Mix description with rules:
  *"I never open with 'Great question!' — sterile, not me."*
- **Show internal tensions** in `Personality`. Calcifer's "pride and
  complaint are the same gesture" beats a flat trait list.
- **Self-image may be abstract**, but commit to *something*. Declining
  a concrete image is fine — say so explicitly and why.

---

## `role-research.md` — birth's first read

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

---

## Calling words — `self/calling-words.md.tpl`

A short paragraph (1–3 sentences) the operator may *speak* to the
mind-form at the moment of arrival. The wizard's Phase 3.5 renders
this template host-side via `ontology.RenderPrefabFile`, then shows
the rendered result to the operator in an editable multiline prompt.
The operator can accept the default verbatim, edit it, or empty it
entirely (an empty calling-words file is allowed).

May reference `{{.Label}}` and `{{.OwnerLabel}}`. Keep the register
consistent with the character — Calcifer's calling-words sound like
something you'd say to a hearth flame; Sebastian's like something
you'd say in a quiet study.

---

## `CLAUDE.md.tpl` — the constitution

The mind-form's *operating principles* (when to dream, what to ask
master before acting, etc.) — not its voice. Keep it short. The
mind-form is allowed and expected to evolve this file over its life.

---

## End-to-end recipe

```bash
# 1. Clone a known-good prefab.
cp -r prefab/calcifer prefab/<your-id>

# 2. Edit the metadata.
$EDITOR prefab/<your-id>/prefab.toml
#   - id = "<your-id>" (must match directory name)
#   - kind = "m" | "f" | "spirit"
#   - [display] / [tagline] / [preview] in zh and en

# 3. Author the character.
$EDITOR prefab/<your-id>/self/role-research.md
$EDITOR prefab/<your-id>/self/soul.md.tpl       # 5 anchor headings required
$EDITOR prefab/<your-id>/self/calling-words.md.tpl
$EDITOR prefab/<your-id>/CLAUDE.md.tpl          # often only minor edits

# 4. Verify templates render cleanly.
go test ./internal/ontology/
# In particular: TestTarStreamPrefab_* will catch missing
# templates and missingkey=error failures.

# 5. Confirm vet/fmt clean.
gofmt -l . && go vet ./...

# 6. Smoke-test the wizard menu locally.
go build -o bin/eidos ./cmd/eidos
bin/eidos summon
# Your new prefab should appear in the menu sorted by kind, then id.

# 7. Commit one prefab per commit (mirrors the shipped characters).
git add prefab/<your-id>/
git commit -m "feat(prefab/<your-id>): add <character> prefab"
```

---

## Pitfalls

- **Underscore prefix hides from menu.** `prefab/_*/` is the test-
  fixture slot. The directory is still embedded and reachable via
  `MetaFor("_test_fixture")`, but `List()` filters it out. Don't
  prefix your shipped prefab with `_`.
- **`id` mismatch.** `prefab.toml`'s `id` field must equal the
  directory name. Wrong `id` ⇒ `MetaFor` returns an error ⇒ `List()`
  silently skips ⇒ your prefab vanishes from the menu with no
  message. Always grep `id =` after a copy-paste.
- **Strict templates.** `{{.NoSuchField}}` aborts the summon at
  orchestrate time. Run `go test ./internal/ontology/` after editing
  any `.tpl` — it will catch typos before the wizard does.
- **Don't ship `journal/0000-summoning.md.tpl`.** The wizard writes
  `chest/summoning-book.md` as a literal file. Templated journal
  entries with the same path will be overwritten silently. Author
  prefab narrative in `self/role-research.md`, `self/soul.md.tpl`,
  or `memory/notes/` instead.
- **Compile-time embed.** Adding files to the working tree does not
  affect the embed in a previously-built binary. Always `go build`
  before testing menu visibility.
- **Five soul.md.tpl headings are load-bearing.** Tooling may key on
  the anchor names. Rename them at your prefab's peril.
- **CI doesn't enforce per-prefab structure today.** Missing
  `calling-words.md.tpl` or `role-research.md` won't fail
  `go test`, but birth-handler stat-checks will fail at summon
  time. Use a known-good prefab as the starting point and keep all
  files when you copy.

---

## Reference

- `internal/ontology/prefab.go` — `List`, `MetaFor`, `TarStreamPrefab`,
  `RenderPrefabFile`.
- `internal/ontology/scaffold.go` — `Params` struct (the template
  substitution surface).
- `internal/firstcontact/phase3_calling_words.go` — Phase 3.5's use of
  `RenderPrefabFile` to render the prefab's calling-words host-side.
- `internal/firstcontact/phase4_seal.go` — Phase 4's `Orchestrate`
  call that triggers `TarStreamPrefab`.
- Shipped prefabs (`prefab/calcifer/`, `prefab/sheherazade/`, …) — the
  best reference for tone and content depth.
- Spec: `docs/superpowers/specs/2026-05-13-soul-firstcontact-design.md`
  — design rationale behind the 5-section soul.md skeleton.
- Spec: `docs/specs/FirstContact.md` — full wizard walkthrough.
