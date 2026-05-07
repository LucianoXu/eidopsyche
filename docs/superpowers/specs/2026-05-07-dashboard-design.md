# Local Web Dashboard for MindGate (v1)

**Date**: 2026-05-07
**Status**: Approved (pending implementation)
**Scope**: An always-on, loopback-bound HTTP server inside `eidos gate daemon`, serving a single embedded HTML/HTMX dashboard. v1 covers the daily-use surface — chat thread per contact (primary), all-messages list and soft-reject inspector (supplementary), in-place compose, and read-only contacts. Setup-time actions (invites, relays, identity) stay CLI-only for v1.

## 1. Problem statement

MindGate's daily-use loop today is `eidos gate inbox --tail` in one terminal and `eidos gate send` in another. This works for an operator who lives in the shell, but two limitations matter:

1. **No conversational view.** Eidopsyche's whole purpose is human ↔ mind-form (and human ↔ human via mind-form network) interaction. That interaction is fundamentally conversational — sender, receiver, threaded reply context. A flat tail-mode CLI doesn't render thread context; the operator reconstructs it in their head.
2. **Unfriendly to non-shell users.** Anyone Eidopsyche is shared with (family, collaborators, future end-users) shouldn't need terminal fluency to use a mind-form. A browser surface lowers the floor without raising the ceiling.

OpenClaw's "zero-install web dashboard served by the daemon on a loopback port" pattern is well-validated and cheap to copy. The architecture decision was approved in earlier brainstorming (envelope-v1 spec, §11).

## 2. Design intent

Five commitments shape the rest of this spec:

1. **Single-language, single-binary.** The daemon embeds the dashboard via `go:embed`. No Node, npm, or Vite in the build pipeline. Templates are Go `html/template`; client-side reactivity is HTMX + a thin handwritten CSS file.
2. **Conversational primary, list secondary.** The default view is a chat thread per contact (sender on the left, self on the right, oldest at top, compose at the bottom). Operators who need the global console view get it via an "All messages" sidebar item and a separate "Soft-rejected" filter.
3. **Loopback-only for v1.** The dashboard binds `127.0.0.1` and ships no auth. Remote access = SSH tunnel. Configurable bind + auth + TLS is deferred to v2 with a clean cut.
4. **In-process with the daemon.** The HTTP server runs in the same process that owns the inbox/outbox/contact state. Handlers call `d.Box.*` / `d.Repo.*` directly — no IPC round-trip, no second source of truth.
5. **Reads only the shipped IPC surface; mutations only through what `eidos gate send` already does.** v1 does not introduce new daemon methods. The only write action surfaced is "send a chat envelope to a pubkey", which uses the existing `sendMessage` code path.

## 3. Layout

The dashboard is a single full-page app with a left sidebar and a main pane.

```
┌──────────────────────────────────────────────────────────────────────┐
│  eidos-gate dashboard         npub1self…0000  ⚙                      │
├──────────────────────────────────────────────────────────────────────┤
│ INBOX                                                                │
│ ● All messages              │  Bob · master                          │
│   Soft-rejected             │ ─────────────────────────────────────  │
│                             │                                        │
│ CONTACTS                    │       Hey, can you ping me when forge  │
│ ● Bob       master   14m    │       is online?                       │
│ ● Alice     friend   2h     │       2 days ago                       │
│ ● Carol     friend   1d     │                                        │
│ ● Dave      acq.     —      │                            will do.    │
│                             │                            yesterday   │
│ + Compose to npub…          │                                        │
│                             │       thanks, all working now          │
│                             │       14 min ago                       │
│                             │                                        │
│                             │ ─────────────────────────────────────  │
│                             │ ┌────────────────────────────────────┐ │
│                             │ │ Reply to Bob…                  Send│ │
│                             │ └────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

### 3.1 Sidebar

Three sections, top to bottom:

- **INBOX**
  - `All messages` — table view (paradigm B) of all inbox + outbox rows newest first, paginated.
  - `Soft-rejected` — same table view filtered to `malformed=true`. Operator inspector for interop debugging.
- **CONTACTS** — one row per contact. Format: `<label> <tier-badge> <relative-time-of-last-message>`. Sorted by most-recent-message desc, then by tier (master > friend > acquaintance > blocked).
- A `+ Compose to npub…` action at the bottom. Opens a small modal (just an `<input>` for the npub or a `mindgate://` URI plus a text area; submits to the same `POST /thread/:npub/send` route).

