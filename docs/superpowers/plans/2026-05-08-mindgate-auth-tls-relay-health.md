# MindGate AUTH, native TLS, and relay-health — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land NIP-42 AUTH end-to-end, native TLS in `relayd`, and relay-health visibility — three coupled gaps from v0.5 — in one milestone targeting the dev release.

**Architecture:** Three subsystems sharing the daemon's per-relay state machine: AUTH (Pool signer + relayd RejectFilter), TLS (relayd `ListenAndServeTLS`), health (daemon owns state, dashboard SSE event + IPC method consume).

**Tech Stack:** Go; `github.com/fiatjaf/khatru` (server-side AUTH built-in via `RequestAuth`/`GetAuthed`); `github.com/nbd-wtf/go-nostr` + `nip42` package (client-side AUTH); BurntSushi/toml.

**Spec:** `docs/superpowers/specs/2026-05-08-mindgate-auth-tls-relay-health-design.md`

---

## File mapping

**Created:**
- `test/integration/auth_roundtrip_test.go`
- `test/integration/relayd_tls_test.go`
- `test/integration/relay_health_test.go`
- `cmd/eidos/gate/status_test.go`
- `internal/dashboard/templates/relays.html` (partial)

**Modified (code):**
- `internal/config/config.go` + `config_test.go`
- `internal/nostr/pool.go` + `pool_test.go`
- `internal/relayd/relayd.go` + `relayd_test.go`
- `internal/daemon/daemon.go` + new `relayhealth.go`
- `internal/daemon/methods.go` (register `relays.health`)
- `internal/daemon/dashboard_adapter.go` (forward `relay.state` events)
- `internal/dashboard/deps.go` (extend `Event`)
- `internal/dashboard/sse.go` (handle `relay.state`)
- `internal/dashboard/handlers.go` (`GET /relays` partial)
- `internal/dashboard/render.go` / templates main view
- `cmd/eidos/gate/status.go`
- `cmd/eidos/gate/whoami.go`

**Modified (docs):**
- `docs/INSTALL.md` (Native TLS subsection; CHANGELOG note for AUTH default)
- `docs/USAGE.md` (drop the "experimental" caveat from public-Nostr-relay topology)

---

## Task 1: Config schema additions

**Files:** `internal/config/config.go`, `internal/config/config_test.go`

- [ ] **Step 1:** Add nested types and Defaults():

```go
type RelayConfig struct {
    Enabled bool          `toml:"enabled"`
    Mode    string        `toml:"mode"`
    Listen  string        `toml:"listen"`
    DataDir string        `toml:"data_dir"`
    Auth    RelayAuthConfig `toml:"auth"`
    TLS     RelayTLSConfig  `toml:"tls"`
}

type RelayAuthConfig struct {
    Required   bool   `toml:"required"`
    ServiceURL string `toml:"service_url"`
}

type RelayTLSConfig struct {
    CertFile string `toml:"cert_file"`
    KeyFile  string `toml:"key_file"`
}
```

In `Defaults()`: `Auth: RelayAuthConfig{Required: true}`, `TLS: RelayTLSConfig{}`.

- [ ] **Step 2:** Tests in `config_test.go`:
  - `Defaults().Relay.Auth.Required == true`, TLS fields empty.
  - Round-trip `[relay.auth] required = false` and `[relay.tls] cert_file = "..." key_file = "..."`.
  - Loading a v0.5 config (no `[relay.auth]`) → `Auth.Required == false` (Go zero value — test asserts current behavior; we apply the spec-default at consumer sites instead of via Defaults() merge to avoid disturbing the v0.4-detection contract).

  **Wait:** if we want the spec-default `true` to apply for v0.5 configs that lack the section, the cleanest path is to fold it into `Load()` after decoding: if `meta.IsDefined("relay","auth","required") == false { cfg.Relay.Auth.Required = true }`. That keeps Defaults() purely structural. Test both branches.

- [ ] **Step 3:** Update `Load()` to set `Auth.Required = true` when the field was not present in the file (uses MetaData via the existing `LoadWithMeta` helper or via a new `Load` that consults MetaData internally).

- [ ] **Step 4:** Build + test config package:

```sh
go build ./internal/config/...
go test ./internal/config/...
```

- [ ] **Step 5:** Commit:

