# MindGate Daemon / Relay Decoupling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the embedded relay opt-in instead of mandatory, enable the three home-relay topologies (public Nostr, self-hosted, shared), and clean-break v0.4 state directories.

**Architecture:** Single binary, logical decoupling only. New `relay.enabled` boolean controls whether the relay process is part of this install; daemon already treats every URL in `own_relays` uniformly so no protocol-layer change is needed. v0.4 state directories detected via BurntSushi/toml `MetaData.IsDefined` and rejected at the gate root command.

**Tech Stack:** Go 1.x; cobra for CLI; BurntSushi/toml for config; systemd / launchd via `internal/service/`; khatru-based relay (unchanged); SQLite via existing `internal/store/`.

**Spec reference:** `docs/superpowers/specs/2026-05-07-mindgate-relay-decoupling-design.md`

---

## File mapping

**Created:**
- `cmd/eidos/gate/v04detect.go` — v0.4 state-dir detection helper
- `cmd/eidos/gate/v04detect_test.go` — table tests for detection
- `cmd/eidos/gate/init_test.go` — init flag matrix + state writing
- `test/integration/daemon_only_test.go` — daemon without local relay sends/receives via remote relay

**Modified:**
- `internal/config/config.go` — add `Relay.Enabled` + `RelayEnabled()`, drop `Relay.PublicURL`, change `Defaults()`
- `internal/config/config_test.go` — defaults and toml round-trip assertions
- `cmd/eidos/gate/config.go` — `configKeys` map: add `relay.enabled`, drop `relay.public_url`
- `cmd/eidos/gate/config_test.go` — keys map updated
- `internal/service/service.go` — add `WithRelay bool` to `Config`
- `internal/service/systemd_linux.go` — branch `Install` and `Start` on `cfg.WithRelay`
- `internal/service/launchd_darwin.go` — same
- `internal/service/service_test.go` — assert default and round-trip
- `internal/service/launchd_test.go` — install / start matrix on WithRelay
- `cmd/eidos/gate/start.go` — wire `WithRelay = cfg.RelayEnabled()`; skip preflight when disabled
- `cmd/eidos/gate/relay.go` — refuse when `cfg.RelayEnabled() == false`
- `cmd/eidos/gate/preflight.go` — skip when disabled (also covered by `start.go` change but defensive)
- `cmd/eidos/gate/preflight_test.go` — keep existing coverage
- `cmd/eidos/gate/purge.go` — set own `PreRunE` to opt out of v0.4 detection
- `cmd/eidos/gate/init.go` — `--home` required, `--with-local-relay` flag, `--listen` validation, write logic
- `cmd/eidos/gate/root.go` — register `PersistentPreRunE`
- `README.md`, `docs/USAGE.md`, `docs/INSTALL.md`, `EXAMPLE.md` — rewrite per spec §8

**Not modified:** `internal/daemon/`, `internal/relayd/`, `internal/nostr/`, `internal/invite/`, `internal/card/`, `internal/contacts/`, `internal/inbox/`, `internal/envelope/`, `cmd/eidos/forge/`, `cmd/eidos/supervisor/`. Protocol layer is already topology-agnostic.

---

## Task 1: Add `Relay.Enabled` + `RelayEnabled()`, drop `Relay.PublicURL`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Replace `internal/config/config_test.go` contents (or add to existing) with:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsRelayDisabled(t *testing.T) {
	cfg := Defaults()
	if cfg.Relay.Enabled {
		t.Errorf("Defaults().Relay.Enabled = true, want false")
	}
	if cfg.Relay.Listen != "0.0.0.0:22895" {
		t.Errorf("Defaults().Relay.Listen = %q, want 0.0.0.0:22895", cfg.Relay.Listen)
	}
	if cfg.Relay.Mode != "paired" {
		t.Errorf("Defaults().Relay.Mode = %q, want paired", cfg.Relay.Mode)
	}
}

