# Relay top-level subcommand and state decoupling — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lift the embedded Nostr relay from `eidos gate relay` to a top-level `eidos relay` subcommand with its own config dir, event persistence (sqlite), no publisher whitelist, and split service-unit installation — enabling clean relay-only deployments.

**Architecture:** A new `cmd/eidos/relay` cobra subtree replaces `cmd/eidos/gate/relay.go`. A new `internal/relaycfg` package owns relay config (no shared types with gate). `internal/relayd` gains a `fiatjaf/eventstore/sqlite3` backend and loses its whitelist source. `internal/service` splits into independent gate-only and relay-only install paths under separate unit names (`eidos-gate-daemon`, `eidos-relay`). Pre-1.0 breaking change — `eidos gate relay` and `[relay]` in gate config are removed in this release.

**Tech Stack:** Go; `github.com/spf13/cobra`; `github.com/fiatjaf/khatru`; `github.com/fiatjaf/eventstore/sqlite3` (new dep); `github.com/BurntSushi/toml`; `github.com/nbd-wtf/go-nostr`.

**Spec:** `docs/superpowers/specs/2026-05-09-relay-top-level-design.md`

---

## File mapping

**Created:**
- `internal/relaycfg/config.go` + `internal/relaycfg/config_test.go`
- `internal/relayd/store.go` + persistence test cases in `internal/relayd/relayd_test.go`
- `cmd/eidos/relay/root.go`
- `cmd/eidos/relay/init.go` + `cmd/eidos/relay/init_test.go`
- `cmd/eidos/relay/start.go`
- `cmd/eidos/relay/config.go`
- `cmd/eidos/relay/status.go`
- `cmd/eidos/relay/service.go`

**Removed:**
- `cmd/eidos/gate/relay.go`
- `internal/relayd/whitelist.go`

**Modified (code):**
- `cmd/eidos/main.go` — register `relay.Command()`
- `internal/relayd/relayd.go` — drop `Whitelist`, accept eventstore
- `internal/relayd/relayd_test.go` — drop whitelist setup, add persistence test
- `internal/config/config.go` + `config_test.go` — remove `Relay` block, `RelayEnabled()`
- `cmd/eidos/gate/init.go` + `init_test.go` — remove `--with-local-relay`, `--listen`
- `cmd/eidos/gate/start.go`, `stop.go`, `status.go`, `purge.go`, `config.go` — drop `cfg.RelayEnabled()` branches and `relay.*` config keys
- `cmd/eidos/gate/preflight.go` (if it touches relay listen) — clean up
- `cmd/eidos/gate/service.go` — gate service install no longer references relay unit
- `internal/service/service.go` — `RelayUnitName = "eidos-relay"`; split installer signatures
- `internal/service/systemd_linux.go` + tests — split daemon-only / relay-only install paths
- `internal/service/launchd_darwin.go` + `launchd_darwin_test.go`, `launchd_test.go` — same split
- `internal/service/scm_windows.go` + `scm_windows_test.go` — same split
- `internal/store/schema.go` — drop `relay_whitelist` view

**Modified (docs):**
- `SPEC.md` — three-role topology, relay-not-an-entity, persistence note, whitelist removal
- `EXAMPLE.md` — relay deployment commands
- `README.md` — quick start mentions if any
- `CHANGELOG.md` — `BREAKING CHANGES` entry with migration recipe

---

## Task 1: New `internal/relaycfg` package

**Files:**
- Create: `internal/relaycfg/config.go`
- Create: `internal/relaycfg/config_test.go`

- [ ] **Step 1: Write the failing tests.** In `internal/relaycfg/config_test.go`:

```go
package relaycfg

import (
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Relay.Mode != "paired" {
		t.Errorf("mode = %q, want %q", d.Relay.Mode, "paired")
	}
	if d.Relay.Listen != "0.0.0.0:7777" {
		t.Errorf("listen = %q", d.Relay.Listen)
	}
	if !d.Relay.Auth.Required {
		t.Errorf("auth.required = false, want true")
	}
	if d.Relay.OwnerPubkey != "" {
		t.Errorf("owner_pubkey = %q, want empty (no default)", d.Relay.OwnerPubkey)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Relay.Mode = "public"
	cfg.Relay.Listen = "127.0.0.1:9001"
	cfg.Relay.TLS.CertFile = "/etc/eidos/cert.pem"
	cfg.Relay.TLS.KeyFile = "/etc/eidos/key.pem"
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relay.Mode != "public" || got.Relay.Listen != "127.0.0.1:9001" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Relay.TLS.CertFile != "/etc/eidos/cert.pem" || got.Relay.TLS.KeyFile != "/etc/eidos/key.pem" {
		t.Errorf("tls round-trip mismatch: %+v", got.Relay.TLS)
	}
}

func TestLoadAuthDefaultsTrueWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	// Hand-write a config that omits [relay.auth].
	body := `
[relay]
mode = "public"
listen = "0.0.0.0:7777"
`
	if err := writeFile(t, filepath.Join(dir, "config.toml"), body); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Relay.Auth.Required {
		t.Errorf("auth.required defaulted to false; want true when section absent")
	}
}

func TestLoadRejectsPairedWithoutOwner(t *testing.T) {
	dir := t.TempDir()
	body := `
[relay]
mode = "paired"
listen = "0.0.0.0:7777"
`
	if err := writeFile(t, filepath.Join(dir, "config.toml"), body); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected paired-without-owner to fail validation")
	}
}

func TestLoadRejectsPublicWithOwner(t *testing.T) {
	dir := t.TempDir()
	body := `
[relay]
mode = "public"
listen = "0.0.0.0:7777"
owner_pubkey = "deadbeef"
`
	if err := writeFile(t, filepath.Join(dir, "config.toml"), body); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected public-with-owner to fail validation")
	}
}

func writeFile(t *testing.T, path, body string) error {
	t.Helper()
	return os.WriteFile(path, []byte(body), 0o600)
}
```

(Add `"os"` to the imports — remove the helper if you prefer to inline `os.WriteFile`.)

- [ ] **Step 2: Run tests, expect failure** (`undefined: Defaults`, `Load`, `Save`).

```sh
go test ./internal/relaycfg/...
```

Expected: build errors / undefined symbols.

- [ ] **Step 3: Implement `internal/relaycfg/config.go`.**