```
feat(config): RelayAuthConfig + RelayTLSConfig

Adds [relay.auth] {required, service_url} and [relay.tls] {cert_file,
key_file} blocks. Spec default Auth.Required=true is applied during
Load() when the field was absent in the file (so v0.5 configs upgrade
without flipping to the unsafe default).
```

---

## Task 2: Native TLS in relayd

**Files:** `internal/relayd/relayd.go`, `internal/relayd/relayd_test.go`

- [ ] **Step 1:** Add `TLSConfig struct{ CertFile, KeyFile string }` to `relayd.Config`.

- [ ] **Step 2:** In `Server.ListenAndServe`:

```go
func (s *Server) ListenAndServe() error {
    if s.cfg.TLS.CertFile != "" || s.cfg.TLS.KeyFile != "" {
        if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
            return fmt.Errorf("relay.tls: both cert_file and key_file must be set")
        }
        return s.http.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
    }
    return s.http.ListenAndServe()
}
```

- [ ] **Step 3:** Tests:
  - `TestRelayd_TLS_BothSetServesHTTPS`: generate self-signed cert in `t.TempDir()`, start relay, dial via `tls.Dial` with `InsecureSkipVerify`, assert handshake succeeds.
  - `TestRelayd_TLS_OnlyCertSet_Errors`: assert `ListenAndServe()` returns error matching `both cert_file and key_file`.
  - `TestRelayd_TLS_OnlyKeySet_Errors`: symmetric.
  - `TestRelayd_TLS_Empty_FallsBackToPlain`: existing plain-ws tests already cover this; just verify nothing broke.

- [ ] **Step 4:** Build + test:

```sh
go test ./internal/relayd/... -run TestRelayd_TLS
```

- [ ] **Step 5:** Commit:

```
feat(relayd): native TLS via [relay.tls] cert_file / key_file

When both paths are set, Server.ListenAndServe calls
http.ListenAndServeTLS instead of plain ListenAndServe. Setting only
one is a startup error. No autocert / ACME — cert lifecycle is BYO
(certbot, manual, etc.).
```

---

## Task 3: AUTH enforcement on relayd

**Files:** `internal/relayd/relayd.go`, `internal/relayd/relayd_test.go`

- [ ] **Step 1:** Inspect khatru's AUTH primitives:

```sh
grep -n "RequestAuth\|GetAuthed\|nip42" $(go env GOMODCACHE)/github.com/fiatjaf/khatru@v0.17.4/utils.go
```

Expected: `khatru.RequestAuth(ctx)` and `khatru.GetAuthed(ctx) string`.

- [ ] **Step 2:** Add the filter:

```go
// In relayd.New, after the RejectEvent setup:
if cfg.Auth.Required {
    r.RejectFilter = append(r.RejectFilter, requireAuthForKind1059Reads)
}
```

```go
func requireAuthForKind1059Reads(ctx context.Context, filter gnostr.Filter) (bool, string) {
    wantsKind1059 := false
    for _, k := range filter.Kinds {
        if k == 1059 {
            wantsKind1059 = true
            break
        }
    }
    if !wantsKind1059 {
        return false, ""
    }
    authed := khatru.GetAuthed(ctx)
    if authed == "" {
        khatru.RequestAuth(ctx)
        return true, "auth-required: NIP-42 AUTH required for kind:1059 reads"
    }
    pTags, ok := filter.Tags["p"]
    if !ok || len(pTags) == 0 {
        return true, "auth-required: kind:1059 REQ must include #p filter"
    }
    for _, p := range pTags {
        if p != authed {
            return true, "auth-mismatch: requested #p does not match AUTH pubkey"
        }
    }
    return false, ""
}
```

Plumb `Auth.Required` and `Auth.ServiceURL` through `relayd.Config`. If `ServiceURL != ""`, set `r.ServiceURL = cfg.Auth.ServiceURL`.

- [ ] **Step 3:** Tests:
  - `TestRelayd_Auth_RejectsUnauthenticated_Kind1059`: connect to relay, send REQ for `{kinds:[1059], #p:[somepubkey]}`, assert relay sends `["AUTH", challenge]` and the REQ does not return events.
  - `TestRelayd_Auth_AcceptsAuthenticatedMatching`: send AUTH event with pubkey == `#p` value, then REQ; assert events flow.
  - `TestRelayd_Auth_RejectsAuthenticatedMismatch`: AUTH as pubkey-X, REQ for `#p:[pubkey-Y]`; assert "auth-mismatch".
  - `TestRelayd_Auth_NotRequired_AcceptsUnauth`: with `Auth.Required = false`, REQ succeeds without AUTH.

  These tests need a low-level WebSocket client that can send raw NIP-01 frames; use `github.com/nbd-wtf/go-nostr` or hand-roll over `gorilla/websocket`.

