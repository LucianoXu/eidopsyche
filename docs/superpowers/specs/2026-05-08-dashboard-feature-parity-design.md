# Dashboard feature-parity with the gate CLI — design

**Status:** draft, awaiting review
**Author:** Claude (brainstormed with Yingte)
**Date:** 2026-05-08
**Scope:** `internal/dashboard/`, `internal/daemon/dashboard_adapter.go`, `deploy-test/playwright/`
**Related specs:** `2026-05-07-dashboard-design.md` (current dashboard), `2026-05-07-mindgate-invites-design.md`, `2026-05-08-mindgate-auth-tls-relay-health-design.md`

## Goal

Bring the `eidos gate` dashboard webui to feature parity with the `eidos gate ...` CLI: every day-to-day and operator action that exists in the CLI gains a webui equivalent. Mobile/web thin-clients (SPEC §153, NIP-46) are explicitly **out of scope** and will get their own listener.

## Decisions captured up front

| | Choice | Rationale |
|---|---|---|
| **Scope** | Full operator surface (incl. destructive lifecycle: stop / purge / self-update) | Operator wants 1:1 parity with CLI; webui is the day-to-day surface and should not force shell-out for routine ops. |
| **Auth** | Loopback-only HTTP + same-origin guard (existing) + typed-confirm modal for irreversible actions | Mobile path is NIP-46 over Nostr (separate listener). Local trust boundary is identical to the CLI's; an extra bearer token would add friction without changing the threat model. |
| **Information architecture** | Single `⚙ Settings` sidebar entry → tabbed page (Identity / Contacts / Invites / Relays / Config / Service) | Keeps the chat surface uncluttered; isolates "operator mode" behind one entry. Familiar pattern (Slack / Discord / GitHub settings). |
| **Lifecycle implementation** | Daemon shells out to `eidos gate <subcmd>` (or `eidos self-update`); streams stdout via SSE | The CLI stays the single source of truth — no drift between the two surfaces. The dashboard does not import `internal/service` or `internal/update`. |
| **Phasing** | Five PRs: Identity+Settings shell+Config → Contacts → Invites → Relays → Service | Each phase ships an independently reviewable surface. Later phases can adjust based on usage feedback. |
| **DashboardDeps growth** | Linear growth (~14 typed methods added; ~28 total) | Matches today's pattern; KISS-friendly; refactor to service facades later iff it becomes painful. |

## §1 — Architecture overview

The dashboard remains a thin HTTP layer over the daemon's IPC.

```
browser (loopback + same-origin) ──HTTP/SSE──▶ internal/dashboard
                                                       │  (DashboardDeps interface)
                                                       ▼
                                                  internal/daemon
                                                    (dashboard_adapter)
                                                       │
                              ┌────────────────────────┼────────────────────────────┐
                              ▼                        ▼                            ▼
                    internal/contacts          internal/invitedb             internal/service
                    internal/store             internal/identity                (target of
                    internal/inbox             internal/config                  shell-out)
                                                                              internal/update
```

Three load-bearing properties hold:

1. **`internal/dashboard` never imports `internal/daemon`** — to keep the cycle broken. The daemon-side surface is reached via the `DashboardDeps` interface, with the concrete implementation in `internal/daemon/dashboard_adapter.go`.
2. **All persistent state is owned by existing internal packages.** `contacts`, `invitedb`, `store`, `config` already have the CRUD surface the CLI uses; the dashboard handlers route into the *same* daemon code paths the CLI uses (via existing IPC methods plus a few new ones). No new persistence layer.
3. **Lifecycle ops are the single exception** — the daemon exposes one new IPC method `LifecycleRun(args)` that forks `os.Args[0] gate <subcmd> --yes` and streams stdout/stderr lines back over IPC; the dashboard tails that stream into an SSE event the operator sees in real time.

What this **isn't**:
- Not a remote API: no token auth, no CORS, no listening beyond loopback. The mobile/NIP-46 path is explicitly a separate listener.
- Not a re-skin: the existing chat surface (sidebar contacts list, thread view, compose, relays panel, SSE) stays exactly as today. The new work is purely additive — one new sidebar entry, one new tabbed page.

## §2 — Route table

URL grammar: `/settings` is the shell with tab nav; sub-paths target sub-panes via htmx with `hx-push-url="true"` so browser back/forward works. POSTs use `sameOriginGuard` (already enforced); destructive POSTs additionally require a `confirm=<phrase>` form field (server-side defense-in-depth for §7).