```go
// Package relaycfg owns the embedded relay's configuration: a TOML
// file rooted at the relay's state directory, distinct from the gate
// config in internal/config. Kept separate so a relay-only build path
// does not import gate-specific types.
package relaycfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const FileName = "config.toml"

type Config struct {
	LogLevel string      `toml:"log_level,omitempty"`
	Relay    RelaySection `toml:"relay"`
}

type RelaySection struct {
	Mode        string         `toml:"mode"`
	Listen      string         `toml:"listen"`
	OwnerPubkey string         `toml:"owner_pubkey,omitempty"`
	TLS         TLSSection     `toml:"tls,omitempty"`
	Auth        AuthSection    `toml:"auth,omitempty"`
}

type TLSSection struct {
	CertFile string `toml:"cert_file,omitempty"`
	KeyFile  string `toml:"key_file,omitempty"`
}

type AuthSection struct {
	Required   bool   `toml:"required"`
	ServiceURL string `toml:"service_url,omitempty"`
}

// Defaults returns a Config with the spec-defined defaults applied.
// OwnerPubkey deliberately has no default — `eidos relay init` requires
// --owner when --mode=paired and rejects it when --mode=public.
func Defaults() Config {
	return Config{
		LogLevel: "info",
		Relay: RelaySection{
			Mode:   "paired",
			Listen: "0.0.0.0:7777",
			Auth:   AuthSection{Required: true},
		},
	}
}

// Load reads dir/config.toml, applies spec defaults for any fields the
// file omits, and validates mode/owner consistency.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, FileName)
	cfg := Defaults()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("relaycfg load %s: %w", path, err)
	}
	// Re-apply spec default for Auth.Required if the field was absent.
	if !meta.IsDefined("relay", "auth", "required") {
		cfg.Relay.Auth.Required = true
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes cfg to dir/config.toml (mkdir -p the parent first).
func Save(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("relaycfg save mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("relaycfg save open %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("relaycfg save encode: %w", err)
	}
	return nil
}

// Validate checks the invariants `init` and `start` both rely on.
func (c Config) Validate() error {
	switch c.Relay.Mode {
	case "paired":
		if strings.TrimSpace(c.Relay.OwnerPubkey) == "" {
			return fmt.Errorf("relay.mode=paired requires owner_pubkey")
		}
	case "public":
		if strings.TrimSpace(c.Relay.OwnerPubkey) != "" {
			return fmt.Errorf("relay.mode=public must not set owner_pubkey")
		}
	default:
		return fmt.Errorf("relay.mode must be \"paired\" or \"public\" (got %q)", c.Relay.Mode)
	}
	if strings.TrimSpace(c.Relay.Listen) == "" {
		return fmt.Errorf("relay.listen must be set (host:port)")
	}
	if (c.Relay.TLS.CertFile == "") != (c.Relay.TLS.KeyFile == "") {
		return fmt.Errorf("relay.tls: cert_file and key_file must both be set or both empty")
	}
	return nil
}

// EventStorePath returns the canonical events.db path for a config dir.
func EventStorePath(dir string) string { return filepath.Join(dir, "events.db") }

// DefaultDir returns the conventional relay config dir under the gate's
// XDG_CONFIG_HOME (or $HOME/.config). Stays a sibling of gate's config
// dir so co-located deployments share the parent.
func DefaultDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "eidos", "relay"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "eidos", "relay"), nil
}
```

- [ ] **Step 4: Run tests, expect pass.**

```sh
go test ./internal/relaycfg/...
```

Expected: `ok  github.com/LucianoXu/eidopsyche/internal/relaycfg`.

- [ ] **Step 5: Commit.**

```sh
git add internal/relaycfg/
git commit -m "feat(relay): internal/relaycfg package for relay-only config

Owns the relay's config.toml independently of internal/config. Spec
default Auth.Required=true is re-applied at Load() when the file
omits the section. Validate() enforces mode/owner_pubkey consistency
so init and start share one truth."
```

---

## Task 2: Eventstore wiring in `internal/relayd`

**Files:**
- Create: `internal/relayd/store.go`
- Modify: `internal/relayd/relayd.go`
- Modify: `internal/relayd/relayd_test.go` (add persistence test; existing tests untouched yet)
- Modify: `go.mod`, `go.sum` (new dependency)

- [ ] **Step 1: Add the eventstore dependency.**

```sh
go get github.com/fiatjaf/eventstore/sqlite3@latest
go mod tidy
```

Expected: `go.mod` gains `github.com/fiatjaf/eventstore vX.Y.Z` (and indirect deps).

- [ ] **Step 2: Write `internal/relayd/store.go`.**

```go
package relayd

import (
	"fmt"

	"github.com/fiatjaf/eventstore/sqlite3"
)

// OpenSQLiteStore creates and initializes a sqlite-backed eventstore at
// path. The caller owns Close().
func OpenSQLiteStore(path string) (*sqlite3.SQLite3Backend, error) {
	if path == "" {
		return nil, fmt.Errorf("OpenSQLiteStore: path is empty")
	}
	b := &sqlite3.SQLite3Backend{DatabaseURL: path}
	if err := b.Init(); err != nil {
		return nil, fmt.Errorf("eventstore sqlite3 init %s: %w", path, err)
	}
	return b, nil
}
```

(The concrete `*sqlite3.SQLite3Backend` is what's wired into khatru's handler slices; no abstract interface is needed at this stage.)

- [ ] **Step 3: Modify `internal/relayd/relayd.go`** — add `EventStorePath` to `Config` and wire khatru handlers when set:

```go
type Config struct {
	Mode      Mode
	Listen    string
	OwnerHex  string
	Whitelist *WhitelistSource         // (still here; removed in Task 3)
	TLS       TLSConfig
	Auth      AuthConfig
	EventStorePath string               // NEW. Empty = ephemeral (today's behavior).
}
```

In `New()`, after the mode switch and before constructing `srv`:

```go
if cfg.EventStorePath != "" {
	store, err := OpenSQLiteStore(cfg.EventStorePath)
	if err != nil {
		return nil, err
	}
	r.StoreEvent   = append(r.StoreEvent,   store.SaveEvent)
	r.QueryEvents  = append(r.QueryEvents,  store.QueryEvents)
	r.CountEvents  = append(r.CountEvents,  store.CountEvents)
	r.DeleteEvent  = append(r.DeleteEvent,  store.DeleteEvent)
	r.ReplaceEvent = append(r.ReplaceEvent, store.ReplaceEvent)
	// Closing the store on shutdown: store the handle on Server.
}
```

Add an `eventStore *sqlite3.SQLite3Backend` field on `Server`. Update `Server.Shutdown` and `Server.Close` to call `s.eventStore.Close()` after the http server drains.

- [ ] **Step 4: Add a persistence test in `internal/relayd/relayd_test.go`.** Append after the existing tests:

```go
func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	db, err := store.Open(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := gnostr.GeneratePrivateKey()
	ownerPK, _ := gnostr.GetPublicKey(owner)
	if err := db.SetMeta(ctx, "owner_pubkey", ownerPK); err != nil {
		t.Fatal(err)
	}
	wl := NewWhitelistSource(db, 100*time.Millisecond)
	if err := wl.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(dir, "events.db")
	addr := freePort(t)

	// First run: publish a kind:1059 event addressed to owner.
	srv1, err := New(Config{
		Mode:           ModePaired,
		Listen:         addr,
		OwnerHex:       ownerPK,
		Whitelist:      wl,
		EventStorePath: eventsPath,
		Auth:           AuthConfig{Required: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv1.ListenAndServe()
	time.Sleep(200 * time.Millisecond)

	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	sender := gnostr.GeneratePrivateKey()
	senderPK, _ := gnostr.GetPublicKey(sender)
	ev := gnostr.Event{
		Kind:      1059,
		PubKey:    senderPK,
		CreatedAt: gnostr.Now(),
		Content:   "wrapped",
		Tags:      gnostr.Tags{{"p", ownerPK}},
	}
	if err := ev.Sign(sender); err != nil {
		t.Fatal(err)
	}
	if err := relay.Publish(ctx, ev); err != nil {
		t.Fatalf("publish: %v", err)
	}
	relay.Close()
	if err := srv1.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// Second run: same eventsPath, fresh server, query. Expect 1 event.
	srv2, err := New(Config{
		Mode:           ModePaired,
		Listen:         addr,
		OwnerHex:       ownerPK,
		Whitelist:      wl,
		EventStorePath: eventsPath,
		Auth:           AuthConfig{Required: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv2.ListenAndServe()
	defer srv2.Shutdown(ctx)
	time.Sleep(200 * time.Millisecond)

	relay2, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay2.Close()

	sub, err := relay2.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{ownerPK}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != ev.ID {
			t.Errorf("event id mismatch: got %s want %s", got.ID, ev.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received from persistent store")
	}
}
```