A blocked-tier contact never appears in the sidebar (its messages are dropped pre-inbox by the existing contact filter). Acquaintance tier appears with a dimmed style.

### 3.2 Main pane: thread view (chat)

When a contact is selected:

- Thread of envelope-v1 chat messages between self and contact, oldest at top, scrolling to bottom on load.
- Self's bubbles right-aligned, contact's bubbles left-aligned. Outbox rows (your sends) are matched into the thread by `to == contact.Pubkey`. Inbox rows by `from == contact.Pubkey`.
- Timestamps shown as relative ("14 min ago") with hover/title showing absolute time.
- Malformed rows from this contact are rendered inline with a dimmed border and a small `[malformed: <reason>]` chip — surfaces interop issues in-context without forcing a tab switch.
- Bottom: a single-line compose input bound to this contact's pubkey. Enter sends. The compose preserves draft text per-contact in `localStorage` so tab-switching doesn't lose typing.

### 3.3 Main pane: list view

When `All messages` or `Soft-rejected` is selected:

- A flat table: `time | direction (in/out) | counterpart-label | preview`.
- Newest-first, paginated 50 per page (cursor-based).
- `Soft-rejected` adds `reason` column.
- Click a row → navigates to the corresponding thread view, scrolled to that message (anchor by event_id).

### 3.4 Header

- Left: project label + own npub (truncated).
- Right: a settings cog. v1 settings page is **out of scope**; the cog opens an "Out of scope in v1 — use the CLI for setup actions" placeholder pointing at `eidos gate --help`.

## 4. Process placement

The dashboard runs **inside the `eidos gate daemon` process**.

- `Daemon.Run()` starts a goroutine that runs the dashboard HTTP listener alongside the existing IPC server and Nostr subscriber.
- Lifetime is identical to the daemon's: starts when daemon starts, stops when daemon stops, no separate systemd unit.
- Handlers receive a `*daemon.Daemon` reference (or a narrowed interface — see §6.2) and call its in-memory state directly.

This decision is locked in for v1 because:

- It eliminates a second IPC client implementation (browser → out-of-process dashboard → IPC socket → daemon would be three hops, two with marshaling).
- The daemon already broadcasts inbox events on a channel; a second consumer (SSE bridge) is one additional select case.
- One process = one log stream = one set of metrics later.

## 5. Transport

Plain HTTP/1.1 on a loopback TCP port. Live updates use **Server-Sent Events** (SSE), not WebSocket. Rationale:

- Server → browser is one-way for v1 (live tail, sidebar refresh on contact-add, etc.). SSE matches the shape exactly.
- HTMX has first-class SSE support (`hx-ext="sse"` + `sse-connect` + `sse-swap`). Adding WebSocket would require the `htmx-ws` extension and a heavier server-side framing.
- Browser native `EventSource` reconnects automatically on network blips; we don't have to write reconnection logic.

### 5.1 Routes

| Path | Method | Returns | Notes |
|---|---|---|---|
| `/` | GET | full HTML page (shell) | initial render; subsequent navigation is htmx |
| `/sidebar/contacts` | GET | HTML fragment | refreshable sidebar contacts list (sse-swap target) |
| `/messages` | GET | HTML fragment | "All messages" table view, paginated via `?cursor=` |
| `/messages?malformed=1` | GET | HTML fragment | "Soft-rejected" filter |
| `/thread/{pubkey}` | GET | HTML fragment | chat thread for a specific pubkey |
| `/thread/{pubkey}/send` | POST | HTML fragment (new bubble) | sends envelope-v1 chat; pubkey may be hex or npub. On send failure (e.g. no relay accepted), returns HTTP 502 with an HTML error fragment that htmx swaps into the compose pane; the draft text is preserved. |
| `/compose` | GET | HTML fragment | compose-to-npub modal contents |
| `/events` | GET (SSE) | `text/event-stream` | live tail of inbox/outbox/contact events |
| `/static/{path}` | GET | static file (htmx, css, icons) | served from `embed.FS` |