```
existing (unchanged)
─────────────────────────────────────────────────────────────────────
GET   /                            shell
GET   /sidebar/contacts            sidebar refresh partial
GET   /messages                    all-messages list
GET   /thread/<pk>                 thread view
POST  /thread/<pk>/send            send-in-thread
GET   /compose                     compose-to-npub form
POST  /compose/send                send-via-compose
GET   /relays                      relays *panel* (right rail)
GET   /events                      SSE
GET   /static/*                    embedded assets


phase 1 — Identity + Settings shell + Config
─────────────────────────────────────────────────────────────────────
GET   /settings                    shell with tab nav (defaults to identity)
GET   /settings/identity           pane: label, npub, hex, card URI (copy)
POST  /settings/identity/label     change own label
GET   /settings/config             pane: KV table of every config key
POST  /settings/config             set one key (form: path, value)


phase 2 — Contacts
─────────────────────────────────────────────────────────────────────
GET   /settings/contacts           pane: list with edit affordances
GET   /settings/contacts/<pk>      pane: contact detail
POST  /settings/contacts/scan      preview card URI w/o storing
POST  /settings/contacts           add contact (form: card uri, label?)
POST  /settings/contacts/<pk>/label    rename
POST  /settings/contacts/<pk>/tier     change tier
DELETE /settings/contacts/<pk>     remove  ← typed-confirm


phase 3 — Invites
─────────────────────────────────────────────────────────────────────
GET   /settings/invites            pane: list (active / expired / exhausted)
POST  /settings/invites            create (form: redeemer-label, expires, max-uses)
POST  /settings/invites/<id>/revoke    revoke  ← typed-confirm
POST  /settings/invites/redeem     redeem (form: invite uri)


phase 4 — Relays
─────────────────────────────────────────────────────────────────────
GET   /settings/relays             pane: own relays + health (merges /relays data)
POST  /settings/relays             add (form: url, role)
DELETE /settings/relays/<idx>      remove  ← typed-confirm if role=home


phase 5 — Service control
─────────────────────────────────────────────────────────────────────
GET   /settings/service            pane: status + lifecycle action buttons
POST  /settings/service/reconnect  kick subscriber refresh   (safe, no confirm)
POST  /settings/service/stop       shell-out `eidos gate stop`     ← typed-confirm
POST  /settings/service/purge      shell-out `eidos gate purge`    ← typed-confirm (label)
POST  /settings/service/self-update    shell-out `eidos self-update` ← typed-confirm
```

Sidebar gets exactly **one** new entry: a `⚙ Settings` link beneath the Compose button. The right-rail relays panel keeps its current behaviour; clicking a row in the panel deep-links to `/settings/relays`.

## §3 — `DashboardDeps` additions

Pattern is the same as today: the dashboard package owns the interface; the daemon's `dashboardAdapter` implements it. New methods are listed by phase, grouped to match the `/settings/<tab>` panes. Types in `internal/...` are existing — no new value types are introduced for the dashboard.

```go
// internal/dashboard/deps.go (sketch of additions)

type DashboardDeps interface {
    // ... existing 8 methods unchanged ...

    // ─── phase 1: identity + config ──────────────────────────────────
    SetOwnLabel(ctx context.Context, label string) error
    OwnCardURI() (string, error)                     // mindgate://npub…@ws…/?label=…

    ConfigSnapshot() (config.Config, error)           // current effective config
    ConfigSet(ctx context.Context, path, value string) error  // dotted path

    // ─── phase 2: contacts ───────────────────────────────────────────
    GetContact(ctx context.Context, pubkey string) (*contacts.Contact, error)
    AddContact(ctx context.Context, cardURI, labelOverride string) (*contacts.Contact, error)
    RemoveContact(ctx context.Context, pubkey string) error
    SetContactLabel(ctx context.Context, pubkey, label string) error
    SetContactTier(ctx context.Context, pubkey string, tier contacts.Tier) error
    ScanCard(cardURI string) (ScanPreview, error)    // dry-run, no DB write

    // ─── phase 3: invites ────────────────────────────────────────────
    ListInvites(ctx context.Context, status string) ([]*invitedb.Invite, error)
    CreateInvite(ctx context.Context, opts InviteCreate) (*invitedb.Invite, string /*uri*/, error)
    RevokeInvite(ctx context.Context, idPrefix string) error
    RedeemInvite(ctx context.Context, inviteURI string) (RedeemResult, error)

    // ─── phase 4: own relays ─────────────────────────────────────────
    ListOwnRelays(ctx context.Context) ([]OwnRelay, error)
    AddOwnRelay(ctx context.Context, url, role string) error
    RemoveOwnRelay(ctx context.Context, url string) error

    // ─── phase 5: lifecycle ──────────────────────────────────────────
    Status() ServiceStatus
    LifecycleRun(ctx context.Context, args []string) (jobID string, err error)
    // No separate Reconnect() method: per the "shell out to eidos gate <subcmd>"
    // decision in the table at the top, the Reconnect button calls
    // LifecycleRun(["gate","reconnect"]) like every other lifecycle action.
    // The child eidos process then re-enters the parent over IPC, which is
    // wasteful but consistent — the CLI stays the single source of truth.
}

// Dashboard-local response shapes (no daemon coupling).
type ScanPreview struct { Pubkey, Label, Relay string; AlreadyContact bool }
type InviteCreate struct { IssuerLabel, RedeemerLabel string; Expires time.Duration; MaxUses int }
type RedeemResult struct { IssuerNpub, IssuerRelay string; AcceptedBy []string }
type OwnRelay     struct { URL, Role string; AddedAt int64 }
type ServiceStatus struct {
    Version, Commit, BuildDate string
    DaemonStarted              time.Time
    DashboardListen, IPCSocket string
    RelayEnabled               bool
    RelayListen                string
    RelayMode                  string
}
```