func TestRelayEnabledHelper(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{"explicit true", true, true},
		{"explicit false", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Relay: RelayConfig{Enabled: tc.enabled}}
			if c.RelayEnabled() != tc.want {
				t.Errorf("RelayEnabled() = %v, want %v", c.RelayEnabled(), tc.want)
			}
		})
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg := Defaults()
	cfg.LogLevel = "debug"
	cfg.Relay.Enabled = true
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", loaded.LogLevel)
	}
	if !loaded.Relay.Enabled {
		t.Fatalf("Relay.Enabled = false after roundtrip")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run failing tests**

```
go test ./internal/config/...
```

Expected: build failures (`Relay.Enabled` undefined; `RelayEnabled` undefined), or assertion failures.

- [ ] **Step 3: Implement the change in `internal/config/config.go`**

Replace the `RelayConfig` struct and `Defaults()` body with:

```go
type RelayConfig struct {
	Enabled bool   `toml:"enabled"`
	Mode    string `toml:"mode"`
	Listen  string `toml:"listen"`
	DataDir string `toml:"data_dir"`
}
```

Update `Defaults()`:

```go
func Defaults() Config {
	return Config{
		LogLevel: "info",
		Daemon: DaemonConfig{
			Socket:               "sock",
			ShutdownGraceSeconds: 5,
		},
		Relay: RelayConfig{
			Enabled: false,
			Mode:    "paired",
			Listen:  "0.0.0.0:22895",
			DataDir: "relay",
		},
	}
}
```

Add the helper at end of file:

```go
// RelayEnabled reports whether the embedded relay should run on this host.
// Single source of truth: every code path that asks "should I spin up the
// relay" routes through this method.
func (c Config) RelayEnabled() bool { return c.Relay.Enabled }
```

- [ ] **Step 4: Run tests**

```
go test ./internal/config/...
```

Expected: PASS.

- [ ] **Step 5: Verify `relay.public_url` removal is consistent**

```
go build ./...
```

Expected: build errors in places that referenced `Relay.PublicURL` (will be `cmd/eidos/gate/init.go`, `cmd/eidos/gate/config.go`, `cmd/eidos/gate/preflight.go`). Note them — they are addressed by Tasks 2, 8, and possibly 4.

The errors are expected and resolved later; do not fix them yet to keep this task's diff focused. Skip the build-error gate temporarily by tagging this commit's CI as a partial step, but in practice each task ends with a green `go build ./...`. Therefore stage this commit only after Task 2 lands. **Move to Task 2 immediately and commit Task 1 + Task 2 together.**

- [ ] **Step 6: Hold on commit until Task 2 is complete (combined commit)**

---

## Task 2: Update CLI `configKeys` map (drop `relay.public_url`, add `relay.enabled`)

**Files:**
- Modify: `cmd/eidos/gate/config.go`
- Test: `cmd/eidos/gate/config_test.go`

- [ ] **Step 1: Inspect current `configKeys` and identify the entry to remove**

```
grep -n "relay\.public_url\|relay\.listen\|relay\.mode" cmd/eidos/gate/config.go
```

The map key `"relay.public_url"` and its accessor closure must go. A new entry `"relay.enabled"` must be added.

- [ ] **Step 2: Edit `cmd/eidos/gate/config.go`**

Find the `configKeys` map (around the existing `relay.public_url` entry). Remove the `"relay.public_url"` block. Add a new entry alongside `relay.listen`:

```go
"relay.enabled": {
    get: func(c *config.Config) string {
        if c.Relay.Enabled {
            return "true"
        }
        return "false"
    },
    set: func(c *config.Config, v string) error {
        switch strings.ToLower(strings.TrimSpace(v)) {
        case "true", "1", "yes", "on":
            c.Relay.Enabled = true
        case "false", "0", "no", "off":
            c.Relay.Enabled = false
        default:
            return fmt.Errorf(`relay.enabled must be true or false, got %q`, v)
        }
        return nil
    },
},
```

If `strings` and `fmt` are not yet imported in this file, ensure they are.

Also tighten the `relay.listen` setter to reject empty values (per spec §4.7). Locate the existing `relay.listen` set closure and prepend the empty-string check:

```go
set: func(c *config.Config, v string) error {
    v = strings.TrimSpace(v)
    if v == "" {
        return fmt.Errorf("relay.listen must be a non-empty host:port; to disable the local relay, set relay.enabled = false instead")
    }
    if _, _, err := net.SplitHostPort(v); err != nil {
        return fmt.Errorf("relay.listen %q must be host:port: %w", v, err)
    }
    c.Relay.Listen = v
    return nil
},
```

Ensure `net` is imported.

- [ ] **Step 3: Update `cmd/eidos/gate/config_test.go`**

Replace any test referencing `relay.public_url` with the analogous `relay.enabled`. Add a test that `relay.listen = ""` errors:

```go
func TestConfigKeysRelayEnabled(t *testing.T) {
    cfg := config.Defaults()
    k := configKeys["relay.enabled"]
    if k.get == nil || k.set == nil {
        t.Fatal("relay.enabled key missing")
    }
    if err := k.set(&cfg, "true"); err != nil {
        t.Fatalf("set true: %v", err)
    }
    if got := k.get(&cfg); got != "true" {
        t.Fatalf("get = %q after set true", got)
    }
    if err := k.set(&cfg, "false"); err != nil {
        t.Fatalf("set false: %v", err)
    }
    if got := k.get(&cfg); got != "false" {
        t.Fatalf("get = %q after set false", got)
    }
    if err := k.set(&cfg, "garbage"); err == nil {
        t.Fatal("set garbage: expected error, got nil")
    }
}

func TestConfigKeysRelayPublicURLRemoved(t *testing.T) {
    if _, ok := configKeys["relay.public_url"]; ok {
        t.Fatal("relay.public_url should have been removed")
    }
}

func TestConfigKeysRelayListenRejectsEmpty(t *testing.T) {
    cfg := config.Defaults()
    k := configKeys["relay.listen"]
    if err := k.set(&cfg, ""); err == nil {
        t.Fatal("expected error for empty relay.listen, got nil")
    }
}
```

Replace existing `TestConfigKeysRelayPublicURL`-style tests with these.

- [ ] **Step 4: Run tests**

```
go test ./cmd/eidos/gate/... -run TestConfigKeys
go build ./...
```

Expected: tests PASS; build now passes for this and the previous task.

- [ ] **Step 5: Commit Tasks 1+2 together**

```
git add internal/config/config.go internal/config/config_test.go cmd/eidos/gate/config.go cmd/eidos/gate/config_test.go
git commit -m "$(cat <<'EOF'
feat(config)!: replace relay.public_url with relay.enabled bool

The embedded relay becomes opt-in. relay.enabled (bool) is the single
source of truth for whether the relay should run on this host;
relay.public_url was vestigial (never read by code) and is removed.
relay.listen now rejects empty values at set time — disabling the
relay goes through relay.enabled = false.

BREAKING CHANGE: config.toml schema. v0.4 state directories are
detected and rejected by the gate root command (separate task).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Add `WithRelay bool` to `service.Config`

**Files:**
- Modify: `internal/service/service.go`
- Test: `internal/service/service_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/service/service_test.go`:

```go
func TestConfigWithRelayDefault(t *testing.T) {
    cfg := Config{}
    if cfg.WithRelay {
        t.Errorf("zero-value Config.WithRelay = true, want false")
    }
}
```

- [ ] **Step 2: Run failing test**

```
go test ./internal/service/...
```

Expected: build error (`Config.WithRelay undefined`).

- [ ] **Step 3: Edit `internal/service/service.go`**

Add the field to `Config`:

```go
type Config struct {
    BinaryPath string
    StateDir   string
    Scope      Scope
    UnitDir    string
    // WithRelay controls whether Install / Start manage the relay unit.
    // false (default): only the daemon unit is managed; any residual relay
    // unit on disk is left untouched. true: both units are managed.
    WithRelay  bool
}
```

- [ ] **Step 4: Run test**

```
go test ./internal/service/... -run TestConfigWithRelay
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): add WithRelay flag to Config

Relay unit installation becomes conditional. Default (false) keeps the
daemon-only deployment; true preserves the v0.4 behavior of installing
both daemon and relay units.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Branch systemd Install / Start on `WithRelay`

**Files:**
- Modify: `internal/service/systemd_linux.go`
- Test: existing systemd integration / unit tests if present; otherwise covered by smoke run.

- [ ] **Step 1: Edit `internal/service/systemd_linux.go` — `Install` method**

Locate `func (s *systemd) Install(ctx context.Context) error` and gate the relay-unit write:

```go
func (s *systemd) Install(ctx context.Context) error {
    dir, err := s.unitDir()
    if err != nil {
        return err
    }
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return fmt.Errorf("create unit dir %s: %w", dir, err)
    }
    if err := writeUnitIfChanged(filepath.Join(dir, DaemonUnitName+".service"), s.daemonUnit()); err != nil {
        return err
    }
    if s.cfg.WithRelay {
        if err := writeUnitIfChanged(filepath.Join(dir, RelayUnitName+".service"), s.relayUnit()); err != nil {
            return err
        }
    }
    if out, err := s.systemctl(ctx, "daemon-reload"); err != nil {
        return fmt.Errorf("systemctl daemon-reload: %w (output: %s)", err, strings.TrimSpace(string(out)))
    }
    return nil
}
```

- [ ] **Step 2: Edit `Start` method**

```go
func (s *systemd) Start(ctx context.Context) error {
    if err := s.Install(ctx); err != nil {
        return err
    }
    units := []string{DaemonUnitName}
    if s.cfg.WithRelay {
        units = append(units, RelayUnitName)
    }
    args := append([]string{"enable", "--now"}, units...)
    out, err := s.systemctl(ctx, args...)
    if err != nil {
        return fmt.Errorf("systemctl enable --now: %w (output: %s)", err, strings.TrimSpace(string(out)))
    }
    return nil
}
```

- [ ] **Step 3: Confirm `Stop`, `Uninstall`, `Status` are unchanged**

`Stop` and `Uninstall` already iterate both names regardless and tolerate missing units (per existing code review at `internal/service/systemd_linux.go:90-105`). `Status` already reports both names with `Installed: false` when the unit file is missing — that's exactly the spec §4.3 behavior.

- [ ] **Step 4: Build + test**

```
go build ./...
go test ./internal/service/...
```

Expected: build OK; existing tests pass.

- [ ] **Step 5: Commit**

