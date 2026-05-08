# MindGate AUTH, native TLS, and relay-health visibility

**Date**: 2026-05-08
**Status**: Approved (pending implementation)
**Target**: dev (the next release after v0.5.0)
**Scope**: Three coupled improvements that together close the v0.5 gaps for the "public Nostr relay" topology and the silent-failure class we hit during v0.5 rollout.

1. **NIP-42 AUTH end-to-end** — daemon-side client + relayd-side enforcement of AUTH-for-`kind:1059`-reads, per the NIP-17 §Recommendations. Closes the "subscription returns no events" silent failure when the home relay enforces AUTH (the universal posture of public Nostr relays).
2. **Native TLS in `relayd` (BYO certs)** — `[relay.tls]` config block; if both `cert_file` and `key_file` are set, the relay calls `ListenAndServeTLS` instead of `ListenAndServe`. Lets users serve `wss://` without a reverse proxy in front. Compatible with — but not a replacement for — the nginx / Caddy posture documented in INSTALL.md.
3. **Relay-health surface** — daemon-side per-URL connection state machine, exposed as a new dashboard SSE event kind (`relay.state`) and an IPC method (`relays.health`). `eidos gate status` and `eidos gate whoami` annotate URLs (`[connected]` / `[failed: x509: certificate signed by unknown authority]`). Replaces the current "tail journalctl to find out why my inbox is empty" workflow.

## 1. Problem statement

Three interlocking gaps surfaced during v0.5 deployment:

- **Public Nostr relays silently drop our subscriptions.** NIP-17 §Recommendations says relays SHOULD only serve `kind:1059` reads to clients authenticated as the `#p` recipient. Most public relays follow this. Our daemon does not implement NIP-42, so its REQ frames never get answered against AUTH-required relays. The user sees an empty inbox with no actionable signal.

- **Native `wss://` requires a reverse proxy.** `relayd` (khatru-backed) only serves plain HTTP. Putting the relay behind nginx + certbot is a six-step ritual (cert procurement, nginx site, relay re-bind to loopback, UFW changes, home URL on both sides, smoke test). For a user running just one MindGate relay, the friction is disproportionate to the value, and the failure modes (mis-typed `wss://` against a plain-ws relay, expired certs, nginx misconfig) cascade into silent connection drops.

- **Connection failures are invisible to the user.** When `Pool.Connect` errors, `runSubscriber` logs at WARN and retries with exponential backoff forever. `eidos gate status` shows the **process** (active, PID > 0) but not the subscription health. The recent `Pool.Connect` dead-cache eviction patch (`3b718e7`) covers one failure branch (peer relay restarted), but does not help when the URL is misconfigured or the cert is bad.