Each new method is a thin pass-through to existing internal packages. The adapter file grows ~150 lines and stays a single file.

## §4 — Templates and htmx patterns

New template files (under `internal/dashboard/templates/`):

```
templates/
├── settings.html                shell: tab nav + <div id="settings-pane">
├── settings_identity.html       phase 1
├── settings_config.html         phase 1
├── settings_contacts.html       phase 2
├── contact_detail.html          phase 2
├── contact_row.html             phase 2 (partial — list-row swap)
├── settings_invites.html        phase 3
├── invite_row.html              phase 3
├── settings_relays.html         phase 4 (full pane; right-rail panel unchanged)
├── relay_row.html               phase 4
├── settings_service.html        phase 5
├── lifecycle_log.html           phase 5
└── confirm_modal.html           common (typed-confirm — see §7)
```

Three htmx patterns the new pages standardise on (sketches; see implementation):

```html
<!-- 1. Tab nav inside /settings shell — swap pane only, push URL. -->
<nav class="tabs">
  <a hx-get="/settings/identity" hx-target="#settings-pane" hx-push-url="true">Identity</a>
  ...
  <a hx-get="/settings/service"  hx-target="#settings-pane" hx-push-url="true"
     class="tab-danger">Service</a>
</nav>
<section id="settings-pane">{{template "settings_identity" .Identity}}</section>

<!-- 2. List + row partial — every list pane uses this so add/edit/remove
     can OOB-swap a single row instead of redrawing the table. -->
<table class="rows"><tbody id="rows">
  {{range .Rows}}{{template "contact_row" .}}{{end}}
</tbody></table>
<form hx-post="/settings/contacts" hx-target="#rows" hx-swap="beforeend"
      hx-on::after-request="if(event.detail.successful){this.reset()}">
  <input name="card" placeholder="mindgate://…" required>
  <input name="label" placeholder="Optional override">
  <button type="submit">Add</button>
</form>

<!-- 3. Destructive action — load typed-confirm modal partial. See §7. -->
<button hx-get="/settings/contacts/{{.Pubkey}}/confirm-remove"
        hx-target="#modal" class="danger">Remove</button>
```

CSS additions are small (~60 lines into the existing `static/app.css`): `.tabs`, `.tab`, `.tab-danger`, `.confirm-modal`, `.lifecycle-log`, `.tier-pill`, `.danger-button`. **No new `.js` files** in `static/`. The only inline JavaScript ships inside templates as short `hx-on::*` handlers (≤80 chars per attribute) plus a single ~10-line `<script>` block inside `lifecycle_log.html` for the "SSE connection broken" banner (see §6.1) — anything bigger goes to a new template, not to a JS file. htmx + htmx-ext-sse stay the only `.js` we ship.

UI implementation is delegated to the `frontend-design:frontend-design` skill (per project preference) so the result is distinctive and production-grade, not generic AI styling.

## §5 — SSE event additions

Two existing patterns stay: **content-bearing** (event data is HTML to append, e.g. `inbox.message:<peer>`) and **signal-only** (event data is a stub the page reacts to with `hx-get`, e.g. `contact.added`).

We deliberately keep most new events **signal-only** because of the lesson from the recent dup bug — embedding HTML in SSE for things the originating tab also rendered locally creates a duplicate-render race.

