# MindGate Local Web Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a loopback-bound HTTP dashboard inside `eidos gate daemon` that surfaces inbox/outbox/send/contacts as a chat-app-style UI, with a supplementary list view for "All messages" / "Soft-rejected".

**Architecture:** New `internal/dashboard/` package owns the HTTP listener, templates, static assets, and SSE hub. Daemon launches it as another goroutine in `Run`. Dashboard handlers depend on a narrow `DashboardDeps` interface satisfied by a `*Daemon` adapter, so they can be unit-tested with stubs. Live updates use Server-Sent Events fanned out from the existing `broadcastInbox` path. No new IPC methods, no new mutating endpoints; only chat-send reuses the existing `sendMessage` code.

**Tech Stack:** Go 1.25, `html/template`, `embed.FS`, htmx (vendored, ~14KB), htmx-sse extension, hand-written CSS (no preprocessor, no framework).

**Reference spec:** `docs/superpowers/specs/2026-05-07-dashboard-design.md`

---

## File map

| Path | Action | Responsibility |
|---|---|---|
| `internal/config/config.go` | modify | Add `DashboardConfig` struct + default |
| `internal/config/config_test.go` | modify | Default value test for the new section |
| `internal/dashboard/server.go` | create | HTTP listener + lifecycle (`Run(ctx, deps, cfg, logger)`) |
| `internal/dashboard/deps.go` | create | `DashboardDeps` interface + `DashboardEvent` type |
| `internal/dashboard/adapter.go` | create (in `internal/daemon/`) — actually `internal/daemon/dashboard_adapter.go` to satisfy DashboardDeps without an import cycle | adapter from `*Daemon` to `dashboard.DashboardDeps` |
| `internal/dashboard/handlers.go` | create | All HTTP handlers (shell, sidebar, thread, messages, send, compose) |
| `internal/dashboard/sse.go` | create | SSE hub: per-client channels, write loop, named events |
| `internal/dashboard/render.go` | create | Template parse + cache; `Render(name, data)` helper; relative-time + tier-badge funcs |
| `internal/dashboard/pagination.go` | create | Cursor encode/decode for `/messages` |
| `internal/dashboard/templates/*.html` | create | shell, sidebar, thread, bubble, messages, compose |
| `internal/dashboard/static/htmx.min.js` | create (vendor) | htmx 2.x bundle |
| `internal/dashboard/static/htmx-ext-sse.js` | create (vendor) | SSE extension |
| `internal/dashboard/static/app.css` | create | Hand-written styles |
| `internal/daemon/daemon.go` | modify | Daemon gains a tiny SSE-hub plumbing; `Run` launches dashboard goroutine |
| `cmd/eidos/gate/dashboard.go` | create | `eidos gate dashboard [--no-open]` subcommand |
| `test/integration/dashboard_test.go` | create | Spin daemon, GET `/`, POST send, verify SSE event arrives |
| `docs/USAGE.md` | modify | Document `eidos gate dashboard` |

---

## Task 1: DashboardConfig in config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add the failing test for the default**

Append to `internal/config/config_test.go`:

```go
func TestDefaults_Dashboard(t *testing.T) {
	d := Defaults()
	if !d.Dashboard.Enabled {
		t.Error("dashboard enabled by default")
	}
	if d.Dashboard.Listen != "127.0.0.1:22893" {
		t.Errorf("dashboard listen default: got %q want 127.0.0.1:22893", d.Dashboard.Listen)
	}
}
```

- [ ] **Step 2: Run; expect compile failure**

Run: `go test ./internal/config/... -run TestDefaults_Dashboard -v`
Expected: build error — `Dashboard` undefined.

- [ ] **Step 3: Add the struct + default**

In `internal/config/config.go`:

In the `Config` struct add:

```go
	Dashboard DashboardConfig `toml:"dashboard"`
```

(Place after `Subscribe SubscribeConfig` so the field order matches the TOML order.)

Add the type and default block:

```go
type DashboardConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"`
}
```

In `Defaults()` add:

```go
		Dashboard: DashboardConfig{
			Enabled: true,
			Listen:  "127.0.0.1:22893",
		},
```

- [ ] **Step 4: Run; expect pass**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "$(cat <<'EOF'
feat(config): DashboardConfig with loopback default

Adds [dashboard] section to config.toml with enabled=true and
listen=127.0.0.1:22893. Foundation for the embedded HTTP dashboard;
non-loopback listen is refused by the dashboard server in a later
task with an explicit log line.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Dashboard package skeleton — types and dependency interface

**Files:**
- Create: `internal/dashboard/deps.go`
- Create: `internal/dashboard/server.go`
- Create: `internal/dashboard/server_test.go`

- [ ] **Step 1: Write the failing test for the loopback-refusal behavior**

Create `internal/dashboard/server_test.go`:

```go
package dashboard

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// captureLogs returns a logger and a func that returns all log lines
// captured to date.
func captureLogs(t *testing.T) (*slog.Logger, func() string) {
	t.Helper()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(stringWriter{&buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, func() string { return buf.String() }
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { w.b.Write(p); return len(p), nil }

func TestRun_NonLoopbackBindRefused(t *testing.T) {
	logger, getLogs := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: true, Listen: "0.0.0.0:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on non-loopback: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after non-loopback refusal")
	}
	if !strings.Contains(getLogs(), "non-loopback bind not supported") {
		t.Errorf("expected non-loopback refusal log line; got: %s", getLogs())
	}
}

func TestRun_DisabledIsNoop(t *testing.T) {
	logger, _ := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: false, Listen: "127.0.0.1:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error when disabled: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return when disabled")
	}
}

// fakeDeps is a stub used by handler/dispatch tests in this package.
type fakeDeps struct{}

func (fakeDeps) OwnPubkey() string                             { return "" }
func (fakeDeps) OwnLabel(context.Context) (string, error)      { return "", nil }
func (fakeDeps) ListInbox(*time.Time, string, int) ([]inboxMessageStub, error) {
	return nil, nil
}

// (full stubs come in Task 5 once the deps surface stabilizes)
var _ = io.Discard
```

The fake stub is intentionally tiny here — we'll grow it as handlers come online. Use `inboxMessageStub` so this test file compiles before the deps interface mentions `inbox.Message`. We'll replace the stub with the real type in Task 5.

- [ ] **Step 2: Run; expect compile failure (Run, fakeDeps signatures undefined)**

Run: `go test ./internal/dashboard/... -v`
Expected: build error.

- [ ] **Step 3: Create deps.go with the dependency interface scaffold**

Create `internal/dashboard/deps.go`:

```go
// Package dashboard implements the local web dashboard for MindGate.
// See docs/superpowers/specs/2026-05-07-dashboard-design.md.
package dashboard

import (
	"context"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// DashboardDeps is the narrow surface the HTTP handlers consume from the
// daemon. The interface is satisfied by an adapter on *daemon.Daemon
// (see internal/daemon/dashboard_adapter.go); handler tests use a stub.
type DashboardDeps interface {
	OwnPubkey() string
	OwnLabel(ctx context.Context) (string, error)

	ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error)
	ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error)
	ListContacts(ctx context.Context) ([]*contacts.Contact, error)

	Send(ctx context.Context, toPubkey string, env envelope.Envelope) (eventID string, err error)

	SubscribeEvents() (ch <-chan Event, cancel func())
}

// Event is the SSE-bound broadcast type. Kind discriminates the union;
// only the matching pointer field is populated.
type Event struct {
	Kind    string
	Message *inbox.Message
	Sent    *inbox.Sent
	Contact *contacts.Contact
}
```

- [ ] **Step 4: Create server.go with Run + the loopback gate**

Create `internal/dashboard/server.go`:

```go
package dashboard

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// Run starts the dashboard HTTP server and blocks until ctx is cancelled.
// Returns nil on clean shutdown and on configuration-driven no-ops
// (disabled, non-loopback bind refusal, listen failure).
func Run(ctx context.Context, deps DashboardDeps, cfg config.DashboardConfig, logger *slog.Logger) error {
	if !cfg.Enabled {
		logger.Info("dashboard disabled by config")
		return nil
	}
	if !isLoopback(cfg.Listen) {
		logger.Error("dashboard non-loopback bind not supported in v1; skipping", "listen", cfg.Listen)
		return nil
	}

	mux := http.NewServeMux()
	registerHandlers(mux, deps, logger) // implemented in Task 6+

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           sameOriginGuard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		logger.Info("dashboard listening", "url", "http://"+cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-listenErr:
		if err != nil {
			logger.Error("dashboard listen failed", "err", err)
		}
		return nil
	}
}

// isLoopback reports whether the host portion of "host:port" is a loopback
// address. A bare port (e.g. ":22893") is treated as non-loopback because
// it binds all interfaces.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// sameOriginGuard rejects requests whose Origin header is set and does not
// match Host. Browsers omit Origin on plain GETs from the address bar, so
// missing Origin is treated as same-origin. POSTs from cross-origin
// contexts always carry Origin and will be rejected.
func sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || origin == "null" {
			next.ServeHTTP(w, r)
			return
		}
		// Origin = scheme://host[:port]. Compare host portion to r.Host.
		if i := strings.Index(origin, "://"); i >= 0 {
			origin = origin[i+3:]
		}
		if origin != r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// registerHandlers is implemented incrementally; Task 6+ fill it in.
// For Task 2, define a stub that returns 404 for everything so Run can
// stand up without compile errors.
func registerHandlers(mux *http.ServeMux, deps DashboardDeps, logger *slog.Logger) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
}
```

- [ ] **Step 5: Replace the placeholder stub in `server_test.go` with an inboxMessageStub-free version that uses the real interface**

Replace the file contents with the version that compiles against `deps.go`:

```go
package dashboard

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