- [ ] **Step 4:** Build + test:

```sh
go test ./internal/relayd/... -run TestRelayd_Auth
```

- [ ] **Step 5:** Commit:

```
feat(relayd): NIP-42 AUTH required for kind:1059 reads (default on)

When [relay.auth] required=true (the default), relayd enforces NIP-17's
recommended AUTH-for-kind:1059-reads. RejectFilter checks GetAuthed(ctx)
against every #p tag in the filter; mismatch is rejected with a clear
auth-mismatch reason. RequestAuth(ctx) on first unauthed REQ pushes a
challenge to the client. Writes are unchanged.
```

---

## Task 4: Pool AUTH client

**Files:** `internal/nostr/pool.go`, `internal/nostr/pool_test.go`

- [ ] **Step 1:** Inspect go-nostr's relay AUTH hook:

```sh
grep -n "OnAuth\|Auth\b" $(go env GOMODCACHE)/github.com/nbd-wtf/go-nostr@*/relay.go | head
```

Expected: `Relay.Auth` field on the relay struct, called when AUTH challenge arrives.

- [ ] **Step 2:** Add to `Pool`:

```go
type Signer interface {
    Sign(event *gnostr.Event) error  // mutates event with signature
    PublicHex() string
}

func NewPoolWithSigner(signer Signer) *Pool {
    p := NewPool()
    p.signer = signer
    return p
}
```

In the dial path (where `relay.Connect` happens), set `relay.Auth = func(authEvent *nostr.Event) error { return p.signAuthEvent(connURL, authEvent) }`.

```go
func (p *Pool) signAuthEvent(connURL string, ev *gnostr.Event) error {
    if p.signer == nil {
        return errors.New("AUTH challenge received but Pool has no signer")
    }
    // NIP-42 §Considerations: validate the relay tag matches connURL.
    matched := false
    for _, tag := range ev.Tags {
        if len(tag) >= 2 && tag[0] == "relay" {
            if tag[1] == connURL {
                matched = true
            }
            break
        }
    }
    if !matched {
        return fmt.Errorf("AUTH event relay tag does not match connection URL %q", connURL)
    }
    ev.PubKey = p.signer.PublicHex()
    ev.CreatedAt = gnostr.Now()
    return p.signer.Sign(ev)
}
```

(The exact go-nostr API may differ — `Auth` callback might take a challenge string and expect a fully-built event return; pin at implementation time.)

- [ ] **Step 3:** Tests:
  - `TestPool_SignAuthEvent_BuildsCorrectShape`: feed a fake AUTH event, assert kind=22242, tags include relay+challenge, signature verifies against signer's pubkey.
  - `TestPool_SignAuthEvent_RefusesURLMismatch`: AUTH event has `["relay", "wss://attacker"]` but connURL is `wss://us`; assert error.
  - `TestPool_SignAuthEvent_NoSigner_Errors`: pool without signer + AUTH challenge → error.

- [ ] **Step 4:** Wire daemon to call `NewPoolWithSigner(d.Key)`. Add a small `keySigner` adapter on `identity.Key` (or use `identity.Key` directly if it implements the methods).

- [ ] **Step 5:** Build + test:

```sh
go test ./internal/nostr/... -run TestPool_SignAuthEvent
go test ./internal/daemon/...
```

- [ ] **Step 6:** Commit:

```
feat(nostr): NIP-42 AUTH client in Pool

Pool gains an optional Signer; when set, it responds to AUTH challenges
with a kind:22242 event whose ["relay", url] tag matches the connection
URL. Refuses to sign on URL mismatch (NIP-42 §Considerations replay
defense). Daemon constructs Pool via NewPoolWithSigner(d.Key).
```

---

## Task 5: Daemon relay-health state machine

**Files:** `internal/daemon/relayhealth.go` (new), `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`

- [ ] **Step 1:** Define the state struct and the helper:

```go
// internal/daemon/relayhealth.go
package daemon

import (
    "sync"
    "time"
)

type RelayHealth struct {
    URL         string    `json:"url"`
    Role        string    `json:"role"`
    State       string    `json:"state"` // pending | connecting | connected | error | auth-failed
    LastError   string    `json:"last_error,omitempty"`
    LastEventAt int64     `json:"last_event_at,omitempty"` // unix seconds
    UpdatedAt   int64     `json:"updated_at"`
}

type relayHealthStore struct {
    mu sync.RWMutex
    m  map[string]*RelayHealth
}

func newRelayHealthStore() *relayHealthStore {
    return &relayHealthStore{m: map[string]*RelayHealth{}}
}

func (s *relayHealthStore) set(url, role, state, lastErr string) RelayHealth {
    s.mu.Lock()
    defer s.mu.Unlock()
    h, ok := s.m[url]
    if !ok {
        h = &RelayHealth{URL: url}
        s.m[url] = h
    }
    h.Role = role
    h.State = state
    h.LastError = lastErr
    h.UpdatedAt = time.Now().Unix()
    return *h
}

func (s *relayHealthStore) markEvent(url string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if h, ok := s.m[url]; ok {
        h.LastEventAt = time.Now().Unix()
    }
}

func (s *relayHealthStore) snapshot() []RelayHealth {
    s.mu.RLock()
    defer s.mu.RUnlock()
    out := make([]RelayHealth, 0, len(s.m))
    for _, h := range s.m {
        out = append(out, *h)
    }
    return out
}
```

- [ ] **Step 2:** Wire into `Daemon`:

```go
// daemon.go — Daemon struct
relayHealth *relayHealthStore

// Start
d.relayHealth = newRelayHealthStore()
```

In `runSubscriber`, after computing URLs and roles (need a small change to `subscriptionURLs` so it returns role per URL), set state transitions:

- `pending` when URL first appears.
- `connecting` before dial.
- `connected` after Pool.Subscribe returns a live channel.
- `error` with the error string when subscribe fails.
- `auth-failed` is set in `dispatchEnvelope` /elsewhere when we detect the relay's AUTH-required NOTICE we couldn't satisfy. **Simplification for this milestone:** treat AUTH failures as `error` with `LastError` containing the relay's reason; carve out `auth-failed` later if we get distinct signals.

Add an event-receive hook: when an event arrives via the subscriber, call `d.relayHealth.markEvent(url)`.

- [ ] **Step 3:** Tests:
  - `TestRelayHealth_StateTransitions`: write a fake `subscriptionURLs` that returns one URL with role=home; mock Pool to return a known error; verify state goes `pending` → `connecting` → `error` with the error message.
  - `TestRelayHealth_Snapshot`: insert several URLs at varying states, assert `snapshot()` returns them all with the right shape.

- [ ] **Step 4:** Commit:

```
feat(daemon): per-URL relay-health state machine

New relayHealth store tracks State (pending|connecting|connected|error)
+ LastError + LastEventAt per relay URL. runSubscriber drives the
transitions. snapshot() returns []RelayHealth for downstream consumers
(IPC, dashboard).
```

---

## Task 6: IPC `relays.health` method

**Files:** `internal/daemon/methods.go`, `internal/daemon/methods_test.go`

- [ ] **Step 1:** Register and implement:

```go
register("relays.health", relaysHealth)
// ...
func relaysHealth(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
    return d.relayHealth.snapshot(), nil
}
```

- [ ] **Step 2:** Test (in `methods_test.go` or via integration test): call IPC `relays.health` against a started daemon, verify shape.

- [ ] **Step 3:** Commit:

```
feat(daemon): relays.health IPC method

Returns []RelayHealth from the daemon's per-URL state store.
Consumers: gate status, gate whoami, dashboard.
```

---

## Task 7: Dashboard `relay.state` event + panel

**Files:** `internal/dashboard/deps.go`, `internal/dashboard/sse.go`, `internal/dashboard/handlers.go`, `internal/dashboard/render.go`, `internal/dashboard/templates/relays.html` (new), templates/index update; `internal/daemon/dashboard_adapter.go`

- [ ] **Step 1:** Extend `Event`:

```go
type Event struct {
    Kind     string
    Message  *inbox.Message
    Sent     *inbox.Sent
    Contact  *contacts.Contact
    Relay    *RelayHealth   // new; populated when Kind == "relay.state"
}

type RelayHealth struct {
    URL         string
    Role        string
    State       string
    LastError   string
    LastEventAt int64
}
```