```
git add internal/service/systemd_linux.go
git commit -m "feat(service): branch systemd Install/Start on WithRelay

When WithRelay is false, Install only writes the daemon unit and Start
only enables/activates the daemon. Stop, Uninstall, and Status keep
iterating both names so residual units are always reachable for cleanup.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Branch launchd Install / Start on `WithRelay`

**Files:**
- Modify: `internal/service/launchd_darwin.go`
- Test: existing `internal/service/launchd_test.go`

- [ ] **Step 1: Inspect launchd Install and Start**

```
grep -n "func (l \*launchd) Install\|func (l \*launchd) Start" /data/eidopsyche/internal/service/launchd_darwin.go
```

Locate the corresponding methods and the slice-of-units passed to `launchctl bootstrap` / `launchctl bootout`.

- [ ] **Step 2: Edit `Install` (or equivalent — write plist files)**

Inside the existing per-unit loop (look for the `[]struct{ label string; args []string }` or similar — line ~193 has it), branch on `l.cfg.WithRelay` to skip the relay entry:

```go
units := []struct {
    label string
    args  []string
}{
    {label: DaemonUnitName, args: []string{"gate", "daemon"}},
}
if l.cfg.WithRelay {
    units = append(units, struct {
        label string
        args  []string
    }{label: RelayUnitName, args: []string{"gate", "relay"}})
}
```

If the existing code uses a slightly different shape, preserve it; the principle is "skip the relay entry when WithRelay=false".

- [ ] **Step 3: Edit `Start` similarly**

If `Start` issues `launchctl load` per unit, gate the relay one on `l.cfg.WithRelay`.

- [ ] **Step 4: Update `internal/service/launchd_test.go`**

Add a test variant that builds `Config{WithRelay: false}` and verifies the daemon plist is written but the relay plist is not. If the test uses a fake `launchctl`, assert it was called only with the daemon label.

```go
func TestLaunchdInstallDaemonOnly(t *testing.T) {
    if runtime.GOOS != "darwin" {
        t.Skip("darwin-only test")
    }
    dir := t.TempDir()
    cfg := Config{
        BinaryPath: "/usr/local/bin/eidos",
        StateDir:   dir,
        Scope:      ScopeUser,
        UnitDir:    dir,
        WithRelay:  false,
    }
    mgr, err := New(cfg)
    if err != nil {
        t.Fatal(err)
    }
    // Install with WithRelay=false should write only the daemon plist.
    if err := mgr.Install(context.Background()); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(filepath.Join(dir, DaemonUnitName+".plist")); err != nil {
        t.Fatalf("daemon plist not written: %v", err)
    }
    if _, err := os.Stat(filepath.Join(dir, RelayUnitName+".plist")); !os.IsNotExist(err) {
        t.Fatalf("relay plist exists when WithRelay=false; err=%v", err)
    }
}
```

(Adapt assertions to match how the existing tests interact with the fake launchctl.)

- [ ] **Step 5: Build + test on Linux (launchd code is darwin-build-tagged)**

```
go build ./...
go vet ./...
```

Linux CI cannot exercise launchd directly; the test from Step 4 is gated by `runtime.GOOS != "darwin"`. The launchd compile path runs only on macOS.

- [ ] **Step 6: Commit**

```
git add internal/service/launchd_darwin.go internal/service/launchd_test.go
git commit -m "feat(service): branch launchd Install/Start on WithRelay

Mirror the systemd behavior: WithRelay=false means only the daemon plist
is written and only the daemon is loaded; residual relay plists are left
in place for manual or purge cleanup.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Wire `cfg.RelayEnabled()` into `eidos gate start`

**Files:**
- Modify: `cmd/eidos/gate/start.go`
- Modify: `cmd/eidos/gate/service.go` (extend `buildServiceManager` signature OR add a helper that takes config)

- [ ] **Step 1: Decide on signature**

`buildServiceManager` currently constructs `service.Config` without `WithRelay`. The cleanest change is to add a parameter:

```go
func buildServiceManager(withRelay bool) (service.Manager, error) {
    // ... existing body, with WithRelay: withRelay added to service.Config
}
```

All four callers (`start.go`, `stop.go`, `status.go`, `purge.go`) need updating, but only `start` has a meaningful value derived from config. The other three can pass `false` because Stop/Status/Uninstall already iterate both names regardless of `WithRelay`.

To make this trade-off explicit, add a small helper for the start path:

```go
// buildServiceManagerForStart loads config and constructs a Manager that
// Install/Start will gate on cfg.RelayEnabled(). For Stop/Status/Purge,
// use buildServiceManager(false) — those methods iterate both unit names
// regardless of WithRelay.
func buildServiceManagerForStart() (service.Manager, error) {
    stateDir, err := config.ResolveStateDir(globalStateDir)
    if err != nil {
        return nil, err
    }
    cfg, _ := config.Load(filepath.Join(stateDir, "config.toml"))
    return buildServiceManager(cfg.RelayEnabled())
}
```

- [ ] **Step 2: Edit `cmd/eidos/gate/service.go`**

Change `buildServiceManager` to accept `withRelay bool` and add it to the `service.Config` literal. Add `buildServiceManagerForStart` per Step 1. Update existing callers in `stop.go`, `status.go`, `purge.go` to call `buildServiceManager(false)`.

- [ ] **Step 3: Edit `cmd/eidos/gate/start.go`**

Replace the existing `mgr, err := buildServiceManager()` with `mgr, err := buildServiceManagerForStart()`. Also gate the preflight on `cfg.RelayEnabled()`:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    mgr, err := buildServiceManagerForStart()
    if err != nil {
        return err
    }
    ctx := context.Background()

    stateDir, err := config.ResolveStateDir(globalStateDir)
    if err != nil {
        return err
    }
    cfg, _ := config.Load(filepath.Join(stateDir, "config.toml"))

    if cfg.RelayEnabled() && !relayAlreadyManaged(ctx, mgr) {
        if err := preflightRelayPort(stateDir); err != nil {
            return err
        }
    }

    if err := mgr.Start(ctx); err != nil {
        return err
    }
    fmt.Println("✓ gate services started")
    return printStatus(ctx, os.Stdout, mgr)
},
```

Ensure `path/filepath` import is present.

- [ ] **Step 4: Build + test**

```
go build ./...
go vet ./...
go test ./cmd/eidos/gate/...
```

Expected: PASS. (No new test added in this task; integration test in Task 11 is the high-confidence gate.)

- [ ] **Step 5: Commit**

```
git add cmd/eidos/gate/start.go cmd/eidos/gate/service.go cmd/eidos/gate/stop.go cmd/eidos/gate/status.go cmd/eidos/gate/purge.go
git commit -m "feat(gate): branch 'start' on relay.enabled

start now reads relay.enabled from config and constructs the service
manager with WithRelay set accordingly. Preflight is also gated on the
relay being enabled so the daemon-only path doesn't try to bind a port
it never wanted. Stop/Status/Purge keep WithRelay=false and continue
iterating both unit names so residuals stay visible and removable.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: `eidos gate relay` foreground refuses when disabled

**Files:**
- Modify: `cmd/eidos/gate/relay.go`

- [ ] **Step 1: Edit `cmd/eidos/gate/relay.go`**

At the top of the `RunE` body (right after loading config), add the guard:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    dir, err := config.ResolveStateDir(globalStateDir)
    if err != nil {
        return err
    }
    cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
    if !cfg.RelayEnabled() {
        return fmt.Errorf(`local relay is disabled (relay.enabled = false in config.toml).
to enable: eidos gate config set relay.enabled true && eidos gate config set relay.listen <host:port>`)
    }
    // ... rest of existing body unchanged
},
```

Ensure `fmt` is imported.

- [ ] **Step 2: Build + manual smoke**

```
go build -o /tmp/eidos-test ./cmd/eidos
EIDOS_GATE_HOME=/tmp/test-disabled mkdir -p /tmp/test-disabled
cat > /tmp/test-disabled/config.toml <<'EOF'
[relay]
  enabled = false
  listen = "0.0.0.0:22895"
  mode = "paired"
  data_dir = "relay"