- [ ] **Step 5: Run tests, expect pass.**

```sh
go test ./internal/relayd/...
```

Expected: `ok` with the new test green.

- [ ] **Step 6: Commit.**

```sh
git add internal/relayd/ go.mod go.sum
git commit -m "feat(relay): sqlite event persistence via fiatjaf/eventstore

Adds Config.EventStorePath. Empty = today's ephemeral behavior; non-
empty wires khatru's Store/Query/Count/Delete/Replace handlers to a
sqlite-backed eventstore. Persistence test publishes a gift-wrap,
restarts the server, and confirms the event is served from disk."
```

---

## Task 3: Drop publisher whitelist from relayd

**Files:**
- Delete: `internal/relayd/whitelist.go`
- Modify: `internal/relayd/relayd.go`
- Modify: `internal/relayd/relayd_test.go`
- Modify: `cmd/eidos/gate/relay.go` (transitionally; deleted in Task 9)

- [ ] **Step 1: Remove the `Whitelist` field from `relayd.Config`** and the paired-mode check in `internal/relayd/relayd.go`:

```go
type Config struct {
	Mode      Mode
	Listen    string
	OwnerHex  string
	TLS       TLSConfig
	Auth      AuthConfig
	EventStorePath string
}
```

In `New()`, the paired-mode block becomes:

```go
case ModePaired:
	if cfg.OwnerHex == "" {
		return nil, fmt.Errorf("paired mode requires OwnerHex")
	}
	r.RejectEvent = append(r.RejectEvent,
		func(ctx context.Context, event *gnostr.Event) (bool, string) {
			if event.Kind != 1059 {
				return true, "blocked: only kind 1059 accepted"
			}
			for _, tag := range event.Tags {
				if len(tag) >= 2 && tag[0] == "p" && tag[1] == cfg.OwnerHex {
					return false, ""
				}
			}
			return true, "blocked: not addressed to owner"
		},
	)
```

(No whitelist iteration; the addressed-to-owner check is the sole filter on top of `kind == 1059`.)

- [ ] **Step 2: Delete `internal/relayd/whitelist.go`.**

```sh
git rm internal/relayd/whitelist.go
```

- [ ] **Step 3: Update `internal/relayd/relayd_test.go`.** Remove every reference to `NewWhitelistSource`, `WhitelistSource`, and `Whitelist:` in `Config{}`. The persistence test from Task 2 also removes its `Whitelist` field. The existing `TestPairedModeRejection` keeps its assertion that a non-1059 event is rejected; just drop the whitelist setup.

```go
func TestPairedModeRejection(t *testing.T) {
	dir := t.TempDir()
	owner := gnostr.GeneratePrivateKey()
	ownerPK, _ := gnostr.GetPublicKey(owner)

	addr := freePort(t)
	srv, err := New(Config{
		Mode:     ModePaired,
		Listen:   addr,
		OwnerHex: ownerPK,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(200 * time.Millisecond)

	ctx := context.Background()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	stranger := gnostr.GeneratePrivateKey()
	strangerPK, _ := gnostr.GetPublicKey(stranger)
	ev := gnostr.Event{
		Kind:      1,
		PubKey:    strangerPK,
		CreatedAt: gnostr.Now(),
		Content:   "hi",
	}
	if err := ev.Sign(stranger); err != nil {
		t.Fatal(err)
	}
	if err := relay.Publish(ctx, ev); err == nil {
		t.Fatal("expected publish rejection for non-1059 event")
	}
	_ = dir // keep tempdir to prevent unused-var error if dir is no longer used
}
```

(If `dir` becomes unused, drop it entirely.) Update other tests in the file the same way.

- [ ] **Step 4: Update `cmd/eidos/gate/relay.go`** to stop passing the whitelist (transitional — file is deleted entirely in Task 9, but it must build until then):

```go
// Drop these lines:
//   wl := relayd.NewWhitelistSource(db, time.Second)
//   ctx, cancel := signal.NotifyContext(...)  -- keep
//   go wl.Run(ctx)                            -- drop
// And drop `Whitelist: wl,` from the relayd.New(Config{...}) call.
```

The simplest delta: replace the whitelist setup block with nothing; keep the `db.GetMeta` call and the relayd.New call without the Whitelist field.

- [ ] **Step 5: Build and test.**

```sh
go build ./...
go test ./internal/relayd/... ./cmd/eidos/gate/...
```

Expected: all green.

- [ ] **Step 6: Commit.**

```sh
git add internal/relayd/ cmd/eidos/gate/relay.go
git commit -m "refactor(relay): remove publisher whitelist

SPEC :145 already classifies the relay-side whitelist as optional
defense-in-depth; the gate enforces the social-graph filter
client-side regardless of relay. Removing the whitelist eliminates
the last sqlite-shared-state coupling between gate and relay.

Paired mode now filters only on (kind == 1059) AND (addressed to
owner). Behavioral change: a paired relay accepts gift-wraps from
any signer, but the gate's client-side filter still drops anything
not from a contact."
```

---

## Task 4: `cmd/eidos/relay`: root + `init`

**Files:**
- Create: `cmd/eidos/relay/root.go`
- Create: `cmd/eidos/relay/init.go`
- Create: `cmd/eidos/relay/init_test.go`

- [ ] **Step 1: Write `cmd/eidos/relay/root.go`.**

```go
// Package relay implements the `eidos relay` subcommand tree:
// stateless infrastructure operations distinct from `eidos gate`.
package relay

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "relay",
	Short: "Run and manage an eidos Nostr relay",
	Long: `eidos relay manages a stand-alone Nostr relay process.

The relay is infrastructure, not an entity: it has no contacts,
no inbox, no NIP-17 messaging state. A relay-only host runs
` + "`eidos relay init` then `eidos relay service install/start`" + ` and
never invokes ` + "`eidos gate`" + `.`,
}

// Command returns the cobra root for `eidos relay`. Registered in
// cmd/eidos/main.go alongside forge, gate, and supervisor.
func Command() *cobra.Command { return rootCmd }
```

- [ ] **Step 2: Write the failing test in `cmd/eidos/relay/init_test.go`.**