func captureLogs(t *testing.T) (*slog.Logger, func() string) {
	t.Helper()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(stringWriter{&buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, func() string { return buf.String() }
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { w.b.WriteString(string(p)); return len(p), nil }

func TestRun_NonLoopbackBindRefused(t *testing.T) {
	logger, getLogs := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: true, Listen: "0.0.0.0:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on non-loopback: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after non-loopback refusal")
	}
	if !strings.Contains(getLogs(), "non-loopback bind not supported") {
		t.Errorf("expected non-loopback refusal log line; got: %s", getLogs())
	}
}

func TestRun_DisabledIsNoop(t *testing.T) {
	logger, _ := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: false, Listen: "127.0.0.1:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error when disabled: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return when disabled")
	}
}

// fakeDeps is the package-internal stub satisfying DashboardDeps for handler
// unit tests. Behaviour is overridable per-test via assignable fields.
type fakeDeps struct {
	pubkey     string
	label      string
	inbox      []inbox.Message
	outbox     []inbox.Sent
	contactsL  []*contacts.Contact
	sendErr    error
	sendID     string
	subscribed []chan Event
}

func (f fakeDeps) OwnPubkey() string                          { return f.pubkey }
func (f fakeDeps) OwnLabel(context.Context) (string, error)   { return f.label, nil }
func (f fakeDeps) ListInbox(*time.Time, string, int) ([]inbox.Message, error) {
	return f.inbox, nil
}
func (f fakeDeps) ListOutbox(*time.Time, string, int) ([]inbox.Sent, error) {
	return f.outbox, nil
}
func (f fakeDeps) ListContacts(context.Context) ([]*contacts.Contact, error) {
	return f.contactsL, nil
}
func (f fakeDeps) Send(_ context.Context, _ string, _ envelope.Envelope) (string, error) {
	return f.sendID, f.sendErr
}
func (f fakeDeps) SubscribeEvents() (<-chan Event, func()) {
	ch := make(chan Event, 4)
	return ch, func() { close(ch) }
}
```

- [ ] **Step 6: Run; expect both tests pass**

Run: `go test ./internal/dashboard/... -v`
Expected: PASS for both tests.

- [ ] **Step 7: Vet + gofmt**

Run: `gofmt -l internal/dashboard/ && go vet ./internal/dashboard/...`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard/
git commit -m "$(cat <<'EOF'
feat(dashboard): package skeleton with loopback gate

DashboardDeps interface, Event union type, Run() lifecycle with two
guard rails: cfg.Enabled=false short-circuits to no-op; non-loopback
listen logs an error and returns without binding. sameOriginGuard
defends against same-host cross-origin POSTs in the absence of auth.
fakeDeps stub satisfies the interface for handler unit tests.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: SSE hub on the daemon side

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/dispatch_test.go` (add a hub-fanout test)
- Create: `internal/daemon/dashboard_adapter.go`

- [ ] **Step 1: Write a failing test for the hub fan-out**

Append to `internal/daemon/dispatch_test.go`:

```go
func TestDashboardHub_FanOutOnInbox(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	a := dashboardAdapter{d: d}
	ch1, cancel1 := a.SubscribeEvents()
	defer cancel1()
	ch2, cancel2 := a.SubscribeEvents()
	defer cancel2()

	from := "from-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)

	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-hub"}, makeRumor(from, content))

	for i, ch := range []<-chan dashboard.Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Kind != "inbox.message" {
				t.Errorf("subscriber %d kind %q want inbox.message", i, ev.Kind)
			}
			if ev.Message == nil || ev.Message.From != from {
				t.Errorf("subscriber %d missing message data: %+v", i, ev)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("subscriber %d did not receive event", i)
		}
	}
}
```

You'll need to add an import for `dashboard` at the top of dispatch_test.go (the package, not the deps package).

- [ ] **Step 2: Run; expect compile failure**

Run: `go test ./internal/daemon/... -run TestDashboardHub -v`
Expected: build error — `dashboard.Event` and `dashboardAdapter` undefined.

- [ ] **Step 3: Add the hub state and adapter**

In `internal/daemon/daemon.go` add to the `Daemon` struct (after `selfWrapIDs`):

```go
	dashSubs []chan dashboard.Event
```

Import `dashboard` at the top:

```go
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
```

Add hub methods:

```go
// subscribeDashboard registers a new SSE-bound subscriber. The returned
// cancel function closes the channel and removes the subscriber from the
// fan-out list. Channel buffer is small (4) so a slow client falls
// behind quickly; on send-blocked, broadcast drops the event for that
// subscriber.
func (d *Daemon) subscribeDashboard() (<-chan dashboard.Event, func()) {
	ch := make(chan dashboard.Event, 4)
	d.mu.Lock()
	d.dashSubs = append(d.dashSubs, ch)
	d.mu.Unlock()
	cancel := func() {
		d.mu.Lock()
		for i, c := range d.dashSubs {
			if c == ch {
				d.dashSubs = append(d.dashSubs[:i], d.dashSubs[i+1:]...)
				break
			}
		}
		d.mu.Unlock()
		// drain any pending send before close
		select {
		case <-ch:
		default:
		}
		close(ch)
	}
	return ch, cancel
}

func (d *Daemon) emitDashEvent(ev dashboard.Event) {
	d.mu.Lock()
	subs := make([]chan dashboard.Event, len(d.dashSubs))
	copy(subs, d.dashSubs)
	d.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			d.Log.Warn("dashboard subscriber slow; dropping event", "kind", ev.Kind)
		}
	}
}
```

Modify `broadcastInbox` to also emit to dashboard subscribers:

```go
func (d *Daemon) broadcastInbox(m inbox.Message) {
	d.mu.Lock()
	subs := make([]*ipc.Conn, len(d.subs))
	copy(subs, d.subs)
	d.mu.Unlock()
	for _, c := range subs {
		_ = c.PushEvent("inbox.message", m)
	}
	mc := m
	d.emitDashEvent(dashboard.Event{Kind: "inbox.message", Message: &mc})
}
```

Modify `broadcastContactAdded` similarly:

```go
func (d *Daemon) broadcastContactAdded(data any) {
	d.mu.Lock()
	subs := make([]*ipc.Conn, len(d.subs))
	copy(subs, d.subs)
	d.mu.Unlock()
	for _, c := range subs {
		_ = c.PushEvent("contact.added", data)
	}
	// Convert the loose `any` payload to a Contact pointer when possible;
	// for the dashboard we want the structured object.
	if m, ok := data.(map[string]any); ok {
		if pk, _ := m["pubkey"].(string); pk != "" {
			if c, err := d.Repo.Get(context.Background(), pk); err == nil {
				d.emitDashEvent(dashboard.Event{Kind: "contact.added", Contact: c})
				return
			}
		}
	}
	d.emitDashEvent(dashboard.Event{Kind: "contact.added"})
}
```

- [ ] **Step 4: Create the adapter file**

Create `internal/daemon/dashboard_adapter.go`:

```go
package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

// dashboardAdapter wraps *Daemon to satisfy dashboard.DashboardDeps.
// Defined in the daemon package to avoid an import cycle (dashboard
// cannot import daemon).
type dashboardAdapter struct{ d *Daemon }

// NewDashboardAdapter exposes the adapter so daemon.Run can pass it to
// dashboard.Run. Returned as the interface so callers cannot reach into
// the daemon directly.
func NewDashboardAdapter(d *Daemon) dashboard.DashboardDeps { return dashboardAdapter{d: d} }

func (a dashboardAdapter) OwnPubkey() string { return a.d.Key.PublicHex }

func (a dashboardAdapter) OwnLabel(ctx context.Context) (string, error) {
	return a.d.DB.GetMeta(ctx, "label")
}

func (a dashboardAdapter) ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error) {
	return a.d.Box.ListInbox(since, from, limit)
}

func (a dashboardAdapter) ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error) {
	return a.d.Box.ListOutbox(since, to, limit)
}

func (a dashboardAdapter) ListContacts(ctx context.Context) ([]*contacts.Contact, error) {
	return a.d.Repo.List(ctx)
}

func (a dashboardAdapter) Send(ctx context.Context, toPubkey string, env envelope.Envelope) (string, error) {
	content, err := envelope.Encode(env)
	if err != nil {
		return "", err
	}
	wrapBob, _, err := nostr.Wrap(a.d.Key.PrivateHex, toPubkey, content)
	if err != nil {
		return "", err
	}
	wrapSelf, _, err := nostr.Wrap(a.d.Key.PrivateHex, a.d.Key.PublicHex, content)
	if err != nil {
		return "", err
	}
	a.d.recordSelfWrap(wrapSelf.ID)

	urls, err := a.d.ownRelayURLs(ctx)
	if err != nil {
		return "", err
	}
	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := a.d.Pool.Publish(publishCtx, urls, wrapBob)
	_ = a.d.Pool.Publish(publishCtx, urls, wrapSelf)

	for _, r := range res {
		if r.OK {
			return wrapBob.ID, nil
		}
	}
	return "", fmt.Errorf("no relay accepted publish on %d urls", len(urls))
}

func (a dashboardAdapter) SubscribeEvents() (<-chan dashboard.Event, func()) {
	return a.d.subscribeDashboard()
}
```

If `DB.GetMeta` doesn't exist (check `internal/store/`), use the existing pattern that other code uses to read `label` (look at `whoami` in `internal/daemon/methods.go` for the exact call). If the store has `GetMeta(ctx, key) (string, error)` use that; otherwise fall back to a direct `QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", "label")`.

- [ ] **Step 5: Run the new test**

Run: `go test ./internal/daemon/... -run TestDashboardHub -v`
Expected: PASS.

- [ ] **Step 6: Run the full daemon test suite**

Run: `go test ./internal/daemon/...`
Expected: PASS.

- [ ] **Step 7: Vet + gofmt**

Run: `gofmt -l internal/daemon/ && go vet ./internal/daemon/...`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/dashboard_adapter.go internal/daemon/dispatch_test.go
git commit -m "$(cat <<'EOF'
feat(daemon): SSE-bound dashboard event hub