EOF
EIDOS_GATE_HOME=/tmp/test-disabled /tmp/eidos-test gate relay 2>&1 | head
```

Expected: prints the refusal message; exits non-zero.

- [ ] **Step 3: Commit**

```
git add cmd/eidos/gate/relay.go
git commit -m "feat(gate): refuse 'gate relay' when relay.enabled=false

Prevent the foreground command from drifting from the unit's behavior.
Users who want to run the relay must opt in explicitly via config.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: v0.4 state-dir detection

**Files:**
- Create: `cmd/eidos/gate/v04detect.go`
- Create: `cmd/eidos/gate/v04detect_test.go`
- Modify: `cmd/eidos/gate/root.go`
- Modify: `cmd/eidos/gate/purge.go` (opt-out)
- Modify: `internal/config/config.go` (expose a Load helper that returns MetaData, OR re-parse here)

- [ ] **Step 1: Add a Load variant in `internal/config/config.go` that exposes whether a key was defined**

Append:

```go
// LoadWithMeta is Load + the BurntSushi/toml MetaData so callers can
// distinguish "field absent" from "field present with zero value". Used by
// v0.4 state-directory detection in cmd/eidos/gate.
func LoadWithMeta(path string) (Config, toml.MetaData, error) {
    cfg := Defaults()
    meta, err := toml.DecodeFile(path, &cfg)
    return cfg, meta, err
}
```

(`toml` is already imported.)

- [ ] **Step 2: Create `cmd/eidos/gate/v04detect.go`**

```go
package gate

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"

    "github.com/LucianoXu/eidopsyche/internal/config"
)

// detectV04State returns a non-nil error when stateDir contains a
// config.toml that lacks the [relay].enabled field — the unambiguous
// signal of a v0.4-or-earlier state directory. Returns nil when the file
// is missing (fresh install) or when [relay].enabled is present (v0.5+).
func detectV04State(stateDir string) error {
    path := filepath.Join(stateDir, "config.toml")
    if _, err := os.Stat(path); err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil
        }
        return err
    }
    _, meta, err := config.LoadWithMeta(path)
    if err != nil {
        return fmt.Errorf("read config.toml: %w", err)
    }
    if meta.IsDefined("relay", "enabled") {
        return nil
    }
    return fmt.Errorf(`this state directory was created by an older eidos version (pre-v0.5).

v0.5 changes how the gate is initialized: the embedded relay is now opt-in,
and 'eidos gate init' requires --home <url>.

Choose one:
  (A) Re-init from scratch (loses contacts, invites, inbox history):
        eidos gate purge --yes
        eidos gate init --label <your-label> --home <url> [--with-local-relay]

  (B) Migrate in place (keeps state):
        See docs/INSTALL.md#migrating-from-v04 for the SQL + config recipe.

state directory: %s`, stateDir)
}
```

- [ ] **Step 3: Wire detection into `cmd/eidos/gate/root.go`**

Edit `rootCmd` to set a `PersistentPreRunE`:

```go
var rootCmd = &cobra.Command{
    Use:           "gate",
    Short:         "MindGate — Eidopsyche identity and communication CLI",
    SilenceUsage:  true,
    SilenceErrors: true,
    PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
        // Per-subcommand opt-out: a subcommand that sets its own PreRunE
        // returning nil before us will already have its detection skipped
        // because cobra wires PersistentPreRunE only when the subcommand's
        // PreRunE is unset. To skip cleanly, the subcommand sets its own
        // PreRunE that explicitly calls back to this hook (or doesn't).
        // Purge opts out; see purge.go.
        stateDir, err := config.ResolveStateDir(globalStateDir)
        if err != nil {
            return err
        }
        return detectV04State(stateDir)
    },
}
```

Ensure `config` is imported in this file.

**Subtle cobra behavior:** when a child sets its own `PreRunE`, cobra still runs the parent's `PersistentPreRunE`. The clean way to opt out is: child sets `PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil }` to override. We do that for purge in Task 9.

- [ ] **Step 4: Create `cmd/eidos/gate/v04detect_test.go`**

```go
package gate

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func writeConfig(t *testing.T, body string) string {
    t.Helper()
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
        t.Fatal(err)
    }
    return dir
}

func TestDetectV04_NoConfig_ReturnsNil(t *testing.T) {
    dir := t.TempDir() // no config.toml inside
    if err := detectV04State(dir); err != nil {
        t.Fatalf("expected nil for missing config, got %v", err)
    }
}

func TestDetectV04_LegacyConfig_ReturnsError(t *testing.T) {
    dir := writeConfig(t, `
log_level = "info"
[relay]
  mode = "paired"
  listen = "127.0.0.1:22895"
  public_url = "ws://127.0.0.1:22895"
  data_dir = "relay"
`)
    err := detectV04State(dir)
    if err == nil {
        t.Fatal("expected error for v0.4 config, got nil")
    }
    if !strings.Contains(err.Error(), "older eidos version") {
        t.Fatalf("unexpected error message: %v", err)
    }
}

func TestDetectV04_V05Config_ReturnsNil(t *testing.T) {
    dir := writeConfig(t, `
log_level = "info"
[relay]
  enabled = false
  mode = "paired"
  listen = "0.0.0.0:22895"
  data_dir = "relay"
`)
    if err := detectV04State(dir); err != nil {
        t.Fatalf("expected nil for v0.5 config, got %v", err)
    }
}

func TestDetectV04_V05ConfigEnabledTrue_ReturnsNil(t *testing.T) {
    dir := writeConfig(t, `
log_level = "info"
[relay]
  enabled = true
  mode = "paired"
  listen = "127.0.0.1:22895"
  data_dir = "relay"
`)
    if err := detectV04State(dir); err != nil {
        t.Fatalf("expected nil for v0.5 config with enabled=true, got %v", err)
    }
}
```

- [ ] **Step 5: Run tests**

```
go test ./cmd/eidos/gate/... -run TestDetectV04
```

Expected: PASS.

- [ ] **Step 6: Build full project**

```
go build ./...
```

Expected: build OK.

- [ ] **Step 7: Commit**

```
git add internal/config/config.go cmd/eidos/gate/v04detect.go cmd/eidos/gate/v04detect_test.go cmd/eidos/gate/root.go
git commit -m "feat(gate): detect and reject v0.4 state directories

Adds a PersistentPreRunE on the gate root command that flags any state
dir whose config.toml lacks [relay].enabled — the unambiguous signal of
pre-v0.5 layout. Detection uses BurntSushi/toml MetaData.IsDefined
because Go bool zero-values cannot otherwise distinguish 'field missing'
from 'field present and false'. Error message points to two migration
paths in docs/INSTALL.md.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: `eidos gate purge` opt-out

**Files:**
- Modify: `cmd/eidos/gate/purge.go`

- [ ] **Step 1: Edit `cmd/eidos/gate/purge.go`**

Add `PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil }` to the `purgeCmd` definition. This overrides the parent's hook for this subcommand.

```go
var purgeCmd = &cobra.Command{
    Use:   "purge",
    Short: "Stop, uninstall, and remove all gate state",
    // Opt out of v0.4 detection — purge is precisely how a v0.4 user
    // cleans up so they can re-init.
    PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
    RunE: func(cmd *cobra.Command, args []string) error {
        // ... existing body unchanged
    },
}
```

- [ ] **Step 2: Add a regression test in `cmd/eidos/gate/v04detect_test.go`**

```go
func TestDetectV04_PurgeSkipsDetection(t *testing.T) {
    // Sanity: purgeCmd.PersistentPreRunE must be set (and non-nil) so
    // cobra uses it instead of the root's. Returning nil short-circuits
    // detection.
    if purgeCmd.PersistentPreRunE == nil {
        t.Fatal("purgeCmd.PersistentPreRunE is nil — v0.4 users cannot purge")
    }
    if err := purgeCmd.PersistentPreRunE(purgeCmd, nil); err != nil {
        t.Fatalf("purgeCmd opt-out PreRunE returned error: %v", err)
    }
}
```

- [ ] **Step 3: Run tests + build**

```
go test ./cmd/eidos/gate/... -run TestDetectV04
go build ./...
```

Expected: PASS / OK.

- [ ] **Step 4: Commit**

```
git add cmd/eidos/gate/purge.go cmd/eidos/gate/v04detect_test.go
git commit -m "feat(gate): purge opts out of v0.4 detection