```go
package relay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

func TestInitWritesConfigPaired(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{
		dir:    dir,
		mode:   "paired",
		listen: "127.0.0.1:9001",
		owner:  "abc123deadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay.Mode != "paired" || cfg.Relay.OwnerPubkey != "abc123deadbeef" {
		t.Errorf("unexpected cfg: %+v", cfg.Relay)
	}
	if _, err := os.Stat(filepath.Join(dir, "events.db")); err != nil {
		t.Errorf("events.db not created: %v", err)
	}
}

func TestInitRejectsPairedWithoutOwner(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "paired", listen: ":7777"}); err == nil {
		t.Fatal("expected paired-without-owner to fail")
	}
}

func TestInitRejectsPublicWithOwner(t *testing.T) {
	dir := t.TempDir()
	err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777", owner: "abc"})
	if err == nil {
		t.Fatal("expected public-with-owner to fail")
	}
}

func TestInitRejectsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err != nil {
		t.Fatal(err)
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err == nil {
		t.Fatal("expected re-init to fail without --force")
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":8888", force: true}); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
}
```

(The `_ = filepath.Join` keeps the package "used" if the test layout drops other references; tweak imports as the test grows.)

- [ ] **Step 3: Run, expect failure** (`undefined: runInit`, `initOpts`).

- [ ] **Step 4: Implement `cmd/eidos/relay/init.go`.**

```go
package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

type initOpts struct {
	dir    string
	mode   string
	listen string
	owner  string
	force  bool
}

var initFlags initOpts

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a relay config dir at ~/.config/eidos/relay (or --dir)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := initFlags.dir
		if dir == "" {
			d, err := relaycfg.DefaultDir()
			if err != nil {
				return err
			}
			dir = d
		}
		opts := initFlags
		opts.dir = dir
		return runInit(opts)
	},
}

func runInit(o initOpts) error {
	if strings.TrimSpace(o.mode) == "" {
		return fmt.Errorf("--mode is required (\"paired\" or \"public\")")
	}
	if strings.TrimSpace(o.listen) == "" {
		return fmt.Errorf("--listen is required (host:port)")
	}
	switch o.mode {
	case "paired":
		if strings.TrimSpace(o.owner) == "" {
			return fmt.Errorf("--owner is required when --mode=paired")
		}
	case "public":
		if strings.TrimSpace(o.owner) != "" {
			return fmt.Errorf("--owner must not be set when --mode=public")
		}
	default:
		return fmt.Errorf("--mode must be \"paired\" or \"public\" (got %q)", o.mode)
	}

	cfgPath := filepath.Join(o.dir, relaycfg.FileName)
	if _, err := os.Stat(cfgPath); err == nil && !o.force {
		return fmt.Errorf("relay already initialized at %s (re-run with --force to overwrite)", cfgPath)
	}
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", o.dir, err)
	}

	cfg := relaycfg.Defaults()
	cfg.Relay.Mode = o.mode
	cfg.Relay.Listen = o.listen
	cfg.Relay.OwnerPubkey = strings.TrimSpace(o.owner)
	if err := relaycfg.Save(o.dir, cfg); err != nil {
		return err
	}

	// Touch the event store now so a fresh `relay start` doesn't have
	// to create-on-first-write. Also catches FS permission issues at init.
	store, err := relayd.OpenSQLiteStore(relaycfg.EventStorePath(o.dir))
	if err != nil {
		return fmt.Errorf("create events.db: %w", err)
	}
	store.Close()

	fmt.Printf("relay initialized at %s\n", o.dir)
	return nil
}

func init() {
	initCmd.Flags().StringVar(&initFlags.dir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	initCmd.Flags().StringVar(&initFlags.mode, "mode", "", "paired | public (required)")
	initCmd.Flags().StringVar(&initFlags.listen, "listen", "", "address to bind, e.g. 0.0.0.0:7777 (required)")
	initCmd.Flags().StringVar(&initFlags.owner, "owner", "", "owner pubkey hex (required iff --mode=paired)")
	initCmd.Flags().BoolVar(&initFlags.force, "force", false, "overwrite existing config")
	rootCmd.AddCommand(initCmd)
}
```

- [ ] **Step 5: Run tests, expect pass.**

```sh
go test ./cmd/eidos/relay/...
```

- [ ] **Step 6: Commit.**

```sh
git add cmd/eidos/relay/
git commit -m "feat(relay): cmd/eidos/relay/{root,init}

Adds the eidos relay subtree (not yet registered in main.go) and
\`eidos relay init --mode {paired|public} --listen <h:p> [--owner npub]\`.
Mode/owner consistency rejected at runInit; existing config rejected
without --force. events.db is created at init so permission errors
surface eagerly."
```

---

## Task 5: `eidos relay start`

**Files:**
- Create: `cmd/eidos/relay/start.go`

- [ ] **Step 1: Write `cmd/eidos/relay/start.go`.** Foreground command, ports the logic from today's `cmd/eidos/gate/relay.go` to load from `relaycfg`:

```go
package relay

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

var startDir string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Run the relay in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := startDir
		if dir == "" {
			d, err := relaycfg.DefaultDir()
			if err != nil {
				return err
			}
			dir = d
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return fmt.Errorf("load relay config: %w (have you run `eidos relay init`?)", err)
		}

		srv, err := relayd.New(relayd.Config{
			Mode:           relayd.Mode(cfg.Relay.Mode),
			Listen:         cfg.Relay.Listen,
			OwnerHex:       cfg.Relay.OwnerPubkey,
			TLS:            relayd.TLSConfig{CertFile: cfg.Relay.TLS.CertFile, KeyFile: cfg.Relay.TLS.KeyFile},
			Auth:           relayd.AuthConfig{Required: cfg.Relay.Auth.Required, ServiceURL: cfg.Relay.Auth.ServiceURL},
			EventStorePath: relaycfg.EventStorePath(dir),
		})
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		fmt.Fprintf(os.Stderr, "eidos-relay listening on %s mode=%s\n", cfg.Relay.Listen, cfg.Relay.Mode)
		errc := make(chan error, 1)
		go func() { errc <- srv.ListenAndServe() }()
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			shutdown, scancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer scancel()
			return srv.Shutdown(shutdown)
		}
	},
}

func init() {
	startCmd.Flags().StringVar(&startDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	rootCmd.AddCommand(startCmd)
}
```

- [ ] **Step 2: Build.**

```sh
go build ./cmd/eidos/relay/...
```

Expected: clean.

- [ ] **Step 3: Smoke test manually** (skip if running headless / CI):

```sh
mkdir -p /tmp/eidos-relay-smoke
go run ./cmd/eidos relay init --dir /tmp/eidos-relay-smoke --mode public --listen 127.0.0.1:9001
go run ./cmd/eidos relay start --dir /tmp/eidos-relay-smoke &
sleep 1
curl -s -H 'Accept: application/nostr+json' http://127.0.0.1:9001/ | head -c 200
kill %1
```