Daemon gains a small fan-out (dashSubs []chan dashboard.Event) that
broadcastInbox and broadcastContactAdded write to alongside the
existing IPC PushEvent path. Dashboard subscribers are bounded
(buffer 4); slow consumers are dropped with a log line, never
blocking the broadcaster. dashboardAdapter wraps *Daemon to satisfy
dashboard.DashboardDeps without an import cycle (daemon imports
dashboard, not the reverse).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Static assets — vendor htmx and write base CSS

**Files:**
- Create: `internal/dashboard/static/htmx.min.js`
- Create: `internal/dashboard/static/htmx-ext-sse.js`
- Create: `internal/dashboard/static/app.css`

- [ ] **Step 1: Vendor htmx 2.0.4 (or current LTS)**

Run:

```bash
mkdir -p internal/dashboard/static
curl -fsSL -o internal/dashboard/static/htmx.min.js \
    https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js
curl -fsSL -o internal/dashboard/static/htmx-ext-sse.js \
    https://unpkg.com/htmx-ext-sse@2.2.2/sse.js
```

Verify:

```bash
ls -la internal/dashboard/static/
sha256sum internal/dashboard/static/*.js
```

Expected: two files present, ~50KB and ~10KB respectively.

- [ ] **Step 2: Add audit comment headers**

Prepend a one-line vendor-source comment to each (do not modify the actual source after the comment). Use Read to view the current first line, then Edit to add the comment as a new first line:

For htmx.min.js:
```
/*! htmx.org 2.0.4 — vendored from https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js — see internal/dashboard/static/README.md for upgrade procedure */
```

For htmx-ext-sse.js:
```
/*! htmx-ext-sse 2.2.2 — vendored from https://unpkg.com/htmx-ext-sse@2.2.2/sse.js — see internal/dashboard/static/README.md for upgrade procedure */
```

Create `internal/dashboard/static/README.md`:

```markdown
# Dashboard static assets

These files are vendored to keep the build pipeline npm-free.

| File | Upstream | Version | Why vendored |
|---|---|---|---|
| `htmx.min.js` | https://unpkg.com/htmx.org | 2.0.4 | Core dashboard interactivity |
| `htmx-ext-sse.js` | https://unpkg.com/htmx-ext-sse | 2.2.2 | SSE swap for live tail |

## Upgrade procedure

1. Download new version from unpkg.
2. Update the version in this README and in the audit comment at the top of the file.
3. Run `go test ./...` and `go test -tags=integration ./test/integration/...`.
4. Eyeball the dashboard against a running daemon.

No automated upgrades; htmx is reviewed manually.
```

- [ ] **Step 3: Write the starter CSS**

Create `internal/dashboard/static/app.css`. This is a minimum-viable starting point. Task 12 (frontend craft) will polish it under the `frontend-design` skill.

```css
/* eidos-gate dashboard — base layout. Polished under frontend-design skill in a later task. */

:root {
  --bg: #0e0f12;
  --bg-2: #15171c;
  --fg: #e3e6eb;
  --fg-dim: #9aa0a8;
  --accent: #7aa9ff;
  --accent-soft: rgba(122, 169, 255, 0.15);
  --warn: #d97a7a;
  --warn-soft: rgba(217, 122, 122, 0.15);
  --border: rgba(255, 255, 255, 0.07);
  --shadow: 0 1px 2px rgba(0, 0, 0, 0.4);
  --radius: 6px;
  --gap: 12px;
}

@media (prefers-color-scheme: light) {
  :root {
    --bg: #fafbfc;
    --bg-2: #ffffff;
    --fg: #14171c;
    --fg-dim: #5b6470;
    --accent: #2563eb;
    --accent-soft: rgba(37, 99, 235, 0.10);
    --warn: #b13d3d;
    --warn-soft: rgba(177, 61, 61, 0.10);
    --border: rgba(0, 0, 0, 0.07);
    --shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
  }
}

* { box-sizing: border-box; }

body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 14px;
  line-height: 1.5;
  background: var(--bg);
  color: var(--fg);
  height: 100vh;
  display: flex;
  flex-direction: column;
}

header.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 16px;
  background: var(--bg-2);
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}

main.layout {
  display: flex;
  flex: 1;
  min-height: 0;
}

aside.sidebar {
  flex: 0 0 240px;
  background: var(--bg-2);
  border-right: 1px solid var(--border);
  overflow-y: auto;
  padding: 8px 0;
}

aside.sidebar h3 {
  font-size: 11px;
  text-transform: uppercase;
  color: var(--fg-dim);
  letter-spacing: 0.5px;
  padding: 12px 16px 4px;
  margin: 0;
}

aside.sidebar a, aside.sidebar button {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 16px;
  color: var(--fg);
  text-decoration: none;
  background: transparent;
  border: none;
  width: 100%;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

aside.sidebar a:hover, aside.sidebar button:hover { background: var(--accent-soft); }
aside.sidebar a.active { background: var(--accent-soft); border-left: 2px solid var(--accent); padding-left: 14px; }

.tier-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--accent-soft);
  color: var(--accent);
  margin-left: 4px;
}

section.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

.thread-header {
  padding: 8px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 8px;
}

.thread-body {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.bubble {
  max-width: 70%;
  padding: 8px 12px;
  border-radius: 12px;
  background: var(--bg-2);
  border: 1px solid var(--border);
  word-wrap: break-word;
}

.bubble.self { align-self: flex-end; background: var(--accent-soft); border-color: var(--accent); }
.bubble.malformed { border-color: var(--warn); background: var(--warn-soft); }

.bubble .meta { font-size: 11px; color: var(--fg-dim); margin-bottom: 4px; }
.bubble .reject-chip {
  display: inline-block;
  font-size: 10px;
  padding: 0 6px;
  border-radius: 3px;
  background: var(--warn);
  color: var(--bg);
  margin-left: 4px;
}

.compose {
  border-top: 1px solid var(--border);
  padding: 12px 16px;
  display: flex;
  gap: 8px;
}

.compose input[type=text] {
  flex: 1;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg);
  color: var(--fg);
  font: inherit;
}

.compose button {
  padding: 8px 16px;
  background: var(--accent);
  color: var(--bg);
  border: none;
  border-radius: var(--radius);
  cursor: pointer;
  font: inherit;
  font-weight: 600;
}

.compose .error { color: var(--warn); font-size: 12px; }

table.messages {
  width: 100%;
  border-collapse: collapse;
}
table.messages th, table.messages td {
  padding: 8px 16px;
  text-align: left;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
table.messages th { color: var(--fg-dim); font-weight: 500; font-size: 11px; text-transform: uppercase; }
table.messages tr.malformed td { color: var(--warn); }
table.messages tr:hover { background: var(--accent-soft); cursor: pointer; }

.empty { padding: 32px; text-align: center; color: var(--fg-dim); }
```

- [ ] **Step 4: Commit static assets**

```bash
git add internal/dashboard/static/
git commit -m "$(cat <<'EOF'
feat(dashboard): vendor htmx + base CSS

Vendor htmx 2.0.4 and htmx-ext-sse 2.2.2 (npm-free; checked in
verbatim with audit headers). Base CSS provides dark-mode-default
palette with prefers-color-scheme light fallback and the layout
primitives (sidebar / thread / bubble / messages table). Real visual
polish lands in a later task under the frontend-design skill.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Templates and the render package

**Files:**
- Create: `internal/dashboard/render.go`
- Create: `internal/dashboard/render_test.go`
- Create: `internal/dashboard/templates/shell.html`
- Create: `internal/dashboard/templates/sidebar.html`
- Create: `internal/dashboard/templates/thread.html`
- Create: `internal/dashboard/templates/bubble.html`
- Create: `internal/dashboard/templates/messages.html`
- Create: `internal/dashboard/templates/compose.html`

- [ ] **Step 1: Write the failing test for `Render`**

Create `internal/dashboard/render_test.go`:

```go
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