```
content-bearing (HTML payload, appended via sse-swap)
─────────────────────────────────────────────────────────────────────
inbox.message:<peer_pk>       existing — unchanged
lifecycle.line:<job_id>       new (phase 5) — one stdout/stderr line
lifecycle.done:<job_id>       new (phase 5) — final marker (rc + status pill)


signal-only (payload "(refresh)", consumer does hx-get)
─────────────────────────────────────────────────────────────────────
contact.added                 existing
contact.removed               existing
contact.relabeled             existing
contact.tier-changed          new (phase 2)

relay.state                   existing — drives right-rail panel
relay.added                   new (phase 4)
relay.removed                 new (phase 4)

identity.label-changed        new (phase 1)
config.changed                new (phase 1)
invite.created                new (phase 3)
invite.revoked                new (phase 3)
invite.redeemed               new (phase 3) — also fires contact.added
service.status                new (phase 5)
```

Three rules from this taxonomy:

1. **For lists and panels, the SSE event is a signal; the consuming element holds the URL and re-fetches itself.** Same pattern as today's sidebar (`<aside hx-get="/sidebar/contacts" hx-trigger="sse:contact.added,...">`). The originating tab also receives the event and re-fetches — that's idempotent. No duplicate-render risk.
2. **Only the chat thread keeps the content-bearing pattern** for inbound bubbles; inbound has no local POST origin to compete with.
3. **Lifecycle output is content-bearing by necessity.** The shell-out streams stdout one line at a time; `<pre id="log-<jobID>" sse-swap="lifecycle.line:<jobID>" hx-swap="beforeend">` appends each line; `lifecycle.done` carries a small fragment (a coloured status pill) that is OOB-swapped into a sibling `<span id="status-<jobID>">`.

`internal/dashboard/sse.go::renderEvent` grows a `case` per new kind; `internal/daemon/dashboard_adapter.go::emitDashEvent` callers grow correspondingly. Events are emitted **after** the DB commit succeeds, never before.

## §6 — Lifecycle ops: subprocess spawn + line streaming

The daemon adds **one** new primitive — `LifecycleRun` — and four buttons drive it. Every button forks the same binary the daemon is already running (`os.Executable()`), so the CLI stays the single source of truth. No `internal/service` import in the dashboard.

```go
// internal/daemon/lifecycle.go
type lifecycleJob struct {
    id      string
    args    []string         // {"gate","stop"} or {"self-update"}
    cmd     *exec.Cmd
    started time.Time
    rc      int
}

// at most one in flight; second concurrent click returns 409.
func (d *Daemon) LifecycleRun(ctx context.Context, args []string) (jobID string, err error) {
    d.lifeMu.Lock()
    if d.activeLife != nil { d.lifeMu.Unlock(); return "", ErrLifecycleBusy }
    j := &lifecycleJob{ id: shortHex(8), args: args, started: time.Now() }
    d.activeLife = j
    d.lifeMu.Unlock()

    self, _ := os.Executable()
    j.cmd = exec.CommandContext(ctx, self, args...)
    stdout, _ := j.cmd.StdoutPipe()
    j.cmd.Stderr = j.cmd.Stdout

    if err := j.cmd.Start(); err != nil { /* clear activeLife, return */ }

    go d.pumpLifecycle(j, stdout)   // emits SSE per line, then done event
    return j.id, nil
}
```

`pumpLifecycle` does line-buffered reads off the pipe and emits `dashboard.Event{Kind: "lifecycle.line:<jobID>", HTML: <li>line</li>}` for each line. When `cmd.Wait()` returns, it emits `lifecycle.done:<jobID>` carrying a status pill (`rc=0` green / `rc=N` red) plus a `service.status` signal so the rest of the page resyncs.

The four buttons (Service tab):

| Button | Spawns | Confirm | Notes |
|---|---|---|---|
| Reconnect | `eidos gate reconnect` | none | child re-enters parent over IPC; harmless |
| Stop | `eidos gate stop` | type label | parent dies mid-stream — see §6.1 |
| Purge | `eidos gate purge --yes` | type label + checkbox | parent dies + state-dir wiped |
| Self-update | `eidos self-update` | type version | replaces binary on disk; running process stays old |

### §6.1 — When the parent kills itself

`gate stop` and `purge` cause the daemon to receive SIGTERM (via launchd/systemd) while the dashboard is still streaming. We don't fight it. UX:

1. POST returns `200` with `{ "job_id": "<id>", "kind": "<subcmd>" }` and the page swaps in a `<pre id="log-<id>">` plus a banner: *"Daemon will stop. Connection will drop when stop completes; reload after restart."*
2. Lines stream into `<pre>` via SSE.
3. The SSE connection breaks. A small handler (~10 lines of inline JS in `lifecycle_log.html`) flips the banner to *"Daemon stopped — restart from a terminal: `eidos gate start`"*.
4. For `purge`, the banner adds *"State directory removed. Run `eidos gate init` to start over."*