The `RelayHealth` mirror in dashboard package keeps the daemon→dashboard adapter from importing daemon internals.

- [ ] **Step 2:** Adapter forwards `relay.state`:

In `internal/daemon/dashboard_adapter.go` — when `relayHealth.set(...)` is called, also `a.d.emitDashEvent(dashboard.Event{Kind: "relay.state", Relay: convert(h)})`. Push the convert helper into the same file.

- [ ] **Step 3:** SSE handler:

```go
// sse.go renderEvent
case "relay.state":
    return ev.Kind, render.Relay(ev.Relay)  // or similar
```

- [ ] **Step 4:** Template `relays.html`:

```html
{{ define "relays" }}
<table class="relays">
  <thead>
    <tr><th>role</th><th>url</th><th>state</th><th>last event</th></tr>
  </thead>
  <tbody hx-swap-oob="innerHTML:#relays-tbody" id="relays-tbody">
    {{ range . }}
      <tr class="state-{{ .State }}">
        <td>{{ .Role }}</td>
        <td>{{ .URL }}</td>
        <td>{{ .State }}{{ if .LastError }}<span class="error" title="{{ .LastError }}">⚠</span>{{ end }}</td>
        <td>{{ if .LastEventAt }}{{ ago .LastEventAt }}{{ else }}—{{ end }}</td>
      </tr>
    {{ end }}
  </tbody>
</table>
{{ end }}
```

Wire into the main dashboard view alongside inbox / outbox / contacts. Add `GET /relays` handler that returns the partial for HTMX SSE swap.

- [ ] **Step 5:** Tests:
  - `internal/dashboard/sse_test.go`: feed a `relay.state` event, assert SSE wire output includes the URL and state.
  - `internal/dashboard/render_test.go`: render with empty list, single entry, multiple states.

- [ ] **Step 6:** Build + test all dashboard packages.

- [ ] **Step 7:** Commit:

```
feat(dashboard): relay-health panel + relay.state SSE event

Extends the Event union with a Relay payload. New relays.html partial
renders a colored-state table; SSE swap target is #relays-tbody.
Daemon's relayHealth.set transitions emit dashboard events alongside
the IPC store update.
```

---

## Task 8: `eidos gate status` Relays section

**Files:** `cmd/eidos/gate/status.go`, `cmd/eidos/gate/service.go`, `cmd/eidos/gate/status_test.go` (new)

- [ ] **Step 1:** Extend `printStatus` to fetch and render relay health. Open IPC client (use the daemon socket) and call `relays.health`. Render after the unit table:

```go
if mgr supports it:
  fmt.Fprintln(w)
  fmt.Fprintln(w, "Relays:")
  for _, h := range health {
      // format each row
  }
```

If the daemon is not reachable (paused, freshly stopped), skip the section silently — status's primary job is the unit table.

- [ ] **Step 2:** Tests: a small unit test that exercises the rendering function with synthesized `[]RelayHealth`.

- [ ] **Step 3:** Commit:

```
feat(gate): 'status' shows per-relay connection health

Fetches relays.health via IPC after the unit table; lists role / URL /
state / last-event-age. Skipped silently when the daemon socket isn't
reachable (e.g., between stop and start).
```

---

## Task 9: `eidos gate whoami` annotations

**Files:** `cmd/eidos/gate/whoami.go`

- [ ] **Step 1:** Update the existing whoami output to call `relays.health` and annotate each home/fallback row inline:

```
Relays:
  home       wss://yingte.io                 [connected]
  fallback   wss://relay.damus.io            [failed: x509: ...]
```

- [ ] **Step 2:** Commit:

```
feat(gate): 'whoami' annotates relay URLs with connection state
```

---

## Task 10: Integration test — AUTH round-trip

**Files:** `test/integration/auth_roundtrip_test.go` (new), maybe extend `loopback_test.go` helpers

- [ ] **Step 1:** Bring up A with `relayd.ModePaired` and AUTH required. Bring up B as `bringUpDaemonOnly` with home pointed at A's relay URL. Mutual `add-contact`. B sends a chat envelope to A. Assert: B's daemon AUTHed transparently (no error in logs), A's inbox receives the envelope. Verify by checking IPC `relays.health` on B that A's URL state == "connected".