func TestRender_Shell_ContainsOwnNpub(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("shell", shellData{OwnLabel: "alice", OwnNpub: "npub1self...0000"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "npub1self") {
		t.Errorf("shell missing own npub: %s", out)
	}
}

func TestRender_Bubble_FromContact(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{
		Self: false,
		From: "Bob",
		Text: "hello",
		At:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("bubble missing text: %s", out)
	}
	if strings.Contains(out, `class="bubble self"`) {
		t.Errorf("non-self bubble should not have .self class: %s", out)
	}
}

func TestRender_Bubble_Malformed(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{
		Self:         false,
		From:         "Carol",
		Text:         "garbage content",
		At:           time.Now(),
		Malformed:    true,
		RejectReason: "not_envelope",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "malformed") {
		t.Errorf("malformed bubble missing class/marker: %s", out)
	}
	if !strings.Contains(out, "not_envelope") {
		t.Errorf("malformed bubble missing reason: %s", out)
	}
}

func TestRender_Sidebar_ContactSorting(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	out, err := r.Render("sidebar", sidebarData{
		Contacts: []sidebarContact{
			{Pubkey: "p1", Label: "Alice", Tier: contacts.TierFriend, LastSeen: &now},
			{Pubkey: "p2", Label: "Bob", Tier: contacts.TierMaster, LastSeen: nil},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "Bob") {
		t.Errorf("sidebar missing contacts: %s", out)
	}
}

func TestRender_MessagesTable_MalformedRowMarked(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("messages", messagesData{
		Rows: []messageRow{
			{Direction: "in", From: "Bob", Preview: "hello", At: time.Now()},
			{Direction: "in", From: "Carol", Preview: "raw", At: time.Now(), Malformed: true, RejectReason: "not_envelope"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "malformed") {
		t.Errorf("messages row missing malformed class: %s", out)
	}
}

// Suppress unused-import warnings if any helper imports become unused later.
var _ = inbox.Message{}
```

- [ ] **Step 2: Run; expect compile failure**

Run: `go test ./internal/dashboard/... -run TestRender -v`
Expected: build error — `newRenderer`, `shellData`, etc. undefined.

- [ ] **Step 3: Create `render.go` with the renderer + the data types**

Create `internal/dashboard/render.go`:

```go
package dashboard

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"sort"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

//go:embed templates/*.html templates/partials/*.html
var templatesFS embed.FS

type renderer struct {
	t *template.Template
}

func newRenderer() (*renderer, error) {
	funcs := template.FuncMap{
		"reltime": func(t time.Time) string { return relativeTime(t, time.Now()) },
		"truncate": func(n int, s string) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
	}
	t, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &renderer{t: t}, nil
}

func (r *renderer) Render(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := r.t.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// relativeTime renders a duration like "14m ago", "2h ago", "1d ago".
func relativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// View-data structs. These are the only contract with the templates;
// renames here require renames in the .html files.

type shellData struct {
	OwnLabel string
	OwnNpub  string
	Sidebar  sidebarData
	Main     template.HTML // pre-rendered main pane fragment
}

type sidebarData struct {
	Contacts []sidebarContact
}

type sidebarContact struct {
	Pubkey   string
	Label    string
	Tier     contacts.Tier
	LastSeen *time.Time
	Active   bool
}

type threadData struct {
	Counterpart sidebarContact
	Bubbles     []bubbleData
	OwnPubkey   string
}

type bubbleData struct {
	Self         bool
	From         string
	Text         string
	At           time.Time
	Malformed    bool
	RejectReason string
	EventID      string // for SSE swap targets
}

type messagesData struct {
	Rows       []messageRow
	NextCursor string
	OnlyMal    bool // current view is /messages?malformed=1
}

type messageRow struct {
	Direction    string // "in" or "out"
	From         string // counterpart label (or pubkey-prefix when no contact)
	Preview      string
	At           time.Time
	Malformed    bool
	RejectReason string
	EventID      string
	Pubkey       string // counterpart pubkey for thread navigation
}

type composeData struct {
	Pubkey string // pre-fill if specified (compose-to-npub flow)
	Error  string
}

// Sort contacts as the spec requires: most-recent-message desc, then by tier
// rank (master > friend > acquaintance > blocked), then by label.
func sortContactsForSidebar(in []sidebarContact) []sidebarContact {
	out := make([]sidebarContact, len(in))
	copy(out, in)
	tierRank := map[contacts.Tier]int{
		contacts.TierMaster:       0,
		contacts.TierFriend:       1,
		contacts.TierAcquaintance: 2,
		contacts.TierBlocked:      3,
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].LastSeen, out[j].LastSeen
		switch {
		case ai != nil && aj == nil:
			return true
		case ai == nil && aj != nil:
			return false
		case ai != nil && aj != nil && !ai.Equal(*aj):
			return ai.After(*aj)
		}
		if r := tierRank[out[i].Tier] - tierRank[out[j].Tier]; r != 0 {
			return r < 0
		}
		return out[i].Label < out[j].Label
	})
	return out
}
```

- [ ] **Step 4: Create the templates**

Each template name must match the `Render(name, ...)` first argument. Templates use `{{define "name"}}...{{end}}` blocks so multiple definitions can coexist in one file IF needed; we'll use one file per top-level definition for clarity.

`internal/dashboard/templates/shell.html`:

```html
{{define "shell"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=1024">
<title>eidos-gate · {{.OwnLabel}}</title>
<link rel="stylesheet" href="/static/app.css">
<script src="/static/htmx.min.js"></script>
<script src="/static/htmx-ext-sse.js"></script>
</head>
<body hx-ext="sse" sse-connect="/events">
<header class="topbar">
  <strong>eidos-gate</strong>
  <span class="own-id" title="{{.OwnNpub}}">{{.OwnLabel}} · {{truncate 16 .OwnNpub}}</span>
</header>
<main class="layout">
  <aside class="sidebar" id="sidebar"
         sse-swap="contact.added,contact.removed"
         hx-get="/sidebar/contacts"
         hx-trigger="sse:contact.added,sse:contact.removed">
    {{template "sidebar-inner" .Sidebar}}
  </aside>
  <section class="main" id="main">
    {{.Main}}
  </section>
</main>
</body>
</html>{{end}}
```

`internal/dashboard/templates/sidebar.html`:

```html
{{define "sidebar"}}{{template "sidebar-inner" .}}{{end}}

{{define "sidebar-inner"}}
<h3>Inbox</h3>
<a href="/messages" hx-get="/messages" hx-target="#main" hx-push-url="true">All messages</a>
<a href="/messages?malformed=1" hx-get="/messages?malformed=1" hx-target="#main" hx-push-url="true">Soft-rejected</a>

<h3>Contacts</h3>
{{range .Contacts}}
<a href="/thread/{{.Pubkey}}"
   hx-get="/thread/{{.Pubkey}}"
   hx-target="#main"
   hx-push-url="true"
   class="{{if .Active}}active{{end}}">
  <span>{{.Label}} <span class="tier-badge">{{.Tier}}</span></span>
  {{if .LastSeen}}<span class="dim">{{reltime .LastSeen}}</span>{{end}}
</a>
{{else}}
<div class="empty" style="padding:16px">No contacts yet.</div>
{{end}}

<h3>Compose</h3>
<button hx-get="/compose" hx-target="#main">+ Compose to npub…</button>
{{end}}
```

`internal/dashboard/templates/thread.html`:

```html
{{define "thread"}}
<div class="thread-header">
  <strong>{{.Counterpart.Label}}</strong>
  <span class="tier-badge">{{.Counterpart.Tier}}</span>
</div>
<div class="thread-body" id="thread-body" sse-swap="inbox.message,outbox.message" hx-swap="beforeend">
  {{range .Bubbles}}
    {{template "bubble" .}}
  {{else}}
    <div class="empty">No messages yet — say hi.</div>
  {{end}}
</div>
<form class="compose"
      hx-post="/thread/{{.Counterpart.Pubkey}}/send"
      hx-target="#thread-body"
      hx-swap="beforeend"
      hx-on::after-request="this.reset()">
  <input type="text" name="text" placeholder="Reply to {{.Counterpart.Label}}…" required autofocus>
  <button type="submit">Send</button>
</form>
{{end}}
```

`internal/dashboard/templates/bubble.html`:

```html
{{define "bubble"}}
<div class="bubble{{if .Self}} self{{end}}{{if .Malformed}} malformed{{end}}" data-event-id="{{.EventID}}">
  <div class="meta">
    {{.From}} · <span title="{{.At.Format "2006-01-02 15:04:05"}}">{{reltime .At}}</span>
    {{if .Malformed}}<span class="reject-chip">{{.RejectReason}}</span>{{end}}
  </div>
  <div>{{.Text}}</div>
</div>
{{end}}
```

`internal/dashboard/templates/messages.html`:

```html
{{define "messages"}}
<div class="thread-header">
  <strong>{{if .OnlyMal}}Soft-rejected{{else}}All messages{{end}}</strong>
</div>
<table class="messages">
  <thead>
    <tr>
      <th>Time</th>
      <th>Dir</th>
      <th>Counterpart</th>
      <th>Preview</th>
      {{if .OnlyMal}}<th>Reason</th>{{end}}
    </tr>
  </thead>
  <tbody>
    {{range .Rows}}
    <tr class="{{if .Malformed}}malformed{{end}}"
        hx-get="/thread/{{.Pubkey}}#evt-{{.EventID}}"
        hx-target="#main"
        hx-push-url="true">
      <td><span title="{{.At.Format "2006-01-02 15:04:05"}}">{{reltime .At}}</span></td>
      <td>{{.Direction}}</td>
      <td>{{.From}}</td>
      <td>{{truncate 80 .Preview}}</td>
      {{if $.OnlyMal}}<td>{{.RejectReason}}</td>{{end}}
    </tr>
    {{else}}
    <tr><td colspan="5" class="empty">No messages.</td></tr>
    {{end}}
  </tbody>
</table>
{{if .NextCursor}}
<div style="padding:16px;text-align:center">
  <button hx-get="/messages?cursor={{.NextCursor}}{{if .OnlyMal}}&malformed=1{{end}}"
          hx-target="closest div" hx-swap="outerHTML">Load more</button>
</div>
{{end}}
{{end}}
```

`internal/dashboard/templates/compose.html`:

```html
{{define "compose"}}
<div class="thread-header"><strong>Compose to a new pubkey</strong></div>
<form class="compose" style="flex-direction:column;align-items:stretch"
      hx-post="/thread/PUBKEY_PLACEHOLDER/send"
      hx-vals='js:{"to_override": document.getElementById("compose-to").value}'
      hx-target="#main">
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <input id="compose-to" type="text" name="to" placeholder="npub1… or hex" value="{{.Pubkey}}" required style="margin-bottom:8px">
  <input type="text" name="text" placeholder="Message…" required>
  <button type="submit" style="margin-top:8px;align-self:flex-end">Send</button>
</form>
{{end}}
```

Note on the compose form: PUBKEY_PLACEHOLDER plus an `hx-vals` JS expression overrides the route to use the user-typed `to`. We'll handle this server-side in Task 7's send handler by reading `to_override` from the form when present and routing accordingly. (Spec §3.1 prescribed a small modal; this inline pane is the v1 simplification.)

- [ ] **Step 5: Run; expect tests pass**

Run: `go test ./internal/dashboard/... -run TestRender -v`
Expected: all PASS.

- [ ] **Step 6: Vet + gofmt**

Run: `gofmt -l internal/dashboard/ && go vet ./internal/dashboard/...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/dashboard/render.go internal/dashboard/render_test.go internal/dashboard/templates/
git commit -m "$(cat <<'EOF'
feat(dashboard): templates and render package

Six top-level templates (shell, sidebar, thread, bubble, messages,
compose) with associated view-data structs. The renderer parses them
once at construction via go:embed and exposes Render(name, data).
Includes funcs reltime and truncate for inline use, and
sortContactsForSidebar for the sidebar ordering rule (most-recent
first, then tier rank, then label).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Read handlers — shell, sidebar, thread, messages

**Files:**
- Create: `internal/dashboard/handlers.go`
- Create: `internal/dashboard/handlers_test.go`
- Create: `internal/dashboard/pagination.go`
- Modify: `internal/dashboard/server.go` (replace stub `registerHandlers`)

- [ ] **Step 1: Write failing handler tests**

Create `internal/dashboard/handlers_test.go`:

```go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

func newTestServer(t *testing.T, deps DashboardDeps) http.Handler {
	t.Helper()
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerHandlersWithRenderer(mux, deps, r, slogDiscard())
	return sameOriginGuard(mux)
}

func TestHandler_Shell_OK(t *testing.T) {
	deps := fakeDeps{pubkey: "abc123", label: "alice"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice") {
		t.Errorf("shell missing label: %s", body)
	}
}

func TestHandler_Sidebar_RendersContacts(t *testing.T) {
	deps := fakeDeps{
		contactsL: []*contacts.Contact{
			{Pubkey: "p1", Label: "Bob", Tier: contacts.TierMaster},
			{Pubkey: "p2", Label: "Alice", Tier: contacts.TierFriend},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sidebar/contacts", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Bob") || !strings.Contains(body, "Alice") {
		t.Errorf("sidebar missing contacts: %s", body)
	}
}

func TestHandler_Thread_FindsContactAndRendersBubbles(t *testing.T) {
	from := "0123456789abcdef"
	deps := fakeDeps{
		pubkey: "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: from, Label: "Bob", Tier: contacts.TierFriend}},
		inbox: []inbox.Message{
			{From: from, Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hello"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/thread/"+from, nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("thread missing message text: %s", rec.Body.String())
	}
}

func TestHandler_Messages_AllShowsRows(t *testing.T) {
	deps := fakeDeps{
		inbox: []inbox.Message{
			{From: "p1", Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ping"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ping") {
		t.Errorf("messages missing row text: %s", rec.Body.String())
	}
}

func TestHandler_Messages_MalformedFilter(t *testing.T) {
	deps := fakeDeps{
		inbox: []inbox.Message{
			{From: "p1", Content: "garbage", Malformed: true, RejectReason: "not_envelope", ReceivedAt: time.Now().Unix()},
			{From: "p2", Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ok"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages?malformed=1", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "not_envelope") {
		t.Errorf("malformed view missing reason: %s", body)
	}
	if strings.Contains(body, ">ok<") {
		t.Errorf("malformed view should not contain non-malformed row text")
	}
}

func mustEncode(t *testing.T, e envelope.Envelope) string {
	t.Helper()
	s, err := envelope.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func slogDiscard() *slog.Logger { return slog.New(slog.NewTextHandler(stringWriter{&strings.Builder{}}, nil)) }
```

You'll need `import ( "log/slog" )` at the top of the test file.

- [ ] **Step 2: Run; expect compile failure**

Run: `go test ./internal/dashboard/... -run TestHandler -v`
Expected: build error.

- [ ] **Step 3: Create `pagination.go`**

```go
package dashboard

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// encodeCursor packs a unix-second timestamp into a URL-safe cursor.
func encodeCursor(unixSec int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(unixSec, 10)))
}

// decodeCursor returns 0 for the empty string and on any decode error
// (treats malformed cursor as "start from latest"; the caller then sees
// an empty page if there are no rows).
func decodeCursor(s string) int64 {
	if s == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
```

- [ ] **Step 4: Create `handlers.go`**

```go
package dashboard

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

const messagesPageSize = 50

func registerHandlersWithRenderer(mux *http.ServeMux, deps DashboardDeps, r *renderer, logger *slog.Logger) {
	mux.HandleFunc("/", shellHandler(deps, r, logger))
	mux.HandleFunc("/sidebar/contacts", sidebarHandler(deps, r, logger))
	mux.HandleFunc("/messages", messagesHandler(deps, r, logger))
	mux.HandleFunc("/thread/", threadHandler(deps, r, logger))
	mux.HandleFunc("/compose", composeHandler(deps, r, logger))
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler()))
	// /events and /thread/{pk}/send are added in later tasks.
}

func staticHandler() http.Handler {
	sub, _ := embedSubFS(staticFS, "static")
	return http.FileServer(http.FS(sub))
}

// shellHandler renders the full page, default main pane = "All messages".
func shellHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		ctx := req.Context()
		label, _ := deps.OwnLabel(ctx)
		side := buildSidebar(ctx, deps, "")
		main := buildMessagesView(deps, false, "")

		mainHTML, err := r.Render("messages", main)
		if err != nil {
			logger.Error("render messages", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		out, err := r.Render("shell", shellData{
			OwnLabel: label,
			OwnNpub:  deps.OwnPubkey(),
			Sidebar:  side,
			Main:     templateHTML(mainHTML),
		})
		if err != nil {
			logger.Error("render shell", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func sidebarHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		side := buildSidebar(ctx, deps, "")
		out, err := r.Render("sidebar", side)
		if err != nil {
			logger.Error("render sidebar", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func messagesHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		onlyMal := req.URL.Query().Get("malformed") == "1"
		cursor := req.URL.Query().Get("cursor")
		view := buildMessagesView(deps, onlyMal, cursor)
		out, err := r.Render("messages", view)
		if err != nil {
			logger.Error("render messages", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func threadHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// /thread/{pubkey}            → render thread (this file)
		// /thread/{pubkey}/send       → POST send (Task 7 will register this)
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pk := strings.TrimPrefix(req.URL.Path, "/thread/")
		if i := strings.Index(pk, "/"); i >= 0 {
			pk = pk[:i]
		}
		if pk == "" {
			http.NotFound(w, req)
			return
		}
		ctx := req.Context()

		c := lookupContact(ctx, deps, pk)
		var counterpart sidebarContact
		if c != nil {
			counterpart = sidebarContact{Pubkey: c.Pubkey, Label: c.Label, Tier: c.Tier}
		} else {
			counterpart = sidebarContact{Pubkey: pk, Label: shortenPubkey(pk), Tier: contacts.TierAcquaintance}
		}
		bubbles := buildBubbles(deps, pk)
		out, err := r.Render("thread", threadData{
			Counterpart: counterpart,
			Bubbles:     bubbles,
			OwnPubkey:   deps.OwnPubkey(),
		})
		if err != nil {
			logger.Error("render thread", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func composeHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out, err := r.Render("compose", composeData{})
		if err != nil {
			logger.Error("render compose", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// --- view builders ---

func buildSidebar(ctx context.Context, deps DashboardDeps, activePubkey string) sidebarData {
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		return sidebarData{}
	}
	out := make([]sidebarContact, 0, len(cs))
	// Last-message time per contact comes from inbox + outbox; do a quick
	// sweep so the sidebar can sort by most-recent.
	lastSeen := lastSeenByContact(deps)
	for _, c := range cs {
		if c.Tier == contacts.TierBlocked {
			continue
		}
		var ls *time.Time
		if t, ok := lastSeen[c.Pubkey]; ok {
			ls = &t
		}
		out = append(out, sidebarContact{
			Pubkey:   c.Pubkey,
			Label:    c.Label,
			Tier:     c.Tier,
			LastSeen: ls,
			Active:   c.Pubkey == activePubkey,
		})
	}
	return sidebarData{Contacts: sortContactsForSidebar(out)}
}

// lastSeenByContact returns the most-recent message time per pubkey across
// inbox and outbox. Caller-tolerant: returns empty map on error.
func lastSeenByContact(deps DashboardDeps) map[string]time.Time {
	out := map[string]time.Time{}
	if msgs, err := deps.ListInbox(nil, "", 200); err == nil {
		for _, m := range msgs {
			t := time.Unix(m.ReceivedAt, 0)
			if cur, ok := out[m.From]; !ok || t.After(cur) {
				out[m.From] = t
			}
		}
	}
	if sents, err := deps.ListOutbox(nil, "", 200); err == nil {
		for _, s := range sents {
			t := time.Unix(s.SentAt, 0)
			if cur, ok := out[s.To]; !ok || t.After(cur) {
				out[s.To] = t
			}
		}
	}
	return out
}

func buildMessagesView(deps DashboardDeps, onlyMal bool, cursor string) messagesData {
	var since *time.Time
	if c := decodeCursor(cursor); c > 0 {
		t := time.Unix(c, 0)
		since = &t
	}
	rows := []messageRow{}

	if !onlyMal {
		if sents, err := deps.ListOutbox(since, "", messagesPageSize); err == nil {
			for _, s := range sents {
				rows = append(rows, sentToRow(s))
			}
		}
	}
	if msgs, err := deps.ListInbox(since, "", messagesPageSize); err == nil {
		for _, m := range msgs {
			if onlyMal && !m.Malformed {
				continue
			}
			rows = append(rows, msgToRow(m))
		}
	}
	// Sort newest-first by At.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].At.After(rows[j-1].At); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	if len(rows) > messagesPageSize {
		rows = rows[:messagesPageSize]
	}
	view := messagesData{Rows: rows, OnlyMal: onlyMal}
	if len(rows) == messagesPageSize {
		view.NextCursor = encodeCursor(rows[len(rows)-1].At.Unix() - 1)
	}
	return view
}

func msgToRow(m inbox.Message) messageRow {
	preview := previewFor(m.Content)
	return messageRow{
		Direction:    "in",
		From:         shortenPubkey(m.From),
		Pubkey:       m.From,
		Preview:      preview,
		At:           time.Unix(m.ReceivedAt, 0),
		Malformed:    m.Malformed,
		RejectReason: m.RejectReason,
		EventID:      m.EventID,
	}
}

func sentToRow(s inbox.Sent) messageRow {
	return messageRow{
		Direction: "out",
		From:      shortenPubkey(s.To),
		Pubkey:    s.To,
		Preview:   previewFor(s.Content),
		At:        time.Unix(s.SentAt, 0),
		EventID:   s.EventID,
	}
}

func previewFor(content string) string {
	env, err := envelope.Decode(content)
	if err != nil {
		// Legacy plain-text or malformed — return as-is, truncation handled in template.
		return content
	}
	return env.Text
}

func buildBubbles(deps DashboardDeps, pk string) []bubbleData {
	bubbles := []bubbleData{}
	if msgs, err := deps.ListInbox(nil, pk, 100); err == nil {
		for _, m := range msgs {
			bubbles = append(bubbles, msgToBubble(m, false))
		}
	}
	if sents, err := deps.ListOutbox(nil, pk, 100); err == nil {
		for _, s := range sents {
			bubbles = append(bubbles, sentToBubble(s, deps.OwnPubkey()))
		}
	}
	// Oldest-first for thread display.
	for i := 1; i < len(bubbles); i++ {
		for j := i; j > 0 && bubbles[j].At.Before(bubbles[j-1].At); j-- {
			bubbles[j], bubbles[j-1] = bubbles[j-1], bubbles[j]
		}
	}
	return bubbles
}

func msgToBubble(m inbox.Message, _ bool) bubbleData {
	text := m.Content
	if env, err := envelope.Decode(m.Content); err == nil {
		text = env.Text
	}
	return bubbleData{
		Self:         false,
		From:         shortenPubkey(m.From),
		Text:         text,
		At:           time.Unix(m.ReceivedAt, 0),
		Malformed:    m.Malformed,
		RejectReason: m.RejectReason,
		EventID:      m.EventID,
	}
}

func sentToBubble(s inbox.Sent, ownPubkey string) bubbleData {
	text := s.Content
	if env, err := envelope.Decode(s.Content); err == nil {
		text = env.Text
	}
	return bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Unix(s.SentAt, 0),
		EventID: s.EventID,
	}
}

func lookupContact(ctx context.Context, deps DashboardDeps, pk string) *contacts.Contact {
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		return nil
	}
	for _, c := range cs {
		if c.Pubkey == pk {
			return c
		}
	}
	return nil
}

func shortenPubkey(pk string) string {
	if len(pk) <= 16 {
		return pk
	}
	return pk[:8] + "…" + pk[len(pk)-4:]
}
```

- [ ] **Step 5: Add helpers `embedSubFS`, `templateHTML`, plus the `staticFS` embed**

Append to `internal/dashboard/render.go`:

```go
//go:embed static
var staticFS embed.FS

func embedSubFS(fs embed.FS, dir string) (subFS, error) {
	return subFS{base: fs, prefix: dir + "/"}, nil
}

type subFS struct {
	base   embed.FS
	prefix string
}

func (s subFS) Open(name string) (interface{ Read([]byte) (int, error); Close() error }, error) {
	f, err := s.base.Open(s.prefix + name)
	if err != nil {
		return nil, err
	}
	return f.(interface {
		Read([]byte) (int, error)
		Close() error
	}), nil
}

func templateHTML(s string) template.HTML { return template.HTML(s) } //nolint:gosec // we render only trusted templates
```

Important: `subFS.Open` must return `fs.File`, not the ad-hoc interface above. The simpler implementation uses `fs.Sub`:

Actually replace the subFS plumbing with the standard library:

```go
func embedSubFS(efs embed.FS, dir string) (fs.FS, error) {
	return fs.Sub(efs, dir)
}
```

And add `import "io/fs"` at the top of `render.go`. Adjust `staticHandler()` in `handlers.go`:

```go
func staticHandler() http.Handler {
	sub, _ := embedSubFS(staticFS, "static")
	return http.FileServer(http.FS(sub))
}
```

- [ ] **Step 6: Update server.go to use the renderer-aware register**

In `internal/dashboard/server.go`, replace the placeholder `registerHandlers` with one that wires up `newRenderer`:

```go
func registerHandlers(mux *http.ServeMux, deps DashboardDeps, logger *slog.Logger) {
	r, err := newRenderer()
	if err != nil {
		logger.Error("dashboard renderer init failed", "err", err)
		mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "dashboard renderer unavailable", 500)
		})
		return
	}
	registerHandlersWithRenderer(mux, deps, r, logger)
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/dashboard/... -v`
Expected: all PASS.

- [ ] **Step 8: Vet + gofmt**

Run: `gofmt -l internal/dashboard/ && go vet ./internal/dashboard/...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add internal/dashboard/handlers.go internal/dashboard/handlers_test.go internal/dashboard/pagination.go internal/dashboard/render.go internal/dashboard/server.go
git commit -m "$(cat <<'EOF'
feat(dashboard): read handlers (shell / sidebar / thread / messages)

Five HTTP routes return HTML fragments (or the full shell on /):
- GET /                         shell + initial messages pane
- GET /sidebar/contacts         sidebar fragment
- GET /thread/{pubkey}          chat thread for a contact or unknown pubkey
- GET /messages?malformed=1     all-messages or soft-rejected list, paginated
- GET /static/{path}            embedded static assets via fs.Sub

Cursor-paginated /messages decodes a base64 unix-second since-cursor.
Bubbles are built oldest-first and decode envelope.Text for display;
malformed rows render with the .malformed class and a reject chip.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Send handler

**Files:**
- Modify: `internal/dashboard/handlers.go`
- Modify: `internal/dashboard/handlers_test.go`

- [ ] **Step 1: Write failing tests for POST send**

Append to `internal/dashboard/handlers_test.go`:

```go
func TestHandler_Send_ToContact_OK(t *testing.T) {
	deps := fakeDeps{
		pubkey: "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: "abcdef", Label: "Bob", Tier: contacts.TierFriend}},
		sendID: "evt-1",
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=hello+bob")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello bob") {
		t.Errorf("response missing bubble text: %s", rec.Body.String())
	}
}

func TestHandler_Send_RelayFailure_Returns502(t *testing.T) {
	deps := fakeDeps{
		pubkey: "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: "abcdef", Label: "Bob", Tier: contacts.TierFriend}},
		sendErr: errStubFailure,
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=oops")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Send_EmptyText_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

var errStubFailure = errors.New("stub: no relay accepted")
```

Add these imports to the test file: `"errors"`, `"strings"` (likely already imported).

Update `fakeDeps.Send` to return its `sendErr` (already does in Task 2 stub; verify).

- [ ] **Step 2: Run; expect compile/run failure**

Run: `go test ./internal/dashboard/... -run TestHandler_Send -v`
Expected: build error or test failure (no /thread/{pk}/send route registered).

- [ ] **Step 3: Add the send handler**

Append to `internal/dashboard/handlers.go`:

```go
import "errors" // add to existing import block

// In registerHandlersWithRenderer, add a wrapping mux for the thread paths:
//   mux.HandleFunc("/thread/", threadOrSendHandler(...))
// Replace the existing `mux.HandleFunc("/thread/", threadHandler(...))` line.

func threadOrSendHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	getH := threadHandler(deps, r, logger)
	postH := sendHandler(deps, r, logger)
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/send") {
			postH(w, req)
			return
		}
		getH(w, req)
	}
}

func sendHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// path: /thread/{pubkey}/send
		path := strings.TrimPrefix(req.URL.Path, "/thread/")
		path = strings.TrimSuffix(path, "/send")
		pk := path
		if pk == "" {
			http.NotFound(w, req)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		text := strings.TrimSpace(req.Form.Get("text"))
		if text == "" {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}
		// Honour to_override from compose-to-npub flow.
		if v := req.Form.Get("to_override"); v != "" {
			pk = v
		}

		env := envelope.Envelope{
			V:      envelope.SchemaVersion,
			Type:   envelope.TypeChat,
			Text:   text,
			Client: &envelope.Client{Name: "eidos-dashboard", Ver: "0.1.0"},
		}
		eventID, err := deps.Send(req.Context(), pk, env)
		if err != nil {
			logger.Warn("dashboard send failed", "err", err, "to", pk)
			http.Error(w, errSendFragment(r, err.Error()), http.StatusBadGateway)
			return
		}
		// Render the bubble that just got sent so htmx beforeend-swaps it
		// into the thread body.
		out, rerr := r.Render("bubble", bubbleData{
			Self:    true,
			From:    "you",
			Text:    text,
			At:      time.Now(),
			EventID: eventID,
		})
		if rerr != nil {
			logger.Error("render bubble", "err", rerr)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func errSendFragment(_ *renderer, msg string) string {
	// Inline the error fragment directly to avoid a render dependency in
	// the error path.
	return `<div class="error">send failed: ` + template.HTMLEscapeString(msg) + `</div>`
}
```

Add `"html/template"` to imports if not already there.

In `registerHandlersWithRenderer`, replace the `/thread/` line:

```go
	mux.HandleFunc("/thread/", threadOrSendHandler(deps, r, logger))
```

Drop the old `threadHandler` registration.

- [ ] **Step 4: Run send tests**

Run: `go test ./internal/dashboard/... -run TestHandler_Send -v`
Expected: PASS.

- [ ] **Step 5: Run all dashboard tests**

Run: `go test ./internal/dashboard/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/handlers.go internal/dashboard/handlers_test.go
git commit -m "$(cat <<'EOF'
feat(dashboard): POST /thread/{pubkey}/send

Wraps form text into a v1 chat envelope and calls deps.Send (which on
*Daemon publishes via existing nostr.Wrap + Pool.Publish path). On
success returns a self-bubble fragment for htmx beforeend-swap into
the thread body. On send failure returns 502 with an inline error
fragment that the compose pane swaps into its error slot. Empty text
is rejected with 400 before any IO.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: SSE endpoint

**Files:**
- Create: `internal/dashboard/sse.go`
- Create: `internal/dashboard/sse_test.go`
- Modify: `internal/dashboard/handlers.go` (register `/events`)

- [ ] **Step 1: Write failing test**

Create `internal/dashboard/sse_test.go`:

```go
package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

func TestSSE_DeliversInboxMessage(t *testing.T) {
	hub := newSSEHub(testDepsWithChan(t))
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(hub.handler(r, slogDiscard())))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("content-type: %s", resp.Header.Get("Content-Type"))
	}

	// Push an event into the underlying fake.
	hub.deps.(*fakeDepsWithChan).push(Event{
		Kind: "inbox.message",
		Message: &inbox.Message{From: "p1", Content: `{"v":1,"type":"chat","text":"hi"}`, ReceivedAt: time.Now().Unix()},
	})

	// Read until we see "event: inbox.message" or timeout.
	buf := make([]byte, 4096)
	deadline := time.Now().Add(1 * time.Second)
	var seen string
	for time.Now().Before(deadline) {
		n, _ := resp.Body.Read(buf)
		seen += string(buf[:n])
		if strings.Contains(seen, "event: inbox.message") {
			return
		}
		if n == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("did not observe SSE event in stream: %q", seen)
}

// fakeDepsWithChan exposes a channel under a stable cancel; tests push
// events through it.
type fakeDepsWithChan struct {
	fakeDeps
	ch chan Event
}

func (f *fakeDepsWithChan) SubscribeEvents() (<-chan Event, func()) {
	f.ch = make(chan Event, 4)
	return f.ch, func() { close(f.ch) }
}

func (f *fakeDepsWithChan) push(e Event) { f.ch <- e }

func testDepsWithChan(t *testing.T) DashboardDeps { return &fakeDepsWithChan{} }
```

- [ ] **Step 2: Run; expect compile failure**

Run: `go test ./internal/dashboard/... -run TestSSE -v`
Expected: build error.

- [ ] **Step 3: Implement SSE hub + handler**

Create `internal/dashboard/sse.go`:

```go
package dashboard

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// sseHub wires DashboardDeps.SubscribeEvents to an HTTP SSE handler.
type sseHub struct {
	deps DashboardDeps
}

func newSSEHub(deps DashboardDeps) *sseHub { return &sseHub{deps: deps} }

func (h *sseHub) handler(r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		ch, cancel := h.deps.SubscribeEvents()
		defer cancel()

		// Periodic comment keeps connections alive across proxies and lets
		// the client detect disconnects quickly.
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()

		ctx := req.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				_, _ = fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case ev, ok := <-ch:
				if !ok {
					return
				}
				html := renderEvent(r, ev, logger)
				if html == "" {
					continue
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, escapeSSEData(html))
				flusher.Flush()
			}
		}
	}
}

// renderEvent maps an Event to the HTML fragment payload htmx will swap.
// Returns "" if the event has no UI projection (e.g. a kind we don't
// render).
func renderEvent(r *renderer, ev Event, logger *slog.Logger) string {
	switch ev.Kind {
	case "inbox.message":
		if ev.Message == nil {
			return ""
		}
		out, err := r.Render("bubble", msgToBubble(*ev.Message, false))
		if err != nil {
			logger.Warn("render inbox bubble for SSE", "err", err)
			return ""
		}
		return out
	case "outbox.message":
		if ev.Sent == nil {
			return ""
		}
		out, err := r.Render("bubble", sentToBubble(*ev.Sent, ""))
		if err != nil {
			logger.Warn("render outbox bubble for SSE", "err", err)
			return ""
		}
		return out
	case "contact.added", "contact.removed", "contact.relabeled":
		// No fragment payload — the client uses the SSE event name to
		// trigger a sidebar refresh via hx-trigger.
		return "(refresh)"
	default:
		return ""
	}
}

// escapeSSEData replaces newlines in the payload so it fits on one
// `data: ` line. SSE permits multi-line data via repeated `data:`
// prefixes, but htmx-ext-sse handles single-line data more robustly.
func escapeSSEData(s string) string {
	out := make([]byte, 0, len(s))
	for _, ch := range []byte(s) {
		switch ch {
		case '\r':
			// drop
		case '\n':
			out = append(out, ' ')
		default:
			out = append(out, ch)
		}
	}
	return string(out)
}
```

In `handlers.go`, register the route inside `registerHandlersWithRenderer`:

```go
	hub := newSSEHub(deps)
	mux.HandleFunc("/events", hub.handler(r, logger))
```

- [ ] **Step 4: Run SSE tests**

Run: `go test ./internal/dashboard/... -run TestSSE -v`
Expected: PASS.

- [ ] **Step 5: Run full dashboard suite**

Run: `go test ./internal/dashboard/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/sse.go internal/dashboard/sse_test.go internal/dashboard/handlers.go
git commit -m "$(cat <<'EOF'
feat(dashboard): SSE /events endpoint

Subscribes to the daemon's dashboard event hub and emits named SSE
events with HTML-fragment payloads (rendered via the bubble template).
Periodic comment-pings keep connections alive across proxies. inbox.message
and outbox.message render bubble fragments; contact.* events trigger a
sidebar refresh via hx-trigger.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Daemon launches the dashboard

**Files:**
- Modify: `internal/daemon/daemon.go`

- [ ] **Step 1: Wire `dashboard.Run` into `Daemon.Run`**

In `internal/daemon/daemon.go`, after `go d.runSubscriber(ctx)` and before `return srv.Serve(ctx)`, add:

```go
	go func() {
		_ = dashboard.Run(ctx, NewDashboardAdapter(d), d.Cfg.Dashboard, d.Log)
	}()
```

- [ ] **Step 2: Run the daemon test suite**

Run: `go test ./internal/daemon/...`
Expected: PASS.

- [ ] **Step 3: Build and confirm no regressions**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "$(cat <<'EOF'
feat(daemon): launch dashboard alongside Run

dashboard.Run is started as a goroutine after the IPC server and Nostr
subscriber. With cfg.Dashboard.Enabled defaulting to true, every
running daemon now serves http://127.0.0.1:22893 by default. Disabling
or non-loopback bind is handled inside dashboard.Run with explicit
log lines (no-op + warning).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: CLI `eidos gate dashboard`

**Files:**
- Create: `cmd/eidos/gate/dashboard.go`

- [ ] **Step 1: Write the subcommand**

Create `cmd/eidos/gate/dashboard.go`:

```go
package gate

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

var dashboardNoOpen bool

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open the gate daemon's web dashboard in the default browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		stateDir, err := config.ResolveStateDir(stateDirFlag)
		if err != nil {
			return err
		}
		cfg, err := config.Load(filepath.Join(stateDir, "config.toml"))
		if err != nil {
			return err
		}
		if !cfg.Dashboard.Enabled {
			return fmt.Errorf("dashboard is disabled in config (%s/config.toml [dashboard].enabled)", stateDir)
		}
		url := "http://" + cfg.Dashboard.Listen

		// Probe the URL with a short timeout to verify the daemon is up.
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", url+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("dashboard not reachable at %s; is the daemon running? (%w)", url, err)
		}
		_ = resp.Body.Close()

		fmt.Println(url)
		if dashboardNoOpen {
			return nil
		}
		return openBrowser(url)
	},
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("auto-open not supported on %s; open %s manually", runtime.GOOS, url)
	}
	return cmd.Start()
}

func init() {
	dashboardCmd.Flags().BoolVar(&dashboardNoOpen, "no-open", false, "print URL only; do not auto-launch browser")
	rootCmd.AddCommand(dashboardCmd)
}
```

If `stateDirFlag` is named differently in the existing CLI (check `cmd/eidos/gate/root.go`), use the canonical name. Look for the package-level state-dir flag binding.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Smoke-test against a local daemon**

This step is manual (CI doesn't run a daemon). Briefly:

```bash
# In one terminal:
go run ./cmd/eidos gate daemon --state-dir /tmp/dash-smoke

# In another:
go run ./cmd/eidos gate dashboard --state-dir /tmp/dash-smoke --no-open
# Expected output: http://127.0.0.1:22893
```

If the URL prints, the route is up. (Init / contact wiring happens in the integration test, Task 11.)

- [ ] **Step 4: Commit**

```bash
git add cmd/eidos/gate/dashboard.go
git commit -m "$(cat <<'EOF'
feat(gate): eidos gate dashboard subcommand

Reads the daemon's config to derive the dashboard URL, probes /
with a 1s timeout to verify the daemon is up, prints the URL, and
launches the OS browser unless --no-open is set. xdg-open / open /
rundll32 cover the three target platforms; unsupported OS prints an
explicit "open manually" message.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Integration tests

**Files:**
- Create: `test/integration/dashboard_test.go`

- [ ] **Step 1: Inspect existing harness for state.db meta access**

Run: `grep -n "DB.GetMeta\|SetMeta\|meta.*label" /data/eidopsyche/internal/store/*.go | head -10`

This confirms the API the dashboard adapter uses for `OwnLabel`.

- [ ] **Step 2: Create the integration test**

Create `test/integration/dashboard_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

// dashboardURL returns the running daemon's dashboard URL.
func dashboardURL(in *instance) string {
	return "http://" + in.daemon.Cfg.Dashboard.Listen
}

func TestDashboard_ShellOK(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	// Give the dashboard goroutine a moment to bind.
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(dashboardURL(alice) + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "alice") {
		t.Errorf("shell missing alice label; got: %s", string(body))
	}
	if !strings.Contains(string(body), "htmx") {
		t.Errorf("shell missing htmx script include")
	}
}

func TestDashboard_PostSendThenThreadShowsBubble(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)
	time.Sleep(200 * time.Millisecond)

	form := strings.NewReader("text=hello+from+web")
	req, _ := http.NewRequest("POST", dashboardURL(alice)+"/thread/"+bob.daemon.Key.PublicHex+"/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("send status %d body %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from web") {
		t.Errorf("send response missing bubble: %s", string(body))
	}

	// Now fetch Bob's thread for Alice; the message should be there.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(dashboardURL(bob) + "/thread/" + alice.daemon.Key.PublicHex)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "hello from web") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Bob's thread for Alice never showed the message")
}

func TestDashboard_SoftRejectVisibleInMalformedView(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	sendRawContent(t, bob, alice, "not an envelope")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(dashboardURL(alice) + "/messages?malformed=1")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "not_envelope") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Alice's malformed view never showed the soft-reject")
}

func TestDashboard_SSEDeliversInboxOnPeerSend(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", dashboardURL(alice)+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Trigger a peer send a moment after subscribing.
	go func() {
		time.Sleep(300 * time.Millisecond)
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "via sse"}
		sendEnvelopeIPC(t, bob, alice.daemon.Key.PublicHex, env)
	}()

	buf := make([]byte, 8192)
	deadline := time.Now().Add(5 * time.Second)
	var seen string
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		seen += string(buf[:n])
		if strings.Contains(seen, "event: inbox.message") {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not observe inbox.message SSE event; saw: %q", seen)
}
```

This relies on `sendEnvelopeIPC` which is already defined in `test/integration/envelope_test.go` (same package).

- [ ] **Step 3: Run integration tests**

Run: `go test -tags=integration ./test/integration/... -run TestDashboard -v -timeout 90s`
Expected: PASS for all four tests.

- [ ] **Step 4: Commit**

```bash
git add test/integration/dashboard_test.go
git commit -m "$(cat <<'EOF'
test(dashboard): integration coverage

Four end-to-end scenarios reusing the bringUp harness:
- GET / returns 200 with the operator label and htmx include
- POST /thread/{bob}/send → bubble fragment; Bob's GET /thread/{alice}
  shows the message after subscriber processes it
- /messages?malformed=1 surfaces a peer's plain-text soft-reject
- GET /events streams an inbox.message named event when a peer publishes

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Frontend craft pass — invoke `frontend-design` skill

**Files:**
- Modify: `internal/dashboard/static/app.css`
- Modify: `internal/dashboard/templates/*.html` (only as the skill suggests)

This task does not write code blindly. It invokes the `frontend-design` skill with the current dashboard state as input and applies the polish recommendations.

- [ ] **Step 1: Run the dashboard locally for the skill to evaluate**

Build, init, run the daemon in a temp state dir, send a few sample messages and add a couple of contacts so the dashboard has real content to render. Open in a browser and confirm baseline rendering.

- [ ] **Step 2: Invoke the `frontend-design` skill**

Tell the skill:
- The current state of `internal/dashboard/static/app.css` and the template files.
- The visual goal from spec §10.3: distinctive aesthetic, dark default + light via prefers-color-scheme, hand-written CSS budget ≤ 8KB minified, JS budget ≤ 20KB total including htmx, must look unlike a default Bootstrap form.
- The constraint: no preprocessor, no framework, no JS for visual behaviour.

The skill returns concrete CSS edits and structural HTML cleanups. Apply them, keeping the budget bound and the template public surface (template names, view-data field names) untouched.

- [ ] **Step 3: Verify visual budget**

Run:

```bash
wc -c internal/dashboard/static/app.css internal/dashboard/static/htmx.min.js internal/dashboard/static/htmx-ext-sse.js
```

Expected: app.css ≤ 8KB; htmx.min.js + htmx-ext-sse.js ≤ 20KB total. If over, trim back.

- [ ] **Step 4: Confirm no template tests broke**

Run: `go test ./internal/dashboard/...`
Expected: PASS. (If a template name or view-data field changed, that's a bug — tests will catch it.)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/static/app.css internal/dashboard/templates/
git commit -m "$(cat <<'EOF'
style(dashboard): frontend-design polish pass

Applies recommendations from the frontend-design skill: distinctive
typography, considered spacing, dark/light parity. Stays within the
v1 visual budget (CSS ≤ 8KB, JS ≤ 20KB total) and preserves the
template names and view-data field surface tested in handler unit
tests.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Documentation

**Files:**
- Modify: `docs/USAGE.md`

- [ ] **Step 1: Add a Dashboard section**

Locate the existing `## Other commands` section in `docs/USAGE.md`. Insert before it (or in another reasonable location) a new section:

```markdown
## Web Dashboard

The daemon serves a local web dashboard at `http://127.0.0.1:22893` whenever
it is running. It is **loopback-only** by default — anyone with shell access
to the host already has access to your gate state, so the dashboard inherits
that trust boundary and adds no auth on top.

Open the dashboard:

```
$ eidos gate dashboard
http://127.0.0.1:22893
# (browser opens)
```

Use `--no-open` for a headless host:

```
$ eidos gate dashboard --no-open
http://127.0.0.1:22893
```

If `eidos gate dashboard` errors with "dashboard not reachable", start the
daemon first (`eidos gate start` or `eidos gate daemon`).

Disable the dashboard entirely by setting `[dashboard] enabled = false` in
`config.toml`. Non-loopback bind (e.g. `0.0.0.0:22893`) is refused in v1
with an error log line; for remote access, use SSH port-forwarding:

```
$ ssh -L 22893:localhost:22893 user@your-server
$ open http://localhost:22893    # in another terminal on your laptop
```

What's in v1: chat thread per contact (primary), all-messages /
soft-rejected list views, in-place compose. Setup actions (`init`,
contact / relay / invite management, label changes) stay in the CLI.
```

(Replace the inner triple-backticks with real triple-backticks in the actual edit; they're escaped here for the plan document.)

- [ ] **Step 2: Commit**

```bash
git add docs/USAGE.md
git commit -m "$(cat <<'EOF'
docs(usage): describe the local web dashboard

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: CI parity, push, open PR, watch CI, fix Copilot

- [ ] **Step 1: Run CI-equivalent checks locally**

```bash
gofmt -l . && go vet ./... && $(go env GOPATH)/bin/staticcheck ./... && go test ./... && go test -tags=integration ./test/integration/... && go build -o /tmp/eidos ./cmd/eidos
```

Expected: no gofmt output; no vet/staticcheck output; all tests pass; build succeeds.

If anything fails, fix in additional commits before pushing.

- [ ] **Step 2: Push branch**

```bash
git push -u origin feat/dashboard
```

- [ ] **Step 3: Open the PR**

```bash
gh pr create --title "feat(gate): local web dashboard (loopback HTMX UI)" --body "$(cat <<'EOF'
## Summary

- New `internal/dashboard/` package owns an HTTP server inside the gate daemon, exposing a chat-app-style UI on `127.0.0.1:22893`.
- Sidebar = contacts (with last-message-time sort) + "All messages" / "Soft-rejected" filters + Compose-to-npub. Main pane = chat thread (paradigm A primary) or list view (paradigm B supplement).
- Live updates via Server-Sent Events fanned out from the existing `broadcastInbox` path.
- Send reuses the daemon's existing `nostr.Wrap` + `Pool.Publish` code via a thin adapter; no new IPC methods.
- HTMX + Go `html/template` only. No Node, no npm. htmx is vendored under `internal/dashboard/static/`.
- Loopback-only for v1; non-loopback bind is refused with an explicit log line. No auth — operator's host trust boundary is sufficient.

Spec: `docs/superpowers/specs/2026-05-07-dashboard-design.md`
Plan: `docs/superpowers/plans/2026-05-07-dashboard.md`

## Test plan

- [x] `go test ./internal/dashboard/...` — codec, render, handlers, SSE
- [x] `go test ./internal/daemon/...` — hub fan-out + dispatch unchanged
- [x] `go test -tags=integration ./test/integration/... -run TestDashboard` — four end-to-end scenarios (shell render, send→thread, soft-reject view, SSE delivery)
- [x] gofmt, go vet, staticcheck all clean
- [x] `go build ./cmd/eidos` succeeds
- [x] Manual smoke test: `eidos gate dashboard` opens browser, sends/receives via web UI
- [x] frontend-design skill applied to CSS/templates

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

```bash
gh run list -b feat/dashboard --limit 3 --json databaseId,status,conclusion -q '.[] | "\(.databaseId) \(.status) \(.conclusion // "—")"'
gh run watch <latest-id> --exit-status
```

If CI fails, fix in new commits. Do not amend or force-push.

- [ ] **Step 5: Fetch Copilot review (if any)**

```bash
gh api repos/LucianoXu/eidopsyche/pulls/<PR>/comments --jq '.[] | {id, path, line: (.line // .original_line), body}'
```

Address each comment per the criteria established in PR #5 (the envelope-v1 work):
- Real bugs / spec divergence → fix
- Style / safety improvements → apply
- Subjective taste differences → apply if cheap, reply with reasoning if not
- User's prior intentional design choices → reply explaining; do not silently revert

Push fix commits.

- [ ] **Step 6: Final CI run + report**

After Copilot fixes:

```bash
gh run watch <new-run-id> --exit-status
```

When green, print the PR URL.

---

## Self-review against the spec

| Spec § | Topic | Plan task |
|---|---|---|
| 3 Layout | sidebar + thread + compose + list | Task 5 (templates), Task 6 (handlers) |
| 3.1 Sidebar ordering & blocked filter | sortContactsForSidebar + buildSidebar | Task 5 (sort), Task 6 (filter) |
| 3.2 Thread chat | bubble template + buildBubbles | Task 5 + 6 |
| 3.3 List view | messages template + view + filter | Task 5 + 6 |
| 3.4 Header / settings cog | shell template; cog placeholder is "out of scope" | Task 5 (HTML; cog stub deferred) |
| 4 Process placement | dashboard.Run launched from daemon.Run | Task 9 |
| 5 Transport / SSE | sse.go + /events | Task 8 |
| 5.1 Routes table | shell, sidebar, messages, thread, send, events, static | Tasks 6, 7, 8 |
| 5.2 SSE event types | renderEvent dispatch | Task 8 |
| 6.1 Module organization | internal/dashboard/* | Tasks 2, 5–8 |
| 6.2 DashboardDeps + adapter | deps.go + dashboardAdapter | Tasks 2, 3 |
| 6.3 Daemon integration + non-loopback refusal | server.go isLoopback + daemon.Run hook | Tasks 2, 9 |
| 7 CLI | eidos gate dashboard | Task 10 |
| 8 Auth posture | sameOriginGuard + loopback gate | Task 2 |
| 9 Build pipeline | go:embed templates + static | Task 5 |
| 10.1 Unit tests | render_test, handlers_test, sse_test, pagination_test | Tasks 5, 6, 7, 8 |
| 10.2 Integration tests | test/integration/dashboard_test.go | Task 11 |
| 10.3 Frontend craft | frontend-design pass | Task 12 |
| 11–14 deferred / acceptance | covered in PR description | Task 14 |

No spec section is uncovered. Note that `pagination_test.go` is implicitly subsumed by handler tests that exercise pagination — if explicit pagination edge tests are valuable, add them in Task 6 step 4.