### §6.2 — Self-update specifically

`eidos self-update` re-execs `install.sh`, which writes a new binary at `~/.local/bin/eidos`. The running daemon process keeps the **old** binary mapped in memory until restart. So `lifecycle.done` for self-update emits a follow-up banner: *"Binary updated to vX.Y.Z. Restart the daemon to pick up the new code: `eidos gate stop && eidos gate start`."* No automatic re-exec — keeping it explicit is safer than hot-swapping a process that owns sockets, the SQLite WAL, and the SSE clients.

### §6.3 — Deliberately out of scope

- **`gate start`** — useless from inside a running daemon; omit the button.
- **Relay enable/disable** — deferred to Config tab + manual restart (toggle `relay.enabled`, then Stop, then `eidos gate start` from terminal). A future iteration can make this seamless once we have a clean reload-relay-only daemon entrypoint.
- **Job replay across reload** — if the user reloads mid-stream they lose the visible output but the subprocess still runs to completion. Reload-resilience can be added later if it actually hurts in practice.

## §7 — Typed-confirm modal (shared component)

One template, one route per destructive action, one server-side check. The user must type a specific phrase tied to the target — every destructive op here is irreversible.

```html
{{define "confirm_modal"}}
<div class="confirm-modal" hx-on::keydown="if(event.key==='Escape')this.remove()">
  <div class="backdrop" hx-on:click="this.parentElement.remove()"></div>
  <form class="confirm-card"
        hx-post="{{.Action}}" hx-target="{{.Target}}" hx-swap="{{.Swap}}">
    <h3>{{.Title}}</h3>
    <p>{{.Body}}</p>
    {{if .Warning}}<p class="warning">{{.Warning}}</p>{{end}}
    <label>Type <code>{{.ExpectedPhrase}}</code> to confirm</label>
    <input name="confirm" autofocus required autocomplete="off"
           hx-on::input="this.closest('form').querySelector('button.danger')
                              .disabled = (this.value !== '{{.ExpectedPhrase}}')">
    <div class="actions">
      <button type="button"
              hx-on:click="this.closest('.confirm-modal').remove()">Cancel</button>
      <button type="submit" class="danger" disabled>{{.ConfirmLabel}}</button>
    </div>
  </form>
</div>
{{end}}
```

```go
// internal/dashboard/handlers.go
type confirmModalData struct {
    Action, Target, Swap          string
    Title, Body, Warning          string
    ExpectedPhrase, ConfirmLabel  string
}
func requireConfirm(req *http.Request, expected string) error {
    if strings.TrimSpace(req.FormValue("confirm")) != expected {
        return fmt.Errorf("confirmation phrase mismatch")
    }
    return nil
}
```

Phrase per action:

| Action | ExpectedPhrase |
|---|---|
| Remove contact | `<contact label>` |
| Revoke invite | invite ID prefix (8 chars) |
| Remove home relay | the relay URL |
| `gate stop` | own label |
| `gate purge` | own label, AND a second checkbox "I have backed up state.db" |
| `self-update` | target version (e.g. `v0.6.1`) |

The handler for a non-daemon-killing destructive action returns a fragment that closes the modal **and** updates the affected element via OOB swaps in one round trip:

```html
<!-- response body for DELETE /settings/contacts/<pk> -->
<div id="modal" hx-swap-oob="innerHTML"></div>     <!-- close modal -->
<tr  id="row-{{.Pubkey}}" hx-swap-oob="delete"></tr>  <!-- drop row -->
```

For daemon-killing actions (`stop`, `purge`), the handler does *not* OOB-close the modal; it returns the lifecycle log fragment from §6 (which replaces the modal area with a streamed `<pre>` plus banner). When the SSE connection breaks because the daemon exited, the banner self-updates. The operator's eyes don't move.

CSS budget: `.confirm-modal { position: fixed; inset: 0; z-index: 50 }`, `.backdrop { background: rgba(0,0,0,0.4) }`, `.confirm-card { ... }`, `.warning`, `.danger`. ≈30 lines added. No new JS files — every interaction is `hx-on::*` inline; anything bigger goes to a new template, not a new JS file.

## §8 — Testing strategy

Three new test surfaces, one growing per phase.

```
internal/dashboard/
├── handlers_test.go      ← grows with each phase (one Test per route)
├── confirm_test.go       ← phase 1 (covers requireConfirm + modal renderer)
└── (existing render_test.go, sse_test.go grow with renderEvent cases)

internal/daemon/
├── lifecycle_test.go     ← phase 5 (subprocess streaming)
└── dashboard_adapter_test.go  ← grows with each phase (table-driven)
```