- [ ] **Step 2:** Negative case: configure B's daemon `Pool` without a signer (mock at test level). Assert: B's `relays.health` shows A's URL in `error` state with a string mentioning AUTH; B's inbox does not receive the test message.

- [ ] **Step 3:** Run + commit.

---

## Task 11: Integration test — relayd TLS

**Files:** `test/integration/relayd_tls_test.go` (new)

- [ ] **Step 1:** Generate self-signed cert at test setup (use `crypto/x509` + `crypto/tls` to write to `t.TempDir()`). Configure `relayd.Config{TLS: TLSConfig{...}}`, start. Daemon's Pool.Connect dials `wss://localhost:<port>` with `InsecureSkipVerify` set on the TLS config (test-only knob — added as a build-tagged option or via `Pool` field). Assert send/receive works.

- [ ] **Step 2:** Run + commit.

---

## Task 12: Integration test — relay-health observability

**Files:** `test/integration/relay_health_test.go` (new)

- [ ] **Step 1:** Bring up A with full relay. Bring up B with home pointed at A. Wait until B's `relays.health` reports A as `connected`. Stop A's relay (don't stop the daemon — only the relay listener). Assert B's health flips to `error` within ~10s (with backoff bound). Restart A's relay; assert B's health returns to `connected` (the `Pool.Connect` dead-cache eviction patch enables this).

- [ ] **Step 2:** Run + commit.

---

## Task 13: Documentation

**Files:** `docs/INSTALL.md`, `docs/USAGE.md`

- [ ] **Step 1:** `docs/INSTALL.md`: in the existing "Self-hosting the embedded relay" subsection, add a new "Native TLS (BYO certs)" subsubsection covering:
  - certbot one-liner for issuing the cert
  - `eidos gate config set relay.tls.cert_file ...` / `key_file ...`
  - Restart vs. nginx posture note: "use this when the relay is the only TLS service on the host; otherwise prefer the reverse-proxy posture below"
  - Renewal: certbot's `--post-hook` restarts `eidos-gate-relay`

- [ ] **Step 2:** `docs/USAGE.md`: in the public-Nostr-relay topology block, drop the "**v0.5 caveat:**..." paragraph (this release closes that gap). Add a one-line note: "AUTH is automatic; daemon authenticates with your gate's identity key on every connection."

- [ ] **Step 3:** Commit:

```
docs: native TLS section in INSTALL; drop public-relay caveat in USAGE
```

---

## Task 14: Final verification + push + PR

- [ ] **Step 1:** `make ci` (gofmt + vet + staticcheck + unit + integration). Expected: all green.

- [ ] **Step 2:** Push `feat/auth-tls-relay-health` branch.

- [ ] **Step 3:** Open PR with summary mirroring the spec's three-component framing. Title: `feat: NIP-42 AUTH, native TLS, and relay-health visibility`.

- [ ] **Step 4:** Watch CI; address any failures with NEW commits (no amend / no force-push).

- [ ] **Step 5:** Wait for Copilot review; address comments via the receiving-code-review skill (technical rigor, push back on incorrect points).

- [ ] **Step 6:** Report PR URL + state to user.

---

## Self-review

**Spec coverage:**

| Spec section | Task |
|---|---|
| §3 NIP-42 AUTH | 3 (relayd) + 4 (Pool client) |
| §4 Native TLS | 2 |
| §5 Relay-health | 5 (state machine) + 6 (IPC) + 7 (dashboard) + 8 (status) + 9 (whoami) |
| §6 Components changed | tasks 1-13 cover each row |
| §7 Migration / compat | 1 (config Load handles v0.5 absent block) |
| §8 Testing | 10 + 11 + 12 (integration); inline unit tests in 1-9 |
| §9 Acceptance criteria | 14's CI gate |
| §10 Future direction | not in scope |
| §11 Out of scope | not in scope |

**Placeholder scan:** none.

**Type consistency:** `RelayHealth` (exported) used in daemon, dashboard, IPC; `relayHealthStore` (private) is daemon-internal. Naming consistent.

**Risk callouts (handled at execution time):**
- Exact go-nostr `Relay.Auth` API may differ between versions — pin during Task 4 by reading the actual relay struct.
- khatru's `RequestAuth` returns nothing useful from a RejectFilter; the pattern in their handlers.go shows the canonical use.
- `InsecureSkipVerify` for TLS test needs a Pool-side knob; minimal test-only field.