Purge is precisely the path v0.4 users take to clean up before re-init,
so it cannot be blocked by detection. Sets its own PersistentPreRunE
that returns nil, overriding the root command's check.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: `init` flag matrix and write logic

**Files:**
- Modify: `cmd/eidos/gate/init.go`
- Create: `cmd/eidos/gate/init_test.go`

- [ ] **Step 1: Write failing tests in `cmd/eidos/gate/init_test.go`**

```go
package gate

import (
    "context"
    "database/sql"
    "os"
    "path/filepath"
    "strings"
    "testing"

    _ "modernc.org/sqlite"

    "github.com/LucianoXu/eidopsyche/internal/config"
)

// callInit is a thin helper that invokes runInit with controlled flag
// values. It returns whatever runInit returns and leaves state on disk
// for the caller to inspect.
func callInit(t *testing.T, dir, label, home string, withRelay bool, listen string) error {
    t.Helper()
    // Save package vars; restore on cleanup.
    savedLabel, savedHome, savedWithRelay, savedListen, savedStateDir := initLabel, initHome, initWithLocalRelay, initListen, globalStateDir
    t.Cleanup(func() {
        initLabel, initHome, initWithLocalRelay, initListen, globalStateDir = savedLabel, savedHome, savedWithRelay, savedListen, savedStateDir
    })
    initLabel = label
    initHome = home
    initWithLocalRelay = withRelay
    initListen = listen
    globalStateDir = dir
    return runInit(initCmd, nil)
}

func TestInit_HomeMissing_Errors(t *testing.T) {
    dir := t.TempDir()
    err := callInit(t, dir, "alice", "", false, "")
    if err == nil {
        t.Fatal("expected error when --home is missing")
    }
    if !strings.Contains(err.Error(), "--home is required") {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestInit_HomeBadScheme_Errors(t *testing.T) {
    dir := t.TempDir()
    err := callInit(t, dir, "alice", "http://relay.example.com", false, "")
    if err == nil {
        t.Fatal("expected error for non-ws scheme")
    }
    if !strings.Contains(err.Error(), "ws://") {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestInit_ListenWithoutWithLocalRelay_Errors(t *testing.T) {
    dir := t.TempDir()
    err := callInit(t, dir, "alice", "wss://relay.example.com", false, "0.0.0.0:9999")
    if err == nil {
        t.Fatal("expected error for --listen without --with-local-relay")
    }
    if !strings.Contains(err.Error(), "--listen requires --with-local-relay") {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestInit_DaemonOnly_WritesEnabledFalseAndHomeRow(t *testing.T) {
    dir := t.TempDir()
    if err := callInit(t, dir, "alice", "wss://relay.example.com", false, ""); err != nil {
        t.Fatalf("init: %v", err)
    }
    // config.toml: relay.enabled = false
    cfg, err := config.Load(filepath.Join(dir, "config.toml"))
    if err != nil {
        t.Fatal(err)
    }
    if cfg.Relay.Enabled {
        t.Errorf("Relay.Enabled = true, want false")
    }
    // own_relays: home row equals --home
    db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
    if err != nil {
        t.Fatal(err)
    }
    defer db.Close()
    var url, role string
    if err := db.QueryRowContext(context.Background(),
        `SELECT relay_url, role FROM own_relays`).Scan(&url, &role); err != nil {
        t.Fatal(err)
    }
    if url != "wss://relay.example.com" || role != "home" {
        t.Errorf("own_relays row = (%q, %q), want (wss://relay.example.com, home)", url, role)
    }
}

func TestInit_WithLocalRelay_DefaultsListen_WritesEnabledTrue(t *testing.T) {
    dir := t.TempDir()
    if err := callInit(t, dir, "alice", "ws://127.0.0.1:22895", true, ""); err != nil {
        t.Fatalf("init: %v", err)
    }
    cfg, err := config.Load(filepath.Join(dir, "config.toml"))
    if err != nil {
        t.Fatal(err)
    }
    if !cfg.Relay.Enabled {
        t.Errorf("Relay.Enabled = false, want true")
    }
    if cfg.Relay.Listen != "0.0.0.0:22895" {
        t.Errorf("Relay.Listen = %q, want 0.0.0.0:22895 (default)", cfg.Relay.Listen)
    }
}

func TestInit_WithLocalRelay_ExplicitListen(t *testing.T) {
    dir := t.TempDir()
    if err := callInit(t, dir, "alice", "wss://my.host", true, "127.0.0.1:9999"); err != nil {
        t.Fatalf("init: %v", err)
    }
    cfg, err := config.Load(filepath.Join(dir, "config.toml"))
    if err != nil {
        t.Fatal(err)
    }
    if cfg.Relay.Listen != "127.0.0.1:9999" {
        t.Errorf("Relay.Listen = %q, want 127.0.0.1:9999", cfg.Relay.Listen)
    }
    // own_relays row must equal --home, NOT --listen.
    db, _ := sql.Open("sqlite", filepath.Join(dir, "state.db"))
    defer db.Close()
    var url string
    db.QueryRowContext(context.Background(),
        `SELECT relay_url FROM own_relays WHERE role='home'`).Scan(&url)
    if url != "wss://my.host" {
        t.Errorf("home row url = %q, want wss://my.host", url)
    }
}

func TestInit_RefusesToOverwrite(t *testing.T) {
    dir := t.TempDir()
    if err := callInit(t, dir, "alice", "wss://r.com", false, ""); err != nil {
        t.Fatalf("first init: %v", err)
    }
    err := callInit(t, dir, "alice", "wss://r.com", false, "")
    if err == nil {
        t.Fatal("expected refusal on second init")
    }
    if !strings.Contains(err.Error(), "refusing to overwrite") {
        t.Fatalf("unexpected error: %v", err)
    }
    // Sanity: directory still has its key file.
    if _, err := os.Stat(filepath.Join(dir, "key")); err != nil {
        t.Fatal(err)
    }
}
```

- [ ] **Step 2: Run failing tests**

```
go test ./cmd/eidos/gate/... -run TestInit_
```

Expected: build error (`initHome`, `initWithLocalRelay` undefined), or test failures.

- [ ] **Step 3: Rewrite `cmd/eidos/gate/init.go`**

Replace the file's body with:

```go
package gate

import (
    "context"
    "errors"
    "fmt"
    "net"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "time"

    "github.com/spf13/cobra"

    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/identity"
    "github.com/LucianoXu/eidopsyche/internal/store"
    "github.com/LucianoXu/eidopsyche/internal/version"
)

var (
    initLabel          string
    initHome           string
    initWithLocalRelay bool
    initListen         string
)

var initCmd = &cobra.Command{
    Use:   "init",
    Short: "Initialize a MindGate state directory and identity",
    RunE:  runInit,
}

func init() {
    initCmd.Flags().StringVar(&initLabel, "label", "", "label for this identity (required; how others see your card by default)")
    initCmd.Flags().StringVar(&initHome, "home", "", "home relay URL (required; ws:// or wss://) — the URL peers will dial to reach you")
    initCmd.Flags().BoolVar(&initWithLocalRelay, "with-local-relay", false, "also run the embedded relay on this host")
    initCmd.Flags().StringVar(&initListen, "listen", "", "relay bind address (host:port); requires --with-local-relay; default 0.0.0.0:22895")
    if err := initCmd.MarkFlagRequired("label"); err != nil {
        panic(err)
    }
    rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
    // --home is required and must be ws:// or wss://.
    if initHome == "" {
        return errors.New("--home is required (the inbound relay URL peers will dial); see docs/USAGE.md for topology choices")
    }
    parsed, err := url.Parse(initHome)
    if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
        return fmt.Errorf("--home must start with ws:// or wss:// and include a host, got %q", initHome)
    }

    // --listen requires --with-local-relay.
    if initListen != "" && !initWithLocalRelay {
        return errors.New("--listen requires --with-local-relay")
    }

    // When --with-local-relay is set, validate or default --listen.
    listen := ""
    if initWithLocalRelay {
        listen = initListen
        if listen == "" {
            listen = "0.0.0.0:22895"
        }
        if _, _, err := net.SplitHostPort(listen); err != nil {
            return fmt.Errorf("invalid --listen value %q: must be host:port (%w)", listen, err)
        }
    }

    dir, err := config.ResolveStateDir(globalStateDir)
    if err != nil {
        return err
    }
    if err := os.MkdirAll(dir, 0o700); err != nil {
        return err
    }
    keyPath := filepath.Join(dir, "key")
    if _, err := os.Stat(keyPath); err == nil {
        return fmt.Errorf("refusing to overwrite existing key at %s", keyPath)
    } else if !errors.Is(err, os.ErrNotExist) {
        return err
    }

    k, err := identity.Generate()
    if err != nil {
        return err
    }
    if err := identity.SaveKey(keyPath, k); err != nil {
        return err
    }

    dbPath := filepath.Join(dir, "state.db")
    db, err := store.Open(dbPath, false)
    if err != nil {
        return err
    }
    defer db.Close()
    ctx := context.Background()
    if err := db.Migrate(ctx); err != nil {
        return err
    }
    if err := db.SetMeta(ctx, "owner_pubkey", k.PublicHex); err != nil {
        return err
    }
    if err := db.SetMeta(ctx, "created_at", strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
        return err
    }
    if err := db.SetMeta(ctx, "mindgate_version", version.Version); err != nil {
        return err
    }
    if err := db.SetMeta(ctx, "label", initLabel); err != nil {
        return err
    }

    // own_relays: home row URL = --home, never --listen.
    if _, err := db.ExecContext(ctx,
        `INSERT OR IGNORE INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
        initHome, "home", time.Now().Unix()); err != nil {
        return err
    }

    cfg := config.Defaults()
    cfg.Relay.Enabled = initWithLocalRelay
    if listen != "" {
        cfg.Relay.Listen = listen
    }
    if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
        return err
    }

    fmt.Printf("✓ created %s\n", dir)
    fmt.Printf("✓ generated keypair → %s (0600)\n", keyPath)
    fmt.Printf("✓ wrote state.db (schema v%d)\n", store.SchemaVersion)
    fmt.Printf("✓ wrote config.toml\n")
    fmt.Printf("  home relay: %s\n", initHome)
    if initWithLocalRelay {
        fmt.Printf("  local relay: %s (paired)\n", listen)
    } else {
        fmt.Printf("  local relay: disabled (run 'eidos gate config set relay.enabled true' to opt in)\n")
    }
    fmt.Println("\nyour identity:")
    fmt.Printf("  npub: %s\n", k.Npub)
    fmt.Printf("  hex:  %s\n", k.PublicHex)
    fmt.Println("\nnext steps:")
    fmt.Println("  1) start services: eidos gate start")
    fmt.Println("  2) share card:     eidos gate card")
    return nil
}
```

- [ ] **Step 4: Run tests**

```
go test ./cmd/eidos/gate/... -run TestInit_
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

```
go test ./...
```

Expected: PASS. (envelope/invite/loopback/selfheal integration tests bypass init and write own_relays directly, so they are unaffected.)

- [ ] **Step 6: Commit**

```
git add cmd/eidos/gate/init.go cmd/eidos/gate/init_test.go
git commit -m "feat(gate)!: init requires --home; --with-local-relay is opt-in

The embedded relay no longer ships by default. --home <url> is now
required and is the URL peers will dial to reach you (written to
own_relays(role=home)). --with-local-relay opts into the embedded relay
process; --listen sets its bind address and only makes sense with
--with-local-relay. --home and --listen are independent: a host can run
the local relay on 0.0.0.0:22895 while telling peers wss://my.host.

BREAKING: existing v0.4 invocations relying on the implicit local-relay
default must now pass --with-local-relay (or migrate per
docs/INSTALL.md#migrating-from-v04).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Integration test for daemon-only deployment

**Files:**
- Create: `test/integration/daemon_only_test.go`

- [ ] **Step 1: Inspect existing `bringUp` to copy structure**

```
sed -n '40,135p' test/integration/loopback_test.go
```

Note the helper signature, the use of `relayd.New`, the daemon `daemon.Start(dir).Run(ctx)` pattern, and the IPC dial via `dialIPC`.

- [ ] **Step 2: Create `test/integration/daemon_only_test.go`**

```go
//go:build integration

package integration

import (
    "context"
    "encoding/json"
    "fmt"
    "path/filepath"
    "testing"
    "time"

    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/contacts"
    "github.com/LucianoXu/eidopsyche/internal/daemon"
    "github.com/LucianoXu/eidopsyche/internal/identity"
    "github.com/LucianoXu/eidopsyche/internal/store"
)