**Stub `DashboardDeps`** lives in `handlers_test.go`. Typed fields per method group; tests assert against them after the call. **Subprocess testing** uses an injected `spawner func(ctx, args) (*exec.Cmd, io.ReadCloser, error)` so tests run `/bin/sh -c "echo line1; echo line2; exit 3"` and assert the line stream + done event.

Per-phase tests (illustrative):

| Phase | Tests added |
|---|---|
| 1 | `TestSettingsShell`, `TestSettingsIdentity`, `TestPostLabel_{Valid,TooLong,Missing}`, `TestPostConfig_{Valid,InvalidPath}`, `TestRequireConfirm_{Match,Mismatch}` |
| 2 | `TestContactsList`, `TestAddContact_{ValidURI,InvalidURI,Duplicate}`, `TestRemoveContact_{NoConfirm,WrongPhrase,Success}`, `TestSetTier`, `TestScanCard_DryRun` |
| 3 | `TestInvitesList`, `TestCreateInvite_{ValidExpiry,BadExpiry}`, `TestRevokeInvite_RequiresConfirm`, `TestRedeem_{ValidURI,EmitsContactAdded}` |
| 4 | `TestRelaysList_MergesHealth`, `TestAddRelay`, `TestRemoveRelay_{HomeRequiresConfirm,NonHomeNoConfirm}` |
| 5 | `TestLifecycleRun_{StreamsLines,NonZeroExit,Concurrent409}`, `TestServiceReconnect`, `TestServiceStop_{NoConfirm,WithConfirm}`, `TestServicePurge_DoubleConfirm`, `TestServiceSelfUpdate` |

**Existing integration tests** (`test/integration/`) get one new file per phase covering the happy path against a real daemon: `dashboard_settings_phase{1,2,3,4,5}_test.go`. Build-tag `integration` so they don't slow down `go test ./...`.

Out of scope for tests:
- Visual regression / pixel diff — `frontend-design` will produce the look; baseline locks too soon.
- Cross-browser — Playwright runs Chromium; that matches the operator's actual browsers (mbp/selene).

## §8.5 — Deployment test (cross-machine, Playwright-driven)

Today `deploy-test/test_script_Alice_Bob.sh` covers a single-host alice/bob scenario via CLI only; `deploy-test/deploy-test-script.md` documents the cross-machine flow in prose. We turn that prose into a maintained, replayable artifact that grows alongside the dashboard surface:

```
deploy-test/
├── deploy-test-script.md            existing runbook — updated in phase 1 to
│                                    reference the new playwright/ directory
├── test_script_Alice_Bob.sh         existing single-host CLI test (unchanged)
└── playwright/                      ★ new
    ├── README.md                    env vars, SSH-tunnel recipe (mbp:22893→localhost),
    │                                Chromium binary path expectations
    ├── _lib/
    │   ├── chromium.mjs             reuse ~/.cache/ms-playwright; --no-sandbox
    │   └── dashboard.mjs            page-object: openTab, fillForm, expectRow,
    │                                expectBubbleArrived, dismissModal …
    ├── phase1-identity-config.mjs   one short script per pane: open, exercise
    ├── phase2-contacts.mjs          add → relabel → tier → remove (with confirm)
    ├── phase3-invites.mjs           create → list → revoke; redeem on the peer
    ├── phase4-relays.mjs            add own-relay → remove (home requires confirm)
    ├── phase5-service.mjs           reconnect; stop/purge/self-update gated by env
    └── full-bidirectional.mjs       canonical end-to-end on selene + mbp
```

Each phase PR ships its `phaseN.mjs` script alongside the code — not optional.

`full-bidirectional.mjs` is the canonical regression test:

```
1. purge both selene + mbp                 (CLI, SSH-shelled)
2. init both                               (CLI)
3. start services on both                  (CLI)
4. selene: invite create --redeemer mbp    (CLI; capture invite URI)
5. mbp: redeem <URI>                        (CLI)
6. webui-driven: selene → mbp send         (Playwright)
7. webui-driven: mbp → selene send         (Playwright via SSH tunnel)
8. assert: each side's inbox shows exactly one row, single bubble in DOM
```