Expected: NIP-11 JSON document containing `"name":"eidos-gate-relay"` (note: name still mentions gate-relay; we'll rename in a follow-up if desired — out of scope for this iteration).

- [ ] **Step 4: Commit.**

```sh
git add cmd/eidos/relay/start.go
git commit -m "feat(relay): eidos relay start

Foreground command. Loads relaycfg, opens the sqlite eventstore,
constructs relayd.Server, blocks on ListenAndServe with SIGINT/
SIGTERM driving graceful shutdown."
```

---

## Task 6: `eidos relay config get/set` and `eidos relay status`

**Files:**
- Create: `cmd/eidos/relay/config.go`
- Create: `cmd/eidos/relay/status.go`

- [ ] **Step 1: Write `cmd/eidos/relay/config.go`.**

```go
package relay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

var configDir string

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Get or set relay config keys",
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the current value of a relay config key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(configDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}
		v, err := getKey(cfg, args[0])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a relay config key (writes config.toml)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(configDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}
		if err := setKey(&cfg, args[0], args[1]); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return relaycfg.Save(dir, cfg)
	},
}

func resolveConfigDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return relaycfg.DefaultDir()
}

func getKey(cfg relaycfg.Config, key string) (string, error) {
	switch key {
	case "relay.mode":
		return cfg.Relay.Mode, nil
	case "relay.listen":
		return cfg.Relay.Listen, nil
	case "relay.owner_pubkey":
		return cfg.Relay.OwnerPubkey, nil
	case "relay.tls.cert_file":
		return cfg.Relay.TLS.CertFile, nil
	case "relay.tls.key_file":
		return cfg.Relay.TLS.KeyFile, nil
	case "relay.auth.required":
		return strconv.FormatBool(cfg.Relay.Auth.Required), nil
	case "relay.auth.service_url":
		return cfg.Relay.Auth.ServiceURL, nil
	case "log_level":
		return cfg.LogLevel, nil
	}
	return "", fmt.Errorf("unknown config key %q", key)
}

func setKey(cfg *relaycfg.Config, key, value string) error {
	switch key {
	case "relay.mode":
		cfg.Relay.Mode = value
	case "relay.listen":
		cfg.Relay.Listen = value
	case "relay.owner_pubkey":
		cfg.Relay.OwnerPubkey = strings.TrimSpace(value)
	case "relay.tls.cert_file":
		cfg.Relay.TLS.CertFile = value
	case "relay.tls.key_file":
		cfg.Relay.TLS.KeyFile = value
	case "relay.auth.required":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("relay.auth.required must be true|false: %w", err)
		}
		cfg.Relay.Auth.Required = b
	case "relay.auth.service_url":
		cfg.Relay.Auth.ServiceURL = value
	case "log_level":
		cfg.LogLevel = value
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func init() {
	configCmd.PersistentFlags().StringVar(&configDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	configCmd.AddCommand(configGetCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}
```

- [ ] **Step 2: Write `cmd/eidos/relay/status.go`.**

```go
package relay

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

var statusDir string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show relay configuration and event-store size",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(statusDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}

		fmt.Printf("config dir: %s\n", dir)
		fmt.Printf("mode:       %s\n", cfg.Relay.Mode)
		fmt.Printf("listen:     %s\n", cfg.Relay.Listen)
		if cfg.Relay.Mode == "paired" {
			fmt.Printf("owner:      %s\n", cfg.Relay.OwnerPubkey)
		}
		fmt.Printf("tls:        cert=%q key=%q\n", cfg.Relay.TLS.CertFile, cfg.Relay.TLS.KeyFile)
		fmt.Printf("auth:       required=%t service_url=%q\n", cfg.Relay.Auth.Required, cfg.Relay.Auth.ServiceURL)

		store, err := relayd.OpenSQLiteStore(relaycfg.EventStorePath(dir))
		if err != nil {
			return err
		}
		defer store.Close()
		count, err := store.CountEvents(context.Background(), gnostrFilterAll)
		if err != nil {
			return fmt.Errorf("count events: %w", err)
		}
		fmt.Printf("events:     %d stored\n", count)
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	rootCmd.AddCommand(statusCmd)
}
```

`gnostrFilterAll` is a small helper — define it in `status.go` or alongside:

```go
import gnostr "github.com/nbd-wtf/go-nostr"

var gnostrFilterAll = gnostr.Filter{} // empty filter matches everything
```

(Move the import into the file's top-level `import` block.)

- [ ] **Step 3: Build and test.**

```sh
go build ./cmd/eidos/relay/...
go test ./cmd/eidos/relay/...
```

- [ ] **Step 4: Commit.**

```sh
git add cmd/eidos/relay/config.go cmd/eidos/relay/status.go
git commit -m "feat(relay): eidos relay config get/set and status

config get/set drives relaycfg.Save through Validate(). status prints
the resolved config plus a CountEvents probe against events.db so
operators can confirm persistence is wired."
```

---

## Task 7: Rename `RelayUnitName` and split `internal/service` installers

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/systemd_linux.go`
- Modify: `internal/service/launchd_darwin.go`
- Modify: `internal/service/scm_windows.go`
- Modify: `internal/service/launchd_test.go`, `launchd_darwin_test.go`, `scm_windows_test.go`, `service_test.go`

- [ ] **Step 1: Rename the constant in `internal/service/service.go`.**

```go
const (
	DaemonUnitName = "eidos-gate-daemon"
	RelayUnitName  = "eidos-relay"   // was "eidos-gate-relay"
)
```

- [ ] **Step 2: Refactor each installer so daemon and relay install paths are independent.** Today (`internal/service/service.go:72-85`) the `Manager` interface has bundled `Install/Start/Stop/Uninstall` and a `Config.WithRelay` toggle. Split as follows:

```go
// internal/service/service.go
type Manager interface {
    InstallDaemon(ctx context.Context) error
    InstallRelay(ctx context.Context, relayDir string) error
    UninstallDaemon(ctx context.Context) error
    UninstallRelay(ctx context.Context) error
    StartDaemon(ctx context.Context) error
    StartRelay(ctx context.Context) error
    StopDaemon(ctx context.Context) error
    StopRelay(ctx context.Context) error
    Status(ctx context.Context) ([]Status, error) // unchanged: read-only, returns both names if installed
}
```

Drop `Config.WithRelay` (callers now choose by which method they invoke). `relayDir` is needed by `InstallRelay` because it becomes `ExecStart=<binary> relay start --dir <relayDir>` and `WorkingDirectory=<relayDir>` in the systemd unit (and equivalents on launchd / SCM).

Per-platform refactor (concrete for `systemd_linux.go`):

```go
func (s *systemd) InstallDaemon(ctx context.Context) error {
    dir, err := s.unitDir()
    if err != nil { return err }
    if err := os.MkdirAll(dir, 0o755); err != nil { return err }
    if err := writeUnitIfChanged(filepath.Join(dir, DaemonUnitName+".service"), s.daemonUnit()); err != nil {
        return err
    }
    return s.daemonReload(ctx)
}

func (s *systemd) InstallRelay(ctx context.Context, relayDir string) error {
    dir, err := s.unitDir()
    if err != nil { return err }
    if err := os.MkdirAll(dir, 0o755); err != nil { return err }
    if err := writeUnitIfChanged(filepath.Join(dir, RelayUnitName+".service"), s.relayUnit(relayDir)); err != nil {
        return err
    }
    return s.daemonReload(ctx)
}

func (s *systemd) UninstallDaemon(ctx context.Context) error { return s.uninstallOne(ctx, DaemonUnitName) }
func (s *systemd) UninstallRelay(ctx context.Context) error  { return s.uninstallOne(ctx, RelayUnitName) }
func (s *systemd) StartDaemon(ctx context.Context) error     { return s.enableNow(ctx, DaemonUnitName) }
func (s *systemd) StartRelay(ctx context.Context) error      { return s.enableNow(ctx, RelayUnitName) }
func (s *systemd) StopDaemon(ctx context.Context) error      { return s.stopOne(ctx, DaemonUnitName) }
func (s *systemd) StopRelay(ctx context.Context) error       { return s.stopOne(ctx, RelayUnitName) }
```

(Refactor existing `Install` / `Start` / `Stop` / `Uninstall` bodies into the per-name helpers — `uninstallOne`, `enableNow`, `stopOne` — most of the logic is already split internally.)

Update `s.relayUnit()` (which is private — see `systemd_linux.go:179`) to take a `relayDir string` parameter and emit:

```
[Service]
Environment=EIDOS_RELAY_HOME=<relayDir>
WorkingDirectory=<relayDir>
ExecStart=<binary> relay start --dir <relayDir>
```

Apply equivalent splits to `launchd_darwin.go` and `scm_windows.go`. The `launchdPlist` helper already takes a label and ProgramArguments slice — pass `[]string{"relay", "start", "--dir", relayDir}` in the relay path.

- [ ] **Step 3: Update existing tests** (`launchd_test.go`, `launchd_darwin_test.go`, `scm_windows_test.go`, `service_test.go`):

- Replace `RelayUnitName == "eidos-gate-relay"` references with `"eidos-relay"`.
- Tests that exercised the bundled `Install(ctx, WithRelay: true)` path now invoke `InstallDaemon` and `InstallRelay` separately.
- Tests that previously asserted `WithRelay=false → only daemon installed` become `InstallDaemon → only daemon installed`.
- `launchdPlist("eidos-gate-relay", ..., []string{"gate", "relay"})` becomes `launchdPlist("eidos-relay", ..., []string{"relay", "start"})`.

- [ ] **Step 4: Build + run service tests.**

```sh
go build ./...
go test ./internal/service/...
```

- [ ] **Step 5: Commit.**

```sh
git add internal/service/ cmd/eidos/gate/
git commit -m "refactor(service): split daemon/relay install paths

RelayUnitName renamed to \"eidos-relay\" (was \"eidos-gate-relay\").
Service implementations now expose InstallDaemon/InstallRelay,
StartDaemon/StartRelay, etc., so cmd/eidos/relay can manage its
unit independently of cmd/eidos/gate. Gate service install no
longer writes the relay unit; relay-only hosts never invoke gate
commands."
```

---

## Task 8: `eidos relay service` subcommand

**Files:**
- Create: `cmd/eidos/relay/service.go`

- [ ] **Step 1: Write `cmd/eidos/relay/service.go`.** Mirrors `cmd/eidos/gate/service.go` shape but calls `InstallRelay` / `StartRelay` / etc.:

```go
package relay

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

var (
	svcSystem bool
	svcDir    string
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Install and manage the eidos-relay system service",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the eidos-relay unit",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveServiceDir()
		if err != nil {
			return err
		}
		s, err := service.New(serviceConfig())
		if err != nil {
			return err
		}
		return s.InstallRelay(context.Background(), dir)
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Enable + start eidos-relay",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := service.New(serviceConfig())
		if err != nil {
			return err
		}
		return s.StartRelay(context.Background())
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop eidos-relay",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := service.New(serviceConfig())
		if err != nil {
			return err
		}
		return s.StopRelay(context.Background())
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print the eidos-relay service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := service.New(serviceConfig())
		if err != nil {
			return err
		}
		statuses, err := s.Status(context.Background())
		if err != nil {
			return err
		}
		for _, st := range statuses {
			if st.Name == service.RelayUnitName {
				fmt.Fprintf(cmd.OutOrStdout(),
					"%s\tinstalled=%t enabled=%t active=%t pid=%d\n",
					st.Name, st.Installed, st.Enabled, st.Active, st.PID)
				return nil
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\tnot installed\n", service.RelayUnitName)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Disable and remove eidos-relay",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := service.New(serviceConfig())
		if err != nil {
			return err
		}
		return s.UninstallRelay(context.Background())
	},
}

func resolveServiceDir() (string, error) {
	if svcDir != "" {
		return svcDir, nil
	}
	return relaycfg.DefaultDir()
}

func serviceConfig() service.Config {
	scope := service.ScopeUser
	if svcSystem {
		scope = service.ScopeSystem
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "" // service layer surfaces a clear error if BinaryPath is empty
	}
	dir, _ := relaycfg.DefaultDir()
	if svcDir != "" {
		dir = svcDir
	}
	return service.Config{
		BinaryPath: bin,
		StateDir:   dir,
		Scope:      scope,
	}
}

func init() {
	serviceCmd.PersistentFlags().BoolVar(&svcSystem, "system", false,
		"manage system-wide units (requires root) instead of user units")
	serviceCmd.PersistentFlags().StringVar(&svcDir, "dir", "",
		"relay config dir (default: ~/.config/eidos/relay)")
	serviceCmd.AddCommand(serviceInstallCmd, serviceStartCmd, serviceStopCmd, serviceStatusCmd, serviceUninstallCmd)
	rootCmd.AddCommand(serviceCmd)
}
```

(Adjust `service.Config` field names to match what `internal/service/service.go` exposes — the existing gate code is the reference.)

- [ ] **Step 2: Build.**

```sh
go build ./cmd/eidos/relay/...
```

- [ ] **Step 3: Commit.**

```sh
git add cmd/eidos/relay/service.go
git commit -m "feat(relay): eidos relay service install/start/stop/status/uninstall

Manages the eidos-relay unit through internal/service's split
installer. --system installs system-wide; default is per-user
(systemd --user / launchctl gui scope)."
```

---

## Task 9: Register `eidos relay` in `main.go` and remove `eidos gate relay`

**Files:**
- Modify: `cmd/eidos/main.go`
- Delete: `cmd/eidos/gate/relay.go`

- [ ] **Step 1: Register the subtree** in `cmd/eidos/main.go`:

```go
import (
	// ... existing ...
	"github.com/LucianoXu/eidopsyche/cmd/eidos/relay"
)

func main() {
	// ... existing rootCmd / subcommand setup ...
	rootCmd.AddCommand(gate.Command())
	rootCmd.AddCommand(forge.Command())
	rootCmd.AddCommand(supervisor.Command())
	rootCmd.AddCommand(relay.Command())   // NEW
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(selfUpdateCmd)
	// ...
}
```

- [ ] **Step 2: Delete the old gate-tree relay command.**

```sh
git rm cmd/eidos/gate/relay.go
```

- [ ] **Step 3: Build.**

```sh
go build ./...
```

Expected: clean. (Any compile errors here mean a leftover reference to gate's relay command — fix and re-build.)

- [ ] **Step 4: End-to-end smoke.**

```sh
mkdir -p /tmp/eidos-relay-e2e
go run ./cmd/eidos relay init --dir /tmp/eidos-relay-e2e --mode public --listen 127.0.0.1:9002
go run ./cmd/eidos relay start --dir /tmp/eidos-relay-e2e &
sleep 1
curl -s -H 'Accept: application/nostr+json' http://127.0.0.1:9002/ | head -c 200
kill %1
go run ./cmd/eidos relay status --dir /tmp/eidos-relay-e2e
```

Expected: NIP-11 doc on first curl; `events: 0 stored` from status (no events published yet).

- [ ] **Step 5: Commit.**

```sh
git add cmd/eidos/main.go cmd/eidos/gate/relay.go
git commit -m "feat(relay)!: register eidos relay; remove eidos gate relay

BREAKING CHANGE: \`eidos gate relay\` is removed. Use \`eidos relay
init\` and \`eidos relay start\` (or \`eidos relay service install\`).
The relay's config dir is ~/.config/eidos/relay/, separate from
the gate's state directory. See CHANGELOG for migration."
```

---

## Task 10: Drop gate's `[relay]` config and clean up gate flags / branches

**Files:**
- Modify: `internal/config/config.go` + `config_test.go`
- Modify: `cmd/eidos/gate/init.go` + `init_test.go`
- Modify: `cmd/eidos/gate/start.go`
- Modify: `cmd/eidos/gate/stop.go`
- Modify: `cmd/eidos/gate/status.go`
- Modify: `cmd/eidos/gate/purge.go`
- Modify: `cmd/eidos/gate/config.go`
- Modify: `cmd/eidos/gate/preflight.go` (if it references relay listen)

- [ ] **Step 1: Drop `Relay` block and `RelayEnabled()` from `internal/config/config.go`.**

Remove the `Relay` field from `Config`; remove `RelayConfig` / `RelayAuthConfig` / `RelayTLSConfig` type definitions; remove `cfg.Relay.*` paths from `Defaults()`.

The existing loader (`Load` at `internal/config/config.go:92`, `LoadWithMeta` at `:103`) uses `toml.DecodeFile` without `Strict()` — extra fields like a residual `[relay]` table are silently ignored by the decoder and surface in `meta.Undecoded()`. No loader change required.

Add a regression test:

```go
func TestLoadIgnoresStrayRelaySection(t *testing.T) {
	dir := t.TempDir()
	body := `
[daemon]
socket = "sock"
[relay]
mode = "paired"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "config.toml")); err != nil {
		t.Errorf("legacy [relay] section should be ignored, not error: %v", err)
	}
}
```

(Adjust the `Load` call to whatever signature it has — pass a file path if it takes one, or the directory if it takes that.)

- [ ] **Step 2: Drop `--with-local-relay` and `--listen` from `cmd/eidos/gate/init.go` flag set and tests.** Init no longer touches relay config at all.

- [ ] **Step 3: Drop `cfg.RelayEnabled()` branches from `start.go`, `stop.go`, `status.go`, `purge.go`.** Each becomes daemon-only:
  - `start.go`: `s.InstallDaemon(ctx)`, then `s.StartDaemon(ctx)`. No relay branch.
  - `stop.go`: `s.StopDaemon(ctx)`.
  - `status.go`: print only the daemon line. (Relay status moves to `eidos relay status` — already there.)
  - `purge.go`: `s.UninstallDaemon(ctx)`. **Do not** also call `UninstallRelay` here — the relay is owned by `eidos relay service uninstall` now. Document this in `purge.go`'s help text: *"Removes the gate daemon unit only. Run `eidos relay service uninstall` to remove the relay unit."*

- [ ] **Step 4: Drop `relay.*` keys from `cmd/eidos/gate/config.go`'s `configKeys` map.**

- [ ] **Step 5: Print a one-line warning at `gate start` if a `[relay]` table is present in `config.toml`.** Use the existing `LoadWithMeta` helper (`internal/config/config.go:103`):

```go
cfg, meta, err := config.LoadWithMeta(cfgPath)
if err != nil { return err }
for _, k := range meta.Undecoded() {
    if len(k) > 0 && k[0] == "relay" {
        fmt.Fprintln(os.Stderr,
            "warning: [relay] section is no longer read by gate; configure the relay via `eidos relay init`. "+
            "See CHANGELOG.md for the migration recipe.")
        break
    }
}
```

(Switch any call sites in gate code that use `Load` to `LoadWithMeta` if they need this warning. Other call sites can keep using `Load`.)

- [ ] **Step 6: Build + test.**

```sh
go build ./...
go test ./internal/config/... ./cmd/eidos/gate/...
```

- [ ] **Step 7: Commit.**

```sh
git add internal/config/ cmd/eidos/gate/
git commit -m "refactor(gate)!: drop [relay] config and RelayEnabled()