The dashboard is deliberately **not** a JSON API. Responses are HTML fragments selected by HTMX `hx-target` / `hx-swap`. There is no mobile/native client consuming this endpoint; if such a client appears later, the existing IPC socket is the right surface.

### 5.2 SSE event types

Emitted on `/events` as named SSE events:

- `inbox.message` — new inbox row arrived. Payload is an HTML fragment (a chat bubble) ready to swap into the active thread if `data-pubkey` matches.
- `outbox.message` — new outbox row recorded (after publish accepted). Same shape, opposite alignment.
- `contact.added` / `contact.removed` / `contact.relabeled` — sidebar contacts changed. Payload is the new sidebar fragment.

The browser uses `sse-swap` with selectors that no-op when the active view doesn't match (e.g. an inbox.message for `from=Carol` arrives while the user is viewing the Bob thread → htmx target `#thread-Carol` doesn't exist on the page, swap is a no-op; the sidebar's last-message-time indicator updates separately via a different sse-swap target).

The daemon already has a broadcast mechanism for IPC subscribers (`broadcastInbox` calls each `*ipc.Conn`'s event channel). The dashboard server hooks into the same upstream channel via a small fan-out adapter, NOT by registering as an IPC client.

## 6. Module organization

### 6.1 New package: `internal/dashboard/`

```
internal/dashboard/
├── server.go              # Listener lifecycle; Run(ctx) called from daemon.Run
├── handlers.go            # Route handlers
├── sse.go                 # SSE hub: subscribes to daemon's broadcast, fans out to clients
├── render.go              # Template parse/cache; Render(name, data) helper
├── pagination.go          # Cursor codec for /messages
├── templates/
│   ├── shell.html         # Full page shell
│   ├── sidebar.html       # Contacts + filters
│   ├── thread.html        # Chat thread
│   ├── bubble.html        # One chat bubble (used by thread render + SSE event payload)
│   ├── messages.html      # All-messages table
│   ├── compose.html       # Compose modal
│   └── partials/
│       ├── time.html      # Relative-time helper render
│       └── badge.html     # Tier badge
└── static/
    ├── htmx.min.js        # ~14KB, vendored
    ├── htmx-sse.js        # SSE extension
    ├── app.css            # ~5–8KB hand-written
    └── icons/
        ├── send.svg
        ├── soft-reject.svg
        └── ...
```

All `templates/*.html` and `static/*` are embedded with `//go:embed templates static` in `templates.go` (or `render.go`).

### 6.2 Daemon dependency

Handlers receive a small interface, not the whole `*Daemon`:

```go
type DashboardDeps interface {
    OwnPubkey() string
    OwnLabel(ctx context.Context) (string, error)

    ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error)
    ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error)
    ListContacts(ctx context.Context) ([]*contacts.Contact, error)

    // Send wraps text in a v1 chat envelope and publishes via the same
    // path as the existing IPC `send` method. Returns the wrap event_id
    // on success, or an error if no relay accepts (caller renders the
    // error in the compose pane).
    Send(ctx context.Context, toPubkey string, env envelope.Envelope) (eventID string, err error)

    // SubscribeEvents returns a channel of DashboardEvent values for the
    // SSE hub to fan out. Cancel must be called to release the slot.
    SubscribeEvents() (ch <-chan DashboardEvent, cancel func())
}

// DashboardEvent is the package-internal event type fanned out via SSE.
// One concrete type per §5.2 event kind, discriminated by Kind.
type DashboardEvent struct {
    Kind    string         // "inbox.message" | "outbox.message" | "contact.added" | "contact.removed" | "contact.relabeled"
    Message *inbox.Message // populated when Kind == inbox.message
    Sent    *inbox.Sent    // populated when Kind == outbox.message
    Contact *contacts.Contact // populated for contact.* kinds
}
```

This interface is satisfied by a thin adapter on `*Daemon`. Handlers can be unit-tested with a stub (`fakeDeps{}`). The interface is package-internal — not promoted to `pkg/`.

### 6.3 Integration into daemon.Run

`internal/daemon/daemon.go`'s `Run` gains one more goroutine:

```go
go d.runSubscriber(ctx)
go dashboard.Run(ctx, dashboardAdapter{d}, d.Cfg.Dashboard, d.Log)  // NEW
return srv.Serve(ctx)
```

A new `DashboardConfig` struct in `internal/config/`:

```toml
[dashboard]
enabled = true                     # default true; set false to skip the listener
listen  = "127.0.0.1:22893"        # loopback only in v1
```

If `enabled = false`, `dashboard.Run` returns immediately. If `Listen` is non-loopback, the daemon **logs a warning and refuses to bind** for v1 — auth/TLS are not yet implemented and exposing the dashboard on the network without them is a footgun. The refusal is loud (ERROR level, prefix `[dashboard] non-loopback bind not supported in v1`) and the daemon continues without the dashboard.

## 7. CLI surface

```
eidos gate dashboard           # open the running daemon's dashboard in the default browser
eidos gate dashboard --no-open # print the URL; do not auto-launch
```

Both forms:
1. Read `config.toml`, derive the dashboard URL from `listen`.
2. Probe the URL with a 1-second `GET /` to verify the daemon is up.
3. If unreachable: print `dashboard not reachable at <url>; is the daemon running?` and exit 1.
4. Otherwise print the URL and (unless `--no-open`) call the OS-appropriate browser launcher (`xdg-open` on Linux, `open` on macOS, `rundll32 url.dll,FileProtocolHandler` on Windows).

The subcommand does **not** start the daemon. If the daemon isn't up, the dashboard isn't either; the user runs `eidos gate start` first.

## 8. Auth / security posture

**v1 ships no authentication.** Loopback bind = host-level access. Anyone who can connect to `127.0.0.1` on the host can already read the IPC socket (`~/.eidos/gate/sock`) and the SQLite state (`state.db`). Adding a token at the dashboard layer doesn't raise the bar; it adds friction.

The daemon refuses to bind the dashboard on a non-loopback interface (§6.3). This is not a defense-in-depth measure; it's a guard against the most likely operator mistake.

CSRF: not applicable for v1 since there is no auth boundary to forge across — anyone on loopback is already inside it. Browsers send `Origin` headers; we will accept only same-origin requests as a defensive default (`Origin` matches `Host`, plus `null` for direct address-bar navigation). Misconfigured browser extensions or local services on the host could theoretically issue cross-origin POSTs; the same-origin check rejects them.

XSS: `html/template` escapes by default. Any place we drop into raw HTML (e.g., `template.HTML` casts) needs a comment justifying it, or it's a bug.

## 9. Build pipeline

No npm. CI is unchanged.

- Templates: checked-in `.html` files. `go:embed templates static` packs them into the binary.
- Static assets: htmx is a vendored single file under `internal/dashboard/static/htmx.min.js` (with a comment at the top recording the upstream version + SHA for audit).
- CSS: handwritten `app.css`, no preprocessor.
- Versioning: htmx upgrades are deliberate, manual, and reviewed (no auto-update).

## 10. Testing

### 10.1 Unit tests

- `handlers_test.go` — each handler invoked via `httptest.NewRecorder` against a `fakeDeps` stub. Assert HTTP status, response is parseable HTML, and key data appears in the rendered output.
- `render_test.go` — every template renders against a representative data fixture without error and produces non-empty output.
- `pagination_test.go` — cursor encode/decode round-trip, edge cases (empty, end-of-page).
- `sse_test.go` — hub fan-out: subscribe N clients, publish event, all see it. Slow client handling: drop policy is documented (§11).

### 10.2 Integration tests

Reuse the existing `bringUp` harness in `test/integration/`:

- `dashboard_test.go` (new):
  - `GET /` returns 200 with the shell, contains the operator's npub.
  - `GET /thread/{bob}` after a chat round-trip shows the message body in the response.
  - `POST /thread/{bob}/send` publishes via the same path as `eidos gate send`; row appears in outbox; integration responds with the new bubble fragment.
  - `GET /events` keeps the connection open, sends a `inbox.message` event after a peer publishes, response stream contains the expected `event:` line.
  - `GET /messages?malformed=1` after a forced soft-reject shows the malformed row.

Tag: `//go:build integration`. Reuses the existing `bringUp(t, name)` and (where useful) makes its own thin HTTP client.

### 10.3 Frontend craft (during implementation)

The implementation phase invokes the `frontend-design` skill. The acceptance bar for v1 visual polish:

- Dark mode default, light mode via `prefers-color-scheme`.
- Distinctive aesthetic — not the default Bootstrap / Tailwind look. Type and spacing chosen deliberately.
- Loads under 100ms cold from loopback; full first paint within 200ms even on a slow laptop. (CSS budget ≤ 8KB minified, JS budget ≤ 20KB total including htmx.)
- Keyboard: Enter sends in compose; Esc closes modal; `j`/`k` navigate sidebar; `/` focuses search if/when search exists (search itself is **out of scope** for v1).
- Renders correctly at 1024×768 minimum; mobile not targeted in v1.

## 11. Known limitations / v2 candidates

These are intentionally out of v1. Each has a recorded trigger that would justify adding it.

- **Authenticated remote access.** Requires session/token model + TLS. Trigger: real demand for "view my home daemon's dashboard from my phone over the open internet."
- **Search.** Across inbox/outbox content. Trigger: when an operator hits the practical pagination ceiling and complains.
- **Settings panel.** Identity (label / card / npub QR), invites, relays. Trigger: settings-via-CLI is the friction the dashboard exists to eliminate, so this is a fast-follow, but each setting is its own form; v1 punts.
- **Mobile layout.** Trigger: when there's a thin client / NIP-46 mobile flow ready (this depends on `eidos-mcp` and remote-signing).
- **SSE backpressure.** v1 uses a per-client buffered channel; on overflow we drop the slowest client (close their stream). Documented; not auto-recovered.
- **Per-contact unread counts.** Requires last-read-time persistence per contact. Trigger: confirmed user need (and probably depends on the same persistence the v2 status command will need for "inbox: N unread").
- **Forge integration.** When the forge subcommand is wired up, dashboard panels for mind-form lifecycle (start/stop/wake) will land alongside it. Dashboard is the natural surface; v1 just punts on it.
- **Reactions / threading inside a chat.** Tied to envelope v2 features.
- **Frontend-design skill polish.** v1 implementation does invoke `frontend-design`; v2 should iterate against real user feedback rather than defaulting back to AI aesthetic.

## 12. Out of scope

- New IPC methods. v1 uses only `sendMessage` and the `Box`/`Repo` listing methods that already exist.
- Anything that mutates contacts/relays/invites from the browser.
- A persistent dashboard daemon separate from `eidos gate daemon`.
- Any frontend framework (React/Vue/Svelte/Lit). HTMX only.
- Any CSS framework. Hand-written styles only.

## 13. Acceptance criteria

- `internal/dashboard/` package exists with the layout in §6.1.
- `eidos gate daemon` starts the dashboard listener on `127.0.0.1:22893` by default; logs the URL.
- `eidos gate dashboard` and `--no-open` work as in §7.
- Configuration in `config.toml` `[dashboard]` block respected; non-loopback `listen` is refused with the documented log line.
- All unit tests in §10.1 pass.
- Integration test `dashboard_test.go` (§10.2) passes under `go test -tags=integration ./test/integration/...`.
- `gofmt`, `go vet`, `staticcheck` clean.
- Frontend-design skill applied during implementation; the dashboard does not look like a generic AI-generated form. (Subjective bar — the implementation plan will spell out concrete craft checkpoints.)
- README / USAGE.md gain a "Dashboard" section pointing at `eidos gate dashboard`.

## 14. References

- Envelope v1 spec: `docs/superpowers/specs/2026-05-07-envelope-v1-design.md` — the message wire format the dashboard renders.
- Envelope v1 §11 (`eidos-mcp`) — the same in-process pattern (server alongside daemon) as a precedent.
- OpenClaw Control UI — pattern reference (Vite + Lit chosen there; we deliberately diverge to HTMX for the single-language ethos).
