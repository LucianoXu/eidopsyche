# Prefab Mind-form Templates — Design

**Status:** approved 2026-05-10
**Owner:** wizard / scaffold layer
**Affected packages:** `internal/firstcontact`, `internal/ontology`, `cmd/eidos/forge`, plus new top-level `template/` and `prefab/` directories.

## 1. Problem

Today every mind-form is summoned through Claude-Code-driven dramaturgy (Phase 3 character question → research → "displaying" paragraph; Phase 4 calling-words generation). The flow is rich but slow, requires `claude` on PATH, and produces non-deterministic output — making it awkward for both fast onboarding and repeatable debugging.

We want a parallel "pick a preset" path. The operator chooses a pre-authored character from a small catalogue, gives it an instance name, and we materialise it without any LLM round-trip. The existing AI flow stays as the "from scratch" path, untouched in semantics.

The six initial prefab characters come from `NOTEBOOK.md`:

| Kind | Male view | Female view | Spirit |
|---|---|---|---|
| | Mephistopheles · Sebastian Michaelis | Fire Keeper · Sheherazade | Calcifer · Haku |

## 2. Goals / non-goals

**Goals**
- New "scaffold source" branch in the wizard between master selection and the book stage. User picks Scratch (existing AI) or Prefab.
- Prefab path runs without `claude` on PATH.
- Six self-contained prefab ontology trees ship embedded in the binary, with metadata for menu rendering.
- A new low-level entry point produces a tar stream from a prefab id, applying the same substitutions (`Label`, `OwnerNpub`, `CreatedDate`, plus a few additions).
- Move the canonical ontology template out of `internal/ontology/template/` to a top-level `template/` directory; the `prefab/` directory is its sibling.

**Non-goals (explicit)**
- No prefab inheritance / overlay merging. Each prefab is a complete tree.
- No third-party prefab installation. Prefabs are compiled in.
- No prefab versioning / pinning.
- No CI check that prefab `CLAUDE.md` files stay in sync with `template/CLAUDE.md`. Author drift is accepted until it bites.

## 3. Repo layout

```
eidopsyche/
├── embed.go                  # NEW — package eidopsyche, holds //go:embed
├── template/                 # MOVED from internal/ontology/template/
│   ├── CLAUDE.md
│   ├── self/identity.md.tpl, self/values.md
│   ├── memory/{episodic,procedural,semantic,mood.md}
│   ├── essence/, journal/, desk/, drawer/
│   └── .claude/settings.json, .gitignore
├── prefab/                   # NEW
│   ├── mephistopheles/
│   │   ├── prefab.toml       # metadata + display + preview text
│   │   ├── CLAUDE.md         # may differ from template/CLAUDE.md
│   │   ├── self/identity.md.tpl
│   │   ├── self/values.md
│   │   ├── essence/calling-words.md.tpl
│   │   ├── journal/0000-summoning.md.tpl
│   │   ├── memory/...
│   │   └── desk/, drawer/, .claude/, .gitignore
│   ├── sebastian/
│   ├── fire-keeper/
│   ├── sheherazade/
│   ├── calcifer/
│   └── haku/
└── internal/
    └── ontology/
        ├── embed.go          # re-exports root package's embedded FS
        ├── scaffold.go       # Scaffold + TarStream (template path) — unchanged signatures
        └── prefab.go         # NEW — TarStreamPrefab, List, MetaFor
```

### 3.1 Why a top-level Go file

`//go:embed` paths cannot use `..`, so the embed declaration must live in a package whose directory contains the assets. A small `embed.go` at the module root (`package eidopsyche`) is the only way to keep `template/` and `prefab/` literally at the top while still being embeddable.

### 3.2 Why `prefab.toml` lives alongside the ontology

Each prefab is a "complete self-contained ontology tree" (user choice). Adding a sub-`ontology/` level would dilute that. The `prefab.toml` sidecar at the prefab root is excluded from the tar walk by name.

## 4. Wizard flow change

```
Phase 0 (open / lang)               unchanged
Phase 1 (identity)                   unchanged
Phase 2 (master source)              unchanged: Exit / SummonLocal / SummonCard
Phase 2.5 (scaffold source)          NEW
   ├ Scratch  →  Phase3Scratch  →  Phase4Scratch (existing claude flow)
   └ Prefab   →  Phase3Prefab   →  Phase4Prefab  (no claude calls)
```

### 4.1 Phase 2.5 (new)

Asked only when Phase 2 returns SummonLocal or SummonCard:

> 你想从头创造一个 Mind-form,还是从预设里挑一个?
>   1) 从头创造 (claude 实时塑造)
>   2) 选一个预设 Mind-form
>   3) 返回 / 退出

Returns one of `ScaffoldScratch | ScaffoldPrefab | Exit`. The exit option lets an operator who reached the menu by accident bow out without aborting the program.

`EntryGateInit` short-circuits to `Phase2Exit` before Phase 2.5 ever runs (unchanged).

### 4.2 Readiness split

Today `EnsureSummonReady` bundles `claude on PATH` + `docker daemon` + volume helpers. We split:
- **`EnsureSummonReady`** keeps the docker / volume-helper bits and is invoked after Phase 2 commits to summon (current call site).
- **`EnsureClaudeReady`** (new callback) ensures `claude` on PATH and populates `Deps.Claude`. Invoked **only** from the Scratch branch in Phase 2.5.

Outcome: an operator on a host with docker but no `claude` can still pick a prefab.

### 4.3 Phase 3 prefab branch

1. `prefab.List()` → 6 metas, ordered by kind (m → f → spirit).
2. Render menu:
   ```
   请选择一个预设 Mind-form:
     1) Mephistopheles · 男 · 恶魔灵魂契约
     2) Sebastian      · 男 · 管家式召唤
     3) 防火女         · 女 · 守 / 续命
     4) 山鲁佐德       · 女 · 夜夜为你讲一段
     5) Calcifer       · 灵 · 绑名元素灵
     6) 白龙           · 灵 · 失名与追寻本名
   ```