Steps 1–5 stay shell-driven (matches deploy-test-script.md's "purge → init from scratch" requirement); 6–8 are the Playwright payload that this session debugged into existence (the recent double-bubble bug surfaced in seconds with Playwright). The script is parameterised by env (`SELENE_HOST`, `MBP_HOST`, `SSH_KEY`).

`deploy-test/deploy-test-script.md` is updated in phase 1 to:
- replace the prose checklist with a pointer to `playwright/full-bidirectional.mjs`;
- keep the high-level intent ("install latest build on both, purge, test all communication") at the top;
- list the env-var contract for the playwright runner.

## §9 — Phase-by-phase file deltas

Each phase is one PR, one merge, one playwright script committed alongside the feature, one updated `full-bidirectional.mjs`. The Settings tab nav reveals only the panes that ship — nothing greyed out.

### Phase 1 — Identity, Settings shell, Config

```
NEW
  internal/dashboard/templates/settings.html
  internal/dashboard/templates/settings_identity.html
  internal/dashboard/templates/settings_config.html
  internal/dashboard/templates/confirm_modal.html
  deploy-test/playwright/_lib/chromium.mjs
  deploy-test/playwright/_lib/dashboard.mjs
  deploy-test/playwright/phase1-identity-config.mjs
  deploy-test/playwright/full-bidirectional.mjs
  deploy-test/playwright/README.md

MODIFIED
  internal/dashboard/deps.go                              + 4 methods
  internal/dashboard/handlers.go                          + /settings, /settings/identity,
                                                            /settings/config, helpers
  internal/dashboard/handlers_test.go                     + stub fields + tests
  internal/dashboard/sse.go                               + identity.label-changed,
                                                            config.changed
  internal/dashboard/templates/sidebar.html               + ⚙ Settings entry
  internal/dashboard/templates/shell.html                 + <div id="modal">
  internal/dashboard/static/app.css                       + .tabs, .settings-pane,
                                                            .confirm-modal, .danger
  internal/daemon/dashboard_adapter.go                    + 4 method impls
  internal/daemon/methods.go                              + setLabel, configSet IPC
  internal/daemon/dispatch.go                             + dispatch entries
  deploy-test/deploy-test-script.md                       point at full-bidirectional.mjs

NEW INTEGRATION TEST
  test/integration/dashboard_settings_phase1_test.go      build-tag integration

EST. LOC                                                  ~600 added
```

### Phase 2 — Contacts editor

```
NEW
  internal/dashboard/templates/settings_contacts.html
  internal/dashboard/templates/contact_detail.html
  internal/dashboard/templates/contact_row.html
  deploy-test/playwright/phase2-contacts.mjs

MODIFIED
  internal/dashboard/deps.go                              + 6 methods
  internal/dashboard/handlers.go                          + /settings/contacts*
  internal/dashboard/handlers_test.go
  internal/dashboard/sse.go                               + contact.tier-changed
  internal/daemon/dashboard_adapter.go                    + 6 method impls
  internal/daemon/methods.go                              + setContactTier, scanCard IPC
  deploy-test/playwright/full-bidirectional.mjs           extend assertions

NEW INTEGRATION TEST
  test/integration/dashboard_settings_phase2_test.go

EST. LOC                                                  ~700 added
```

### Phase 3 — Invites

```
NEW
  internal/dashboard/templates/settings_invites.html
  internal/dashboard/templates/invite_row.html
  deploy-test/playwright/phase3-invites.mjs

MODIFIED
  internal/dashboard/deps.go                              + 4 methods
  internal/dashboard/handlers.go                          + /settings/invites*
  internal/dashboard/handlers_test.go
  internal/dashboard/sse.go                               + invite.{created,revoked,redeemed}
  internal/daemon/dashboard_adapter.go                    + 4 method impls
  internal/daemon/methods.go                              expose existing invite IPC
  deploy-test/playwright/full-bidirectional.mjs           replace CLI invite/redeem with
                                                            webui-driven flow

NEW INTEGRATION TEST
  test/integration/dashboard_settings_phase3_test.go

EST. LOC                                                  ~600 added
```

### Phase 4 — Relays

```
NEW
  internal/dashboard/templates/settings_relays.html
  internal/dashboard/templates/relay_row.html
  deploy-test/playwright/phase4-relays.mjs

MODIFIED
  internal/dashboard/deps.go                              + 3 methods
  internal/dashboard/handlers.go                          + /settings/relays*
  internal/dashboard/handlers_test.go
  internal/dashboard/sse.go                               + relay.added, relay.removed
  internal/daemon/dashboard_adapter.go                    + 3 method impls
  internal/daemon/methods.go                              expose existing relay-add/remove IPC
  deploy-test/playwright/full-bidirectional.mjs           extend

NEW INTEGRATION TEST
  test/integration/dashboard_settings_phase4_test.go

EST. LOC                                                  ~500 added
```

### Phase 5 — Service control

```
NEW
  internal/daemon/lifecycle.go                            ★ subprocess primitive
  internal/daemon/lifecycle_test.go                       fake-spawner unit tests
  internal/dashboard/templates/settings_service.html
  internal/dashboard/templates/lifecycle_log.html
  deploy-test/playwright/phase5-service.mjs

MODIFIED
  internal/dashboard/deps.go                              + Status, LifecycleRun
  internal/dashboard/handlers.go                          + /settings/service*
  internal/dashboard/handlers_test.go
  internal/dashboard/sse.go                               + lifecycle.line/done,
                                                            service.status
  internal/daemon/dashboard_adapter.go                    + 3 method impls
  deploy-test/playwright/full-bidirectional.mjs           add reconnect step

NEW INTEGRATION TEST
  test/integration/dashboard_lifecycle_phase5_test.go     fake-spawner only;
                                                            real stop/purge guarded by
                                                            EIDOS_TEST_DESTRUCTIVE=1 env

EST. LOC                                                  ~800 added (lifecycle.go = bulk)
```

### Roll-up

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 | Total |
|---|---|---|---|---|---|---|
| New files | 9 | 4 | 3 | 3 | 5 | **24** |
| Modified files | ~10 | ~7 | ~6 | ~6 | ~7 | **~36** |
| Est. LOC | 600 | 700 | 600 | 500 | 800 | **~3200** |
| New IPC methods | 2 | 2 | 0 | 0 | 1 | **~5** |
| New SSE event kinds | 2 | 1 | 3 | 2 | 3 | **11** |
| `DashboardDeps` methods | 4 | 6 | 4 | 3 | 2 | **19 → 27 total** |

## §10 — Per-phase merge gate (CLAUDE.md pipeline)

Each phase follows the canonical pipeline from `CLAUDE.md` "Commit & Pull Request Guidelines"; the design only *constrains* the gate, it doesn't replace it:

1. **Implementation.** Code lives on a branch under `.claude/<phase-name>/` worktree; UI work uses `frontend-design:frontend-design`; backend wiring uses `writing-plans` derivatives.
2. **Local CI parity.** Run the exact CI commands locally before push: `gofmt -l . && go vet ./... && staticcheck ./... && go test ./...`. Fix until clean.
3. **Phase exit tests.** `node deploy-test/playwright/phaseN.mjs --base http://127.0.0.1:22893` exits 0 on selene's local dashboard. `node deploy-test/playwright/full-bidirectional.mjs --selene http://127.0.0.1:22893 --mbp http://127.0.0.1:22894` exits 0 with both dashboards live.
4. **Third-party code review with `codex`.** Open the PR, request review from `codex` per CLAUDE.md. Address comments before requesting human review.
5. **Documentation pass.** Update `README.md`, `SPEC.md`, `EXAMPLE.md`, and any sibling design specs touched by the phase. Verify the dashboard screenshots/screenshots-equivalent in docs reflect the shipped surface.
6. **Push, watch CI with `gh`.** `gh pr checks --watch` until green; if anything fails, fix and push a new commit (never `--amend` after push).
7. **Watch for Copilot review comments.** GitHub Copilot is auto-assigned on PRs in this repo. Wait for its review; address each comment with a new commit; reply on the thread when fixed.
8. **Merge.** Merge commit only — **no squash**. Per `CLAUDE.md`: "DO NOT use squash merge when you merge a branch or PR."
9. **Proceed.** Only after the phase is merged and the next phase's branch is freshly cut from `main`.

This pipeline is identical for every phase; the playwright script changes, the merge gate doesn't.

## §11 — Open questions / explicitly deferred

- **Mobile / NIP-46 listener.** Out of scope. SPEC §153 dictates a separate listener; auth lives at the listener edge. The design here doesn't bake any mobile assumptions into IPC.
- **Multi-tab outbox sync.** Today's POST-then-bubble pattern means a sibling tab won't see a sent bubble until reload. Acceptable for v1; can be revisited with OOB-by-id swaps if it becomes a real complaint.
- **Job replay across reload.** Lifecycle stream is lost on reload (subprocess still completes). Add a small in-memory ring per job iff users actually hit this.
- **Reload-relay-only entrypoint.** Until this lands in `internal/service`, Relay enable/disable in Config requires a manual `gate stop` + `gate start`. Cleaner UX waits on that primitive.
- **`gate start` button.** Omitted — meaningless from inside a running daemon.
- **Cross-browser regressions.** Chromium-only via Playwright. Firefox/Safari support for the dashboard is implicitly assumed (htmx is conservative about browser features) but not actively tested.
- **Visual regression baseline.** Deferred until the dashboard look stabilises post-`frontend-design`.
- **Token / per-user auth.** Not adopted; revisit if the threat model changes (e.g., dashboard ever gets exposed beyond loopback, or runs on a multi-user host).