The three gaps share infrastructure (the daemon's per-relay state, the relay's listener, the dashboard's SSE hub), so we tackle them in a single milestone.

## 2. Design intent

Three commitments shape the rest:

1. **AUTH is automatic on the client; opt-in on the relay (with default-on for spec compliance).** The daemon responds to AUTH challenges with the gate's identity key — there is no knob; it always tries. The relayd enforces AUTH-for-`kind:1059`-reads when `[relay.auth].required = true` (the default after this release). Operators of shared / public-mode relays can flip `required = false` for posture experiments, but the spec-recommended default is on.

2. **TLS in the relay is convenience, not the only path.** The reverse-proxy posture (nginx / Caddy in front of plain-ws on loopback) remains documented and recommended for hosts already running other web services. `[relay.tls]` is for the single-purpose deployment ("I just want my one relay to serve `wss://`"). No ACME / autocert in this release — BYO certs only, renewal via the user's existing certbot / shell script.

3. **Health visibility is read-only and event-sourced.** The daemon owns the source of truth (per-URL state in `runSubscriber`), emits state-change events to the dashboard hub, and answers IPC reads on demand. Status commands and the dashboard panel are pure consumers; they don't drive the connection state machine.

## 3. NIP-42 AUTH

### 3.1 Wire format (recap)

NIP-42 challenge-response:
- Relay sends `["AUTH", <random-challenge>]` over the open WebSocket.
- Client constructs an event of kind `22242` with tags `["relay", "<wss-url>"]` and `["challenge", "<the-challenge>"]`, signs it with its long-term identity key.
- Client sends `["AUTH", <signed-event>]`. Relay validates signature → records the authenticated pubkey on the connection.

Tag `["relay", ...]` echoes the relay's own "service URL". `khatru.Relay.ServiceURL` (already exists at `relay.go:56`) is used for both the value sent in challenges and the value validated in client responses; the daemon must mirror that string in its tag.

### 3.2 Client side (daemon)

`internal/nostr/Pool` gains an AUTH handler. We wrap go-nostr's relay with our `Pool.Connect` already; on connection set-up, register `relay.OnAuth = func(ctx, challenge string) error` (or whatever `gnostr.Relay` exposes — pin the API at implementation time) that:

1. Builds the kind:22242 event with the connection's URL and the challenge.
2. Signs with `Pool.signer` (new field; populated by daemon at `NewPool` from `d.Key.PrivateHex`).
3. Returns the signed event for go-nostr to publish back over the same socket.

The signer is set on Pool once at daemon startup; it is the gate's own identity key. AUTH is purely a transport-layer assertion of "I am this pubkey"; it does not interact with envelope-v1 or the contacts model.

Threat model note: a malicious or buggy relay could reuse a signed AUTH event against a third party. NIP-42 §Considerations covers this — the `["relay", ...]` tag binds the signature to a specific relay URL, and clients must validate the URL matches before signing. Our handler asserts `tag.Relay == connURL` before signing.

### 3.3 Relay side (relayd)

`khatru` already implements the NIP-42 server protocol (`handlers.go:307` validates AUTH events; `RequestAuth(ctx)` pushes a challenge to the client). What we add is the **policy** — when to require AUTH and how to respond.

In `relayd.New`:

```go
if cfg.Auth.Required {
    r.RejectFilter = append(r.RejectFilter, requireAuthForKind1059Reads)
}
```

Where:

```go
func requireAuthForKind1059Reads(ctx context.Context, filter gnostr.Filter) (bool, string) {
    if !slices.Contains(filter.Kinds, 1059) {
        return false, ""  // not relevant
    }
    authed := khatru.GetAuthed(ctx)
    if authed == "" {
        khatru.RequestAuth(ctx)
        return true, "auth-required: kind:1059 reads require NIP-42 AUTH"
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

The first REJECT triggers `RequestAuth` so the client sees an AUTH challenge; subsequent REQs with the same filter succeed once AUTH is complete. This is exactly the pattern khatru's own examples document.

Writes (`RejectEvent`) are not changed in this release. The existing paired-mode rule (`kind == 1059 && p-tag matches OwnerHex`) is sufficient gatekeeping for paired-mode write traffic; public-mode relays accept any well-formed event today and continue to do so. AUTH-for-writes is a future option, gated on demand.

### 3.4 Configuration

```toml
[relay]
  enabled = true
  listen  = "0.0.0.0:22895"
  mode    = "paired"
  data_dir = "relay"

  [relay.auth]
    required = true            # default in this release; flip to false for an open relay
    service_url = "wss://yingte.io"   # optional; overrides khatru's auto-derived URL
```

`[relay.auth].required` defaults to `true` for new installs. Existing v0.5 installs upgrading do not have `[relay.auth]` in their config; the loader treats absent → `true` (spec-compliant default). Operators who explicitly want an unauthenticated public-mode relay set it to `false`.

`service_url` is optional: when the relay sits behind a reverse proxy, the proxy-facing URL (e.g. `wss://yingte.io`) differs from the relay's internal bind (`127.0.0.1:22895`). Without this override, khatru derives the URL from the incoming Host header, which is correct in most cases; we expose the override for the corner cases (multi-host nginx, X-Forwarded-* tricks, etc.).

### 3.5 Compatibility

- **v0.5 daemon ↔ dev relay (AUTH required)**: v0.5 has no AUTH client, so its REQ for `kind:1059` is rejected with `auth-required` and the daemon never authenticates. v0.5 user sees an empty inbox. **Documented as the upgrade path**: when bumping the relay to dev, all daemons that read from it must also be on dev. CHANGELOG carries a one-line warning.
- **dev daemon ↔ v0.5 relay (no AUTH)**: works. Daemon's AUTH handler is dormant unless the relay sends a challenge.
- **dev daemon ↔ public Nostr relay (Damus, nos.lol, etc.)**: works post-this-release; the relay sends AUTH, the daemon responds.
- **dev daemon ↔ dev relay with `auth.required = false`**: works. Daemon never sees a challenge.

## 4. Native TLS for relayd

### 4.1 Configuration

```toml
[relay]
  enabled = true
  listen  = "0.0.0.0:22895"
  mode    = "paired"

  [relay.tls]
    cert_file = "/etc/letsencrypt/live/yingte.io/fullchain.pem"
    key_file  = "/etc/letsencrypt/live/yingte.io/privkey.pem"
```

If `cert_file` and `key_file` are both set and both readable, `relayd` uses `http.ListenAndServeTLS(certFile, keyFile)`. Otherwise plain `ListenAndServe`. Setting one without the other is a startup error with a clear message.

### 4.2 Cert lifecycle

Out of scope for the relay binary:
- **Issuance**: certbot, acme.sh, or a one-shot manual `openssl` — all out-of-band. Standard certbot installs already cover this on hosts running multiple TLS services; nothing changes.
- **Renewal**: certbot's deploy-hook restarts the service (`systemctl --user restart eidos-gate-relay`). Documented in INSTALL.md alongside the cert-issuance steps.

The relay process reads `cert_file` and `key_file` once at startup — it does not watch for changes. A renewed cert requires a relay restart.

### 4.3 Self-signed certs

For local dev / testing, the user can run `openssl req -x509 -newkey rsa:4096 -days 365 -nodes -keyout key.pem -out cert.pem`, point `[relay.tls]` at those files, and configure clients to ignore validation. **The daemon does not have an "ignore TLS errors" flag** — self-signed cert use is not officially supported. Recommended for testing: use plain `ws://` over loopback or tailnet.

### 4.4 Interaction with `--listen`

Unchanged. `--listen 0.0.0.0:22895` with TLS configured = relay binds publicly on plain TCP and serves TLS-terminated WebSocket on the same socket. The home URL (`own_relays(role=home)`) should be `wss://your.host` once TLS is configured; the new `relays.health` surface (§5) makes a `wss://` home pointed at a non-TLS relay obvious instead of silent.

### 4.5 Why not autocert / ACME?

YAGNI: the user already has certbot on selene (the current target deployment); reusing it costs nothing. Autocert would:
- Require the relay to bind port 80 (HTTP-01 challenge) or own DNS API tokens (DNS-01).
- Add cert lifecycle code — cache dir, renewal goroutine, rate-limit handling.
- Couple certificate management to a single binary, which conflicts with multi-service hosts where another tool (caddy, nginx + certbot) is already authoritative.

If demand emerges (someone asks for "init and forget" TLS), autocert is a clean follow-on — `[relay.tls].auto_cert_for = "..."` slot in the same config block, no breaking change.

## 5. Relay-health visibility

### 5.1 Daemon-side state machine

`runSubscriber` (`internal/daemon/daemon.go`) currently spawns a single goroutine that tries to subscribe to the union of `own_relays`, contact relays, and `subscribe.extra_relays`. We split per-URL state tracking out:

```go
type relayState struct {
    URL          string
    Role         string                   // "home" | "fallback" | "contact" | "extra"
    State        string                   // "pending" | "connecting" | "connected" | "error" | "auth-failed"
    LastError    string                   // human-readable; empty when State != "error"
    LastEventAt  time.Time                // last received gift wrap from this URL
    UpdatedAt    time.Time
}
```

Daemon owns a `relayHealth map[string]*relayState` protected by a sync.RWMutex. State transitions happen at three points:

- **Before connect**: state := `connecting`, emit event.
- **After dial**: success → `connected`, failure → `error` with `LastError = err.Error()`.
- **AUTH failure** (relay returned `auth-required` or our AUTH event was rejected): state := `auth-failed` with the relay's reason in `LastError`.
- **Event received**: `LastEventAt = now`. State stays `connected`. No event emitted (would be too noisy).

The dashboard adapter subscribes to a new `relay.state` event kind on the daemon's hub.

### 5.2 Dashboard event

```go
// internal/dashboard/deps.go — Event union extended:
type Event struct {
    Kind     string
    Message  *inbox.Message
    Sent     *inbox.Sent
    Contact  *contacts.Contact
    Relay    *RelayState     // populated when Kind == "relay.state"
}

type RelayState struct {
    URL         string
    Role        string
    State       string
    LastError   string
    LastEventAt int64        // unix seconds; 0 when never
}
```

A dashboard panel renders the relay table with colored dots (green/yellow/red) and the last error string as a tooltip. SSE event name: `relay.state` — fan-out follows the existing pattern.

### 5.3 IPC + CLI

New IPC method `relays.health` returns `[]RelayState`. CLI consumers:

- `eidos gate status` gains a "Relays" section after the unit table:

  ```
  $ eidos gate status
    eidos-gate-daemon      active  pid=4123
    eidos-gate-relay       active  pid=4131

  Relays:
    home      ws://yingte.io:22895       connected  (last event 2s ago)
    fallback  wss://relay.damus.io       auth-failed (auth-mismatch: requested #p does not match AUTH pubkey)
  ```

- `eidos gate whoami` annotates each home / fallback URL inline (`wss://yingte.io [connected]` / `wss://relay.damus.io [failed: x509: certificate signed by unknown authority]`).

Both commands are pure consumers of `relays.health`; they do not introduce new state.

### 5.4 What this surface does NOT do

- It does not auto-recover or auto-restart subscriptions. The retry loop is unchanged; this surface only reports.
- It does not alert. No emails, no inbox pings. Operators read the dashboard / `gate status`.
- It does not store history. Last-error and last-event-at are ephemeral; daemon restart resets them.

## 6. Components changed

| Path | Change |
|---|---|
| `internal/config/config.go` | Add `Relay.Auth.Required bool` (default true), `Relay.Auth.ServiceURL string`, `Relay.TLS.CertFile string`, `Relay.TLS.KeyFile string`. |
| `internal/relayd/relayd.go` | Branch `New` on `cfg.Auth.Required` → register `requireAuthForKind1059Reads` filter. Branch `ListenAndServe` on `cfg.TLS.CertFile != ""` → call `ListenAndServeTLS`. Surface a startup error when only one of cert/key is set. |
| `internal/relayd/relayd_test.go` | New tests: AUTH-required relay rejects unauthenticated REQ; AUTH-required relay accepts authenticated REQ matching `#p`; AUTH-required relay rejects mismatched `#p`; TLS startup with valid + mismatched + missing certs. |
| `internal/nostr/pool.go` | Add `signer Signer` field on `Pool`; `NewPoolWithSigner` constructor. AUTH handler wired on each connection's `OnAuth`. Builds + signs kind:22242 with `["relay", connURL]` and `["challenge", challenge]` tags. |
| `internal/nostr/pool_test.go` | New tests: AUTH handler builds correct event shape; URL mismatch refuses to sign (defends against the §Considerations replay attack). |
| `internal/daemon/daemon.go` | New `relayHealth` map + `setRelayState` helper. `runSubscriber` updates state at connect / connected / error transitions. Existing log lines preserved for journal-based debugging. |
| `internal/daemon/dashboard_adapter.go` | Forward `relay.state` events to the dashboard hub. |
| `internal/dashboard/deps.go` | Extend `Event` union with `RelayState`. |
| `internal/dashboard/sse.go` | Handle `relay.state` event kind in `renderEvent`. |
| `internal/dashboard/templates/` | New `relays.html` partial; include in main view. |
| `internal/dashboard/handlers.go` | `GET /relays` endpoint returns the partial — for HTMX target after SSE refresh. |
| `internal/daemon/methods.go` | Register `relays.health` IPC method; returns `[]RelayState`. |
| `cmd/eidos/gate/status.go` | Append Relays section after unit table. |
| `cmd/eidos/gate/whoami.go` | Annotate URLs with state. |
| `docs/INSTALL.md` | New "Native TLS" subsection (BYO cert config). New "Reverse-proxy posture" subsection covering relay + dashboard together. CHANGELOG migration note for v0.5 → dev. |
| `docs/USAGE.md` | Public-Nostr-relay topology gains "now production-ready" callout (AUTH works). |

**Not changed**: `internal/identity/`, `internal/invite/`, `internal/envelope/`, `internal/contacts/`, `internal/inbox/`, `internal/store/`, the contacts tier model.

## 7. Migration / compatibility

### 7.1 v0.5 → dev

- **Config schema**: dev config gains `[relay.auth]` and `[relay.tls]` blocks. v0.5 configs lack them; defaults apply (`auth.required = true`, no TLS).
- **No state-dir break**: unlike v0.5's clean break from v0.4, this release's config additions are **additive**. The v0.4-detection hook (`detectV04State`, established in v0.5) stays unchanged; any state directory whose `config.toml` has `[relay].enabled` defined is treated as dev-compatible. No new detection hook.
- **Behavioral break for the `auth.required = true` default**: v0.5 daemons reading from a dev-upgraded relay get empty inboxes (REQ rejected). CHANGELOG carries this prominently.

### 7.2 The single-host install on selene (current target)

After this release, selene's TLS path:

1. Issue cert via certbot (existing tooling on selene): `sudo certbot certonly --standalone -d yingte.io --pre-hook 'eidos gate stop' --post-hook 'eidos gate start'`. Cert at `/etc/letsencrypt/live/yingte.io/`.
2. `eidos gate config set relay.tls.cert_file /etc/letsencrypt/live/yingte.io/fullchain.pem`
3. `eidos gate config set relay.tls.key_file /etc/letsencrypt/live/yingte.io/privkey.pem`
4. Update home: `eidos gate relay-add wss://yingte.io --role home && eidos gate relay-remove ws://yingte.io:22895`
5. `eidos gate stop && eidos gate start`

mbp side: `eidos gate relay-add wss://yingte.io --role home && eidos gate relay-remove <old>`. Re-redeem invite if its embedded URL is stale.

The friction here is the same as the nginx posture (~5 commands), but no nginx process and no separate site config — relay binary owns the TLS.

## 8. Testing

### 8.1 Unit

| File | Coverage |
|---|---|
| `internal/config/config_test.go` | Defaults: `Relay.Auth.Required == true`, TLS fields empty. Round-trip with explicit `[relay.auth]` and `[relay.tls]` blocks. v0.5 config (no `[relay.auth]` / `[relay.tls]`) loads with defaults applied. |
| `internal/nostr/pool_test.go` | AUTH handler signs the right event shape; refuses to sign when `["relay", url]` doesn't match the connection URL; signer absent → returns error. |
| `internal/relayd/relayd_test.go` | AUTH-required relay: unauthenticated REQ for `kind:1059` returns `auth-required: ...`. Authenticated REQ matching `#p` succeeds. Authenticated REQ with mismatched `#p` returns `auth-mismatch: ...`. AUTH-required REQ for non-1059 kinds is unaffected. `auth.required = false` accepts any REQ. |
| `internal/relayd/relayd_test.go` (TLS) | Both cert/key set + valid → `ListenAndServeTLS`. One set + one missing → startup error. Cert file unreadable → startup error. |
| `internal/daemon/daemon_test.go` | `runSubscriber` transitions state correctly across connect / connected / error / auth-failed. `relays.health` IPC returns the current map. |
| `internal/dashboard/sse_test.go` | `relay.state` event renders correctly; HTMX target matches expectation. |
| `cmd/eidos/gate/status_test.go` (new) | `printStatus` appends Relays section when health data is available; falls back gracefully when daemon hasn't reported yet. |

### 8.2 Integration

`test/integration/auth_roundtrip_test.go` (new):
1. Bring up A with `bringUp(t, "alice", relayd.ModePaired)` — paired-mode relay with AUTH required.
2. Bring up B as daemon-only against A's relay (`bringUpDaemonOnly`).
3. A and B mutually `add-contact`.
4. B sends a chat envelope to A. Assert: A's relay sent an AUTH challenge to B's daemon, B's daemon authed, REQ filter on B's side returned the gift wrap, B's inbox receives.
5. **Negative**: bring up C with no AUTH client (mock by stubbing the Pool's signer to nil). Assert: C's REQ is rejected, C's `relays.health` shows `auth-failed` for A's URL.

`test/integration/relayd_tls_test.go` (new): generate a self-signed cert at test setup, point `[relay.tls]` at it, verify the daemon connects via `wss://localhost:port` with `InsecureSkipVerify` for the test only.

`test/integration/relay_health_test.go` (new): verify `relays.health` IPC returns expected state across connect / disconnect cycles. Use `freePort` + start/stop a fake relay to exercise the error → connected → error transitions.

### 8.3 Smoke (manual, in CHANGELOG)

```sh
# v0.5 → dev upgrade (single-host, selene):
sudo certbot certonly --standalone -d yingte.io --pre-hook 'eidos gate stop' --post-hook 'eidos gate start'
eidos gate config set relay.tls.cert_file /etc/letsencrypt/live/yingte.io/fullchain.pem
eidos gate config set relay.tls.key_file  /etc/letsencrypt/live/yingte.io/privkey.pem
eidos gate relay-add wss://yingte.io --role home
eidos gate relay-remove ws://yingte.io:22895
eidos gate stop && eidos gate start
eidos gate status        # Relays: home wss://yingte.io [connected]

# Public Nostr relay topology:
eidos gate relay-add wss://relay.damus.io --role fallback
eidos gate status        # Relays: fallback wss://relay.damus.io [connected] — proves AUTH worked
```

## 9. Acceptance criteria

- `internal/config/config.go` has `Relay.Auth.Required` (default true), `Relay.Auth.ServiceURL`, `Relay.TLS.CertFile`, `Relay.TLS.KeyFile`. Round-trip tests pass.
- `internal/nostr/Pool` accepts a signer and responds to AUTH challenges with a correctly-shaped kind:22242 event whose `["relay", ...]` tag matches the connection URL.
- `internal/relayd` rejects unauthenticated `kind:1059` REQ frames when `auth.required = true`, accepts authenticated REQ matching `#p`, rejects mismatched `#p`. Existing paired-mode write rule unchanged.
- `internal/relayd` calls `ListenAndServeTLS` when both cert/key paths are set; errors at startup when one is set without the other.
- Daemon tracks per-URL connection state (`pending`/`connecting`/`connected`/`error`/`auth-failed`) and exposes it via `relays.health` IPC + dashboard `relay.state` SSE events.
- `eidos gate status` shows a Relays section. `eidos gate whoami` annotates URLs.
- Dashboard renders a Relays panel with state colors and last-error tooltips.
- `go test ./...` and `go test -tags=integration ./...` pass. `gofmt`/`vet`/`staticcheck` clean.
- README quick-start shows the public-Nostr-relay topology working end-to-end (no caveat about silently-empty inbox).
- INSTALL.md has new "Native TLS" subsection alongside the existing "Self-hosting the embedded relay".

## 10. Future direction

- **Autocert / ACME** — `[relay.tls].auto_cert_for = "..."` using `golang.org/x/crypto/acme/autocert`. Trigger: a user asks for "init and forget" TLS, AND nobody on the team objects to coupling the cert lifecycle to the relay binary.
- **AUTH for writes** — extend `RejectEvent` to require AUTH for writes when `auth.required_for_writes = true`. Trigger: spam mitigation needs on a public-mode shared relay become a real complaint.
- **Cert reload without restart** — file-watcher on `cert_file` + graceful reload via `http.Server.Shutdown` + restart. Trigger: a deployment that can't tolerate the second of downtime certbot's deploy-hook causes.
- **NIP-65 relay-list discovery** — kind:10002 events for auto-discovering peers' relays. Tangential to AUTH/TLS/health; tracked separately.

## 11. Out of scope

- ACME / autocert (see §10).
- Cert reload without restart (see §10).
- AUTH-for-writes (see §10).
- Per-user fine-grained AUTH policies (e.g. allow-list of pubkeys allowed to read non-`#p` filters). NIP-17 doesn't ask for this; if a future feature does, design from the consumer.
- Dashboard authentication. Loopback-only deployment is the contract per the dashboard spec; this release doesn't change it.
- AUTH-driven contact management (e.g. "auto-add a peer when their AUTH'd npub appears"). Contacts stay explicit.
- Wire-protocol additions to envelope-v1. Strict YAGNI per project memory: no `client.relay_caps`, no AUTH state echoed in the envelope, until a concrete consumer asks.