BREAKING CHANGE: gate config no longer has [relay]. The flags
--with-local-relay and --listen on \`eidos gate init\` are removed.
\`eidos gate start/stop/status/purge\` no longer manage the relay
unit; use \`eidos relay service ...\` instead.

\`eidos gate start\` prints a one-line warning when a residual
[relay] section is found, pointing at the migration recipe."
```

---

## Task 11: Drop `relay_whitelist` view from store schema

**Files:**
- Modify: `internal/store/schema.go`
- Modify: `internal/store/migrations` (or schema.go's migration block — wherever schema versions are tracked)

- [ ] **Step 1: Remove the view definition from `schema.go`.**

```go
// Delete this block:
//
// CREATE VIEW IF NOT EXISTS relay_whitelist AS
//   SELECT pubkey FROM contacts WHERE tier != 'blocked'
//   UNION
//   SELECT value AS pubkey FROM meta WHERE key = 'owner_pubkey';
```

- [ ] **Step 2: Add a schema migration that drops the view on existing dbs.**

Locate the existing migration mechanism (`SchemaVersion` const + `schemaV2` etc. in `schema.go`). Bump to `SchemaVersion = 3` (or current+1) with:

```go
const schemaV3 = `
DROP VIEW IF EXISTS relay_whitelist;
`
```

Wire it into the migration runner.

- [ ] **Step 3: Build + test.**

```sh
go build ./...
go test ./internal/store/...
```

- [ ] **Step 4: Commit.**

```sh
git add internal/store/
git commit -m "refactor(store): drop relay_whitelist view

The view was consumed only by relayd's WhitelistSource (removed in
Task 3). DROP VIEW IF EXISTS keeps the migration idempotent for
fresh and upgraded dbs alike."
```

---

## Task 12: Update SPEC.md, EXAMPLE.md, README.md, CHANGELOG.md

**Files:**
- Modify: `SPEC.md`
- Modify: `EXAMPLE.md`
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: SPEC.md edits.** Around line 137-141:

  - Replace "自托管本地 relay（推荐）" framing. New paragraph:
    > MindGate 与 relay 部署解耦。三种部署形态都是一等公民：
    > - **Gate-only**：本机只跑 daemon，使用外部 relay（公共 / 共享 / 第三方）。无公网 IP / 移动端 / 笔记本的常态。
    > - **Gate + 自托管 relay**：本机或同信任域内跑 daemon 与 relay 两个独立进程。要求 relay 节点对发送方可达（公网 IP、端口转发、隧道、或共享私网）。个人 inbox 主权部署。
    > - **Relay-only**：单独的 relay 运维节点，不参与社交图谱。社区 / 共享基础设施。
    >
    > 不可达的 relay（既不在公网也不在共享私网中）没有意义——relay 的本质就是消息存储与转发节点。

  - Add a new paragraph clarifying relay-not-an-entity:
    > **Relay 没有"实体身份"。** 网络中的实体身份属于 mind-form 与人类的 MindGate；relay 是基础设施。NIP-11 信息文档可附带管理员联系 `pubkey`，那是管理元数据，不是网络参与身份。Relay 不需要持有 keypair 才能工作。

  - Update line 145 about whitelist:
    > **Client-side 白名单是唯一的社交图谱过滤层。** v0.6 起，relay-side 的 publisher 白名单已被移除——它本就是可选的 defense-in-depth，且在多 relay 拓扑下无法兜底（发送方只要走任一别的 relay 就能绕过）。Paired-mode 仍然在 relay 边界过滤"不是 kind:1059 / 不是发给 owner"的事件，做存储/带宽兜底。

  - Add a new short subsection on persistence:
    > **Relay 持久化。** 自托管 relay 使用 sqlite 持久化事件（`~/.config/eidos/relay/events.db`），由 `github.com/fiatjaf/eventstore/sqlite3` 提供。当前无 TTL / 配额——运维者按需 `rm events.db`。Per-kind / 按时间淘汰策略是后续迭代。

- [ ] **Step 2: EXAMPLE.md edits.** Replace any `eidos gate relay` invocations with the new commands. Add a "Relay-only host" subsection if EXAMPLE.md walks through deployment topologies.

- [ ] **Step 3: README.md edits.** Update quick start if it mentions starting the relay; point at `eidos relay …`.

- [ ] **Step 4: CHANGELOG.md.** Under the upcoming release / Unreleased:

```
### Breaking changes

- `eidos gate relay` removed. The relay is now a top-level subcommand:
  `eidos relay`. See migration recipe below.
- `[relay]` section in gate's config.toml is no longer read; remove it.
- `eidos gate init` flags `--with-local-relay` and `--listen` removed.
- `eidos-gate-relay` systemd / launchd unit renamed to `eidos-relay`.

### Migration

To upgrade an existing local-relay install:

```sh
systemctl --user stop eidos-gate-relay     # or launchctl bootout / sc stop on other OS
systemctl --user disable eidos-gate-relay
eidos gate service install                 # re-runs without the relay unit
eidos relay init --mode <paired|public> --listen <host:port> [--owner <npub>]
eidos relay service install
eidos relay service start
```

Then remove the `[relay]` section from `~/.config/eidos/config.toml`.

### Added

- `eidos relay` subcommand tree: init, start, status, config get/set,
  service install/start/stop/status/uninstall.
- Relay event persistence via sqlite (`fiatjaf/eventstore`).
```

- [ ] **Step 5: Commit.**

```sh
git add SPEC.md EXAMPLE.md README.md CHANGELOG.md
git commit -m "docs: SPEC, EXAMPLE, README, CHANGELOG for top-level relay

- SPEC: three-role topology, relay-not-an-entity, persistence note,
  whitelist removal note.
- EXAMPLE: relay deployment commands updated.
- README: quick start updated.
- CHANGELOG: BREAKING changes block + migration recipe."
```

---

## Task 13: Final verification

- [ ] **Step 1: Full lint, vet, test.**

```sh
gofmt -l .
go vet ./...
go test ./...
```

Expected: gofmt prints nothing; vet exits 0; all tests pass.

- [ ] **Step 2: Integration smoke.** Two-host topology (or two config dirs on one host):

```sh
# Relay-only "host"
mkdir -p /tmp/relay-host
go run ./cmd/eidos relay init --dir /tmp/relay-host --mode public --listen 127.0.0.1:9003
go run ./cmd/eidos relay start --dir /tmp/relay-host &
RELAY_PID=$!
sleep 1

# Gate "host" — points at the relay
mkdir -p /tmp/gate-host
EIDOS_CONFIG_DIR=/tmp/gate-host go run ./cmd/eidos gate init --label test --home ws://127.0.0.1:9003
EIDOS_CONFIG_DIR=/tmp/gate-host go run ./cmd/eidos gate whoami
# (publish + subscribe smoke as appropriate to your test harness)

kill $RELAY_PID
go run ./cmd/eidos relay status --dir /tmp/relay-host
```

(`EIDOS_CONFIG_DIR` is illustrative — use whatever env var or flag the gate already honours; substitute in `--config-dir` if that's the supported pattern.)

Expected: gate-init succeeds without `--with-local-relay`. Relay status shows mode=public, listen=127.0.0.1:9003, events=N.

- [ ] **Step 3: Codex code review** (per CLAUDE.md `:93`):

```sh
codex --task "Review the diff on this branch against docs/superpowers/specs/2026-05-09-relay-top-level-design.md"
```

Address any high-confidence findings; commit fixes.

- [ ] **Step 4: Push branch and open PR.**

```sh
git push -u origin <feature-branch>
gh pr create --title "feat(relay): lift to top-level subcommand with state decoupling" \
  --body "$(cat <<'EOF'
## Summary

- `eidos relay` is now a top-level subcommand. `eidos gate relay` is removed.
- Relay state is decoupled: separate config dir, no shared sqlite with gate, no publisher whitelist.
- Sqlite event persistence via fiatjaf/eventstore.
- Service unit renamed (`eidos-gate-relay` → `eidos-relay`), gate and relay install paths split.
- SPEC.md, EXAMPLE.md, README.md, CHANGELOG.md updated. Pre-1.0 BREAKING.

## Test plan

- [x] `go test ./...` green
- [x] `gofmt -l .` clean, `go vet ./...` clean
- [x] Smoke: relay-only host runs `relay init && relay start`; remote gate publishes and re-reads after relay restart (persistence works)
- [x] Smoke: residual `[relay]` section in gate config produces warning, not error
- [ ] CI green
- [ ] Copilot review addressed (if assigned)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Watch CI.**

```sh
gh pr checks --watch
```

Address any failures; commit fixes; re-watch.

- [ ] **Step 6: Address Copilot review comments** (if assigned automatically); commit fixes.

---

## Self-review notes

Spec coverage:
- Spec §3 (three-role topology) → reflected in Task 12 SPEC update; CLI surface in Tasks 4-9 enables it.
- Spec §4 (CLI surface) → Tasks 4-9 cover every added command; Tasks 9-10 cover removed.
- Spec §5 (config and state layout) → Task 1 (relaycfg dir + paths), Task 2 (events.db).
- Spec §6 (code organization) → Tasks 1-9.
- Spec §7 (persistence) → Task 2.
- Spec §8 (migration) → Task 12 (CHANGELOG); Task 10 step 5 (warning on residual `[relay]`).
- Spec §9 (SPEC.md changes) → Task 12.
- Spec §10 (testing) → Tasks 1, 2, 3, 4 (test cases inline); Task 13 (integration smoke).
- Spec §11 (out of scope) → out-of-scope items are explicitly not present.

No placeholders remain. Type / method names checked for consistency: `runInit`, `initOpts`, `relaycfg.Config`, `relaycfg.Save/Load/Defaults/EventStorePath/DefaultDir`, `relayd.OpenSQLiteStore`, `relayd.Config.EventStorePath`, service interface split (`InstallDaemon` / `InstallRelay` / etc.).