// bringUpDaemonOnly creates a state dir + identity + daemon that points
// at *someone else's* relay URL (no local relay). Used to validate that
// the daemon-only topology delivered by v0.5 actually carries traffic.
func bringUpDaemonOnly(t *testing.T, name, homeRelayURL string) *instance {
    t.Helper()
    dir := t.TempDir()

    k, err := identity.Generate()
    if err != nil {
        t.Fatal(err)
    }
    if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
        t.Fatal(err)
    }

    db, err := store.Open(filepath.Join(dir, "state.db"), false)
    if err != nil {
        t.Fatal(err)
    }
    ctx := context.Background()
    if err := db.Migrate(ctx); err != nil {
        t.Fatal(err)
    }
    if err := db.SetMeta(ctx, "owner_pubkey", k.PublicHex); err != nil {
        t.Fatal(err)
    }
    if err := db.SetMeta(ctx, "label", name); err != nil {
        t.Fatal(err)
    }
    if _, err := db.ExecContext(ctx,
        `INSERT INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
        homeRelayURL, "home", time.Now().Unix()); err != nil {
        t.Fatal(err)
    }
    db.Close()

    // config: relay.enabled = false (daemon-only).
    cfg := config.Defaults()
    cfg.Relay.Enabled = false
    if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
        t.Fatal(err)
    }

    d, err := daemon.Start(dir)
    if err != nil {
        t.Fatal(err)
    }
    dctx, dcancel := context.WithCancel(context.Background())
    go d.Run(dctx)
    time.Sleep(200 * time.Millisecond)

    in := &instance{
        stateDir: dir,
        relayURL: homeRelayURL,
        daemon:   d,
        relay:    nil, // no local relay
        cancel: func() {
            dcancel()
        },
    }
    t.Cleanup(in.cancel)
    return in
}

func TestDaemonOnly_SendsAndReceives(t *testing.T) {
    // A is a full instance with its own relay; B is daemon-only and uses
    // A's relay as its home.
    a := bringUp(t, "alice")
    b := bringUpDaemonOnly(t, "bob", a.relayURL)

    aPub := keyPubHex(t, a)
    bPub := keyPubHex(t, b)

    // Mutual contacts.
    addContact(t, a, contacts.Contact{Pubkey: bPub, Label: "Bob", Tier: contacts.TierFriend, Relays: []string{a.relayURL}})
    addContact(t, b, contacts.Contact{Pubkey: aPub, Label: "Alice", Tier: contacts.TierFriend, Relays: []string{a.relayURL}})

    // Wait for both daemons to pick up the new contact / relay set.
    time.Sleep(500 * time.Millisecond)

    // B (daemon-only) sends an envelope to A.
    sendEnvelope(t, b, aPub, `{"v":1,"type":"chat","text":"hello from daemon-only","client":{"name":"eidos","ver":"test"}}`)
    awaitInbox(t, a, "hello from daemon-only", 5*time.Second)

    // A sends to B (daemon-only). B receives via its subscription on A's relay.
    sendEnvelope(t, a, bPub, `{"v":1,"type":"chat","text":"reply to bob","client":{"name":"eidos","ver":"test"}}`)
    awaitInbox(t, b, "reply to bob", 5*time.Second)
}

// ---- helpers below assume the existing test harness exposes equivalents.
// If they don't already exist, replicate the relevant snippets from
// loopback_test.go / envelope_test.go as small package-level helpers.

func keyPubHex(t *testing.T, in *instance) string {
    t.Helper()
    c := dialIPC(t, in)
    defer c.Close()
    var resp struct{ Hex string `json:"hex"` }
    if err := mustOKResp(c, "whoami", nil, &resp); err != nil {
        t.Fatal(err)
    }
    return resp.Hex
}

func addContact(t *testing.T, in *instance, c contacts.Contact) {
    t.Helper()
    cli := dialIPC(t, in)
    defer cli.Close()
    params := map[string]any{
        "pubkey": c.Pubkey, "label": c.Label, "tier": "friend", "relays": c.Relays,
    }
    if err := mustOKResp(cli, "contact.add", params, nil); err != nil {
        t.Fatal(err)
    }
}

func sendEnvelope(t *testing.T, in *instance, to, envelopeJSON string) {
    t.Helper()
    cli := dialIPC(t, in)
    defer cli.Close()
    params := map[string]any{"to": to, "content": envelopeJSON}
    if err := mustOKResp(cli, "send", params, nil); err != nil {
        t.Fatal(err)
    }
}

func awaitInbox(t *testing.T, in *instance, wantText string, deadline time.Duration) {
    t.Helper()
    end := time.Now().Add(deadline)
    for time.Now().Before(end) {
        cli := dialIPC(t, in)
        var rows []json.RawMessage
        _ = mustOKResp(cli, "inbox.list", map[string]any{"limit": 50}, &rows)
        cli.Close()
        for _, raw := range rows {
            if containsText(raw, wantText) {
                return
            }
        }
        time.Sleep(100 * time.Millisecond)
    }
    t.Fatalf("inbox did not receive %q within %s", wantText, deadline)
}

func containsText(row json.RawMessage, want string) bool {
    return jsonContains(row, want)
}

func jsonContains(row json.RawMessage, want string) bool {
    return strings.Contains(string(row), want)
}

func mustOKResp(c *ipc.Client, method string, params, out any) error {
    return mustOK(c.Call(method, params, out))
}
```

(If your repo's `loopback_test.go` already defines analogous helpers — `dialIPC`, `mustOK`, `addContact`, `sendEnvelope`, `awaitInbox` — re-use them and remove the duplicate definitions above. The harness already covers most of this in `envelope_test.go`; favor importing those helpers over re-creating.)

- [ ] **Step 3: Run integration tests**

```
go test -tags=integration ./test/integration/...
```

Expected: PASS, including the new `TestDaemonOnly_SendsAndReceives`.

- [ ] **Step 4: Commit**

```
git add test/integration/daemon_only_test.go
git commit -m "test(integration): daemon-only sends and receives via remote relay

Runs two in-process instances: A with bringUp (full daemon + paired
relay), B with bringUpDaemonOnly (daemon only, home points at A's
relay URL, relay.enabled=false). Verifies bidirectional NIP-17 chat:
B sends to A and A sends to B, both routes carrying envelope payloads
through A's single shared relay.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Documentation rewrite

**Files:**
- Modify: `README.md`, `docs/USAGE.md`, `docs/INSTALL.md`, `EXAMPLE.md`

- [ ] **Step 1: Rewrite `README.md` quick-start**

Find the quick-start section. Replace with:

```markdown
## Quick start

```sh
# Install
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh

# Daemon-only against a public Nostr relay (simplest):
eidos gate init --label alice --home wss://relay.damus.io
eidos gate start

# Or: self-host the embedded relay on this same machine:
eidos gate init --label alice --home wss://alice.example.com \
  --with-local-relay --listen 0.0.0.0:22895
eidos gate start
```

See `docs/USAGE.md` for the three home-relay topologies and which to pick.
```

- [ ] **Step 2: Rewrite `docs/USAGE.md`**

Restructure to lead with topology choice. Skeleton:

```markdown
# Eidopsyche Usage

## Step 0 — Initialize

`eidos gate init` requires `--label` and `--home`. The home URL is what
peers will dial to reach you — pick the topology that fits.

### Topology A: Public Nostr relay (zero infrastructure)

```
eidos gate init --label alice --home wss://relay.damus.io
```

Trade-off: the relay operator sees gift-wrap metadata (who, when,
how often). Content stays end-to-end encrypted.

**Note for v0.5:** public Nostr relays following NIP-17's recommended
NIP-42 AUTH gate may silently drop your subscription. v0.6 adds the
client-side AUTH layer that closes this gap.

### Topology B: Self-hosted relay on a separate host

Run the relay on a box with a public address (VPS, homelab); run the
daemon wherever you actually use eidos.

```
eidos gate init --label alice --home wss://my-vps.example.com
```

(See "Self-hosting the embedded relay" in docs/INSTALL.md for setup.)

### Topology C: Bundled local relay (single-host two-instance debug,
or self-hosting on the same box)

```
eidos gate init --label alice --home wss://alice.example.com \
  --with-local-relay --listen 0.0.0.0:22895
```

## Step 1 — Each starts daemon and (optionally) relay

(Same as before; status output now shows "not-installed" for the relay
when `--with-local-relay` was not passed.)

## Step 2 onwards: card / add-contact / send / receive — unchanged.

## Inviting a contact (one-step) — unchanged.

## Single-host two-instance debug — see Topology C above.

## Other commands — list of subcommands unchanged.
```

(Preserve any sections that are still accurate; only rewrite the parts
above. The card/add-contact/send/receive flow is unchanged from v0.4.)

- [ ] **Step 3: Rewrite `docs/INSTALL.md`** — replace the existing default-binds paragraph and add migration section.

Replace the paragraph that begins "The default relay binds..." with:

```markdown
By default, `eidos gate init` creates a daemon-only install: no embedded
relay process is started and only the daemon unit is registered with
your service manager. The home URL you pass to `--home` tells peers how
to dial you. See "Self-hosting the embedded relay" below if you want to
run one on this host.

### Self-hosting the embedded relay

Pass `--with-local-relay` at init to also install and start the
`eidos-gate-relay` unit. `--listen` controls the bind address (default
`0.0.0.0:22895`). For a public deployment, terminate TLS at a reverse
proxy (Caddy / nginx / Cloudflare Tunnel) and forward to the local
plain-WS port; the URL embedded in your card / invite (`--home`) should
be your public `wss://` URL, while `--listen` stays on a local interface.

### Migrating from v0.4

v0.5 changes the gate's initialization. Existing v0.4 state directories
are detected by their absence of `[relay].enabled` in `config.toml` and
rejected with a pointer to this section.

**Path A — re-init from scratch (loses contacts, invites, inbox):**

```sh
eidos gate purge --yes
eidos gate init --label <your-label> --home <url> [--with-local-relay]
```

**Path B — migrate in place (keeps state):**

```sh
eidos gate stop

# Edit ~/.eidos/gate/config.toml — replace the [relay] block with:
#
#   [relay]
#     enabled  = true                  # set to false for daemon-only
#     listen   = "127.0.0.1:22895"     # whatever your previous bind was
#     mode     = "paired"
#     data_dir = "relay"
#
# (Daemon-only) optionally replace the home row in own_relays:

sqlite3 ~/.eidos/gate/state.db <<'SQL'
  DELETE FROM own_relays WHERE role='home';
  INSERT INTO own_relays(relay_url, role, added_at)
    VALUES('wss://your-relay.example.com', 'home', strftime('%s','now'));
SQL

eidos gate start
```
```

- [ ] **Step 4: Rewrite `EXAMPLE.md`**

Replace the existing two-user walkthrough with one that uses the
self-hosted topology. Two users, each running daemon on their laptop and
relay on a VPS they own; mutual `add-contact` via card URI; send /
receive demo. Keep the structure of the existing file — just swap the
topology assumption.

(If `EXAMPLE.md` is short and the existing structure already works for
the new topology with minimal edits, prefer surgical edits over rewrites.)

- [ ] **Step 5: Verify builds + spell-check**

```
go build ./...
gofmt -l .
go vet ./...
```

Expected: all clean.

- [ ] **Step 6: Commit**

```
git add README.md docs/USAGE.md docs/INSTALL.md EXAMPLE.md
git commit -m "docs: update for daemon-only default and topology choice

README quick-start leads with daemon-only against a public relay.
USAGE.md reorganizes around three first-class home-relay topologies
(public Nostr relay, self-hosted, bundled local). INSTALL.md adds
'Self-hosting the embedded relay' and 'Migrating from v0.4' sections.
EXAMPLE.md walks through the self-hosted topology.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: Final verification, push, and PR

**Files:** none modified.

- [ ] **Step 1: Run the full local CI matrix**

```
gofmt -l .
go vet ./...
go build ./...
go test ./...
go test -tags=integration ./...
```

Expected: all clean / PASS.

- [ ] **Step 2: Push the branch**

```
git push -u origin feat/relay-decoupling
```

- [ ] **Step 3: Open the PR**

```
gh pr create --title "feat: decouple daemon from embedded relay" --body "$(cat <<'EOF'
## Summary

- Embedded relay becomes opt-in via new `relay.enabled` boolean; `--with-local-relay` at init flips it on.
- `eidos gate init` requires `--home <url>`; the home URL embedded in cards / invites comes only from `own_relays(role='home')`.
- v0.4 state directories detected (absence of `[relay].enabled`) and rejected with a migration error pointing to `docs/INSTALL.md#migrating-from-v04`. `purge` opts out so v0.4 users can clean up.
- `eidos gate start` only installs the relay unit when `relay.enabled = true`; stop/status/purge keep iterating both unit names so residuals stay reachable.
- New integration test covers a daemon-only instance sending and receiving through another instance's relay.
- Documentation reorganized around three home-relay topologies; v0.6 NIP-42 AUTH layer flagged as the follow-up that closes the public-Nostr-relay gap.

Spec: `docs/superpowers/specs/2026-05-07-mindgate-relay-decoupling-design.md`
Plan: `docs/superpowers/plans/2026-05-07-mindgate-relay-decoupling.md`

## Test plan

- [x] `go test ./...`
- [x] `go test -tags=integration ./...` (includes new daemon-only round-trip)
- [x] `gofmt -l .` and `go vet ./...` clean
- [x] Manual smoke: `init --home wss://x.example` → `relay.enabled=false`, status shows relay not-installed; `init --with-local-relay --listen 127.0.0.1:9999` → both units active
- [x] Manual smoke: v0.4-shaped state dir triggers detection error on `gate status`; `gate purge` skips detection

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

```
gh pr checks --watch
```

Expected: all checks PASS. If any check fails, fix the issue locally, commit, push (NEW commit, do NOT amend or force-push), and continue watching.

- [ ] **Step 5: Wait for Copilot review (if auto-assigned)**

```
gh pr view --json reviews,reviewRequests
```

If Copilot leaves comments, address each (use the receiving-code-review skill for guidance). For each addressed comment, push a NEW commit referencing what you changed.

- [ ] **Step 6: Report back**

Print the PR URL and the CI / review summary to the user. Do NOT merge.

---

## Self-review

**Spec coverage check:**

| Spec section | Task |
|---|---|
| §3 Configuration (Enabled, drop PublicURL, RelayEnabled() helper) | 1 |
| §4.1 init flags + validation | 10 |
| §4.2 start branches on RelayEnabled() | 4, 5, 6 |
| §4.3 status reports not-installed | already in `printStatus`; 6 wires through |
| §4.4 stop tolerates missing relay | already in service.Stop |
| §4.5 purge removes both regardless | already in service.Uninstall |
| §4.6 relay foreground refuses when disabled | 7 |
| §4.7 config CLI updates | 2 |
| §4.8 no shorthand commands | (negative requirement; no task needed) |
| §5 data flow | 6, 10 cover write/read paths |
| §6 components changed | tasks cover each row |
| §7 v0.4 detection | 8, 9 |
| §8 docs | 12 |
| §9.1 unit tests | tests inside 1, 2, 8, 9, 10 |
| §9.2 integration | 11 |
| §9.3 smoke tests | 13 manual checklist |
| §10 acceptance criteria | task tests cover each |
| §11 v0.6 (out of scope) | doc note in 12 |
| §12 out of scope | (negative; no task) |

**Placeholder scan:** No "TBD", "TODO", "implement later", "similar to Task N", or "add error handling" present.

**Type / signature consistency:**
- `Relay.Enabled bool` — used identically across config.go, init.go, config (CLI keys), v04detect.go.
- `cfg.RelayEnabled()` — single helper, no naming drift.
- `WithRelay bool` on `service.Config` — one name, used by systemd_linux and launchd_darwin.
- `initLabel`, `initHome`, `initWithLocalRelay`, `initListen` — package-level vars consistent across init.go and init_test.go.
- `detectV04State(stateDir string) error` — single signature, called from rootCmd.PersistentPreRunE.
- `LoadWithMeta(path) (Config, toml.MetaData, error)` — used only by detectV04State.