3. Operator picks. Wizard reads `Meta.Preview[s.Lang]` and typewriter-prints (same UX role as scratch's "displaying" paragraph).
4. Prompt summoned name; slug derived silently (`Derive(name, existingSlugs)`).
5. Populate `Summoning`:
   - `s.SummonedName` = entered name
   - `s.Slug` = derived slug
   - `s.Displaying` = prefab preview text (so the seal book renders normally)
   - `s.PrefabID` = chosen prefab id (new field)
   - `s.CharacterPrompt`, `s.Profile` left zero — unused on this path.

### 4.4 Phase 4 prefab branch

Re-uses the seal flow but skips claude work:
- **Skip `awaitReady`** for claude readiness — wait only for key generation.
- **Skip** `c.CallText(buildCallingWordsPrompt)`. Calling-words come from the prefab's templated `essence/calling-words.md.tpl`, baked into the volume by tar-stream.
- **Skip** the post-orchestrate `WriteVolume("ontology/essence/calling-words.md", words)` call.
- **Keep**: book preview + seal confirmation, `forge.Orchestrate` (with `PrefabID` set), claude-credentials install (`forge.InstallLoginFromHost` — required because the in-container claude is still the agent that wakes; the operator must have `claude /login`-ed on the host), `birth.json` write, container start, response wait, contact add.

The book shown at seal is `RenderSummoningBook(s)` — same renderer; populated `s.Displaying` keeps the output coherent.

## 5. Data model

### 5.1 `ontology.Params`

```go
type Params struct {
    // existing
    Label       string  // SummonedName
    OwnerNpub   string
    CreatedDate string

    // new — used only by prefab .tpl files; template/ ignores them
    OwnerLabel   string
    MindFormNpub string
    HomeRelay    string

    JournalEntry string  // unchanged — only set on scratch path
}
```

`MindFormNpub` is known by the time `forge.Orchestrate` runs (Phase 4 generates the key during readiness).

### 5.2 `firstcontact.Summoning`

```go
type Summoning struct {
    ...existing...
    PrefabID string  // empty for scratch path; set in Phase 3 prefab branch
}
```

### 5.3 `prefab.toml`

```toml
id   = "fire-keeper"
kind = "f"                # m | f | spirit

[display]
zh = "防火女"
en = "Fire Keeper"

[tagline]
zh = "守 / 续命"
en = "guarding / lengthening life"

[preview]
zh = """她在火边静静坐着……"""
en = """She sits quietly by the fire …"""
```

`id` must match the directory name. `kind` drives list ordering.

### 5.4 `forge.CreateOpts`

```go
type CreateOpts struct {
    ...existing...
    PrefabID string  // empty = template; non-empty = prefab/<id>/
}
```

`Orchestrate` branches once on this:
```go
streamFn := func(w io.Writer) error { return ontology.TarStream(w, params) }
if o.PrefabID != "" {
    streamFn = func(w io.Writer) error { return ontology.TarStreamPrefab(w, o.PrefabID, params) }
}
```

## 6. Low-level interface

Lives in `internal/ontology/prefab.go`:

```go
type Meta struct {
    ID      string
    Kind    string            // "m" | "f" | "spirit"
    Display map[string]string // lang → display_name
    Tagline map[string]string
    Preview map[string]string
}

func List() ([]Meta, error)                              // sorted by (kind, id)
func MetaFor(id string) (Meta, error)
func TarStreamPrefab(w io.Writer, id string, params Params) error
```

### 6.1 Internals

- `prefab.toml` is excluded from the tar walk by name (top-level skip per prefab).
- `.tpl` rendering reuses the existing `renderTemplate(body, params)` helper, with `Option("missingkey=error")` added so prefab template typos fail loud rather than ship blank substitutions.
- `TemplateFS()` (kept) and a new `PrefabFS()` are the embedded `fs.FS` accessors. The root `embed.go` holds the `//go:embed` directives; `internal/ontology/embed.go` becomes a thin re-export.
- Top-level prefab dirs starting with `_` are skipped by `List()` (so test fixtures embedded under `prefab/_test_fixture/` don't appear in user-facing menus).

### 6.2 Why this lives in `ontology`, not a new package

The package's doc comment already says it owns "the on-disk shape of a mind-form's essence and the scaffolding that produces it". Prefab is a second source of that scaffolding. Splitting would force every caller to import two packages for one logical action.

### 6.3 Scratch path is untouched

`Scaffold` and `TarStream` keep their signatures and behaviour. The only knock-on edit is `Params` gaining optional fields, which existing call sites (which pass them by name) don't notice.

## 7. Error handling

- **Prefab not found** at Phase 3: typed error; wizard re-prompts the menu. Practically only fires on a coding bug since the menu lists exactly what `List()` returned.
- **`prefab.toml` malformed** at startup: `List()` skips that prefab and logs a structured warning. Bad prefab does not take down the wizard.
- **`.tpl` parse / render failure** during `TarStreamPrefab`: bubbles up via `pipeW.CloseWithError`; `forge.Orchestrate` already surfaces this and rolls back the volume.
- **Missing required `Params` field** referenced by a prefab template: `text/template` `Option("missingkey=error")` raises at scaffold time.
- **Phase 4 prefab path response timeout**: same `purge()` rollback as today.

## 8. Testing

- `internal/ontology/prefab_test.go`:
  - `List()` returns the 6 prefabs in documented order.
  - `MetaFor("fire-keeper")` parses; required fields populated.
  - `TarStreamPrefab` for each id round-trips into a temp dir, expected files present, no `.tpl` suffix remains, substitutions applied.
  - `prefab.toml` is **not** in the tar.
  - `missingkey=error` triggers on a fixture prefab with an unknown field.
  - Top-level dirs starting with `_` are not listed but are tar-streamable when explicitly named (so fixtures still work).
- `internal/firstcontact/phase2half_scaffold_test.go`: prompt returns Scratch / Prefab / Exit correctly given fake renderer input.
- `internal/firstcontact/phase3_prefab_test.go`: list rendering, preview typewriter call, naming → `s.PrefabID` + `s.Slug` + `s.Displaying` populated.
- `internal/firstcontact/phase4_seal_test.go`: extend with a prefab-path subtest. Asserts no claude calls (fake `*Claude` panics if called), volume tar comes from prefab source, calling-words is **not** written via `WriteVolume` after orchestrate.
- `cmd/eidos/forge/orchestrate_test.go`: prefab id branch picks the right tar stream.
- A test fixture prefab `prefab/_test_fixture/` houses the round-trip and missingkey assertions.
- Six prefab content files: hand-authored. No automated test for text quality.

## 9. Migration

- `git mv internal/ontology/template/* template/` in one commit (preserves history).
- New top-level `embed.go` added in the same commit as the move; `internal/ontology/embed.go` updated to re-export.
- Six prefab content commits follow.
- Wizard / forge changes follow last (cleanest blame line per phase).
- No external API renaming; `ontology.TemplateFS()`, `ontology.Scaffold()`, `ontology.TarStream()` keep their names and signatures.

## 10. Out of scope (deferred)

- Prefab inheritance / overlay.
- `eidos prefab install <url>` — installing third-party prefabs.
- Prefab versioning / pinning.
- A CI lint that diffs each `prefab/<id>/CLAUDE.md` against `template/CLAUDE.md`.
- Multilingual prefab display in the menu when the operator's language is neither `zh` nor `en` (we fall back to `en`).
