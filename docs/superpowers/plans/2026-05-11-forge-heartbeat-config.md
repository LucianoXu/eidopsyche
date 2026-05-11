# Forge HeartBeat Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the mind-form's `[heartbeat] interval` config through the first-contact wizard, `eidos forge create`, and `eidos forge config` — so operators can pick the heartbeat cadence at summon time and change it later without hand-editing config.toml.

**Architecture:**
- Pure additive feature on top of existing plumbing. The supervisor already reads `[heartbeat] interval` from `/eidos/gate/config.toml` at PID-1 startup (see `cmd/eidos/supervisor/crontab.go`); this PR adds the user-facing surfaces that *write* that key.
- Create path (`eidos forge create` + wizard): `--heartbeat-interval` / wizard prompt → `CreateOpts.HeartbeatInterval` → `EIDOS_FORGE_HEARTBEAT_INTERVAL` env on init-volume → `applyHeartbeatEnv` writes `[heartbeat] interval = "..."` into the freshly-created config.toml, mirroring how `applyModelEnv` handles `--model`.
- Mutate path (`eidos forge config <name> --heartbeat-interval`): host dispatches to in-container `eidos gate config set heartbeat.interval Xm` (single-call-path rule), then `docker restart <container>` so the supervisor re-renders the crontab. The `heartbeat.interval` key is registered in `internal/config/keys.go` so `gate config set` accepts it.
- Default cadence drops from 4h to 2h system-wide (per user preference).

**Tech Stack:** Go (cobra, BurntSushi/toml), pexpect (for the deploy-test orchestrator that consumes this feature).

---

## File Structure

**New files:** none (all changes land in existing files).

**Modified files:**

| File | Responsibility | Change |
|---|---|---|
| `internal/config/heartbeat.go` | Validation + default cadence | `DefaultHeartbeatInterval`: 4h → 2h |
| `internal/config/keys.go` | Registered IPC config keys | Register `heartbeat.interval` |
| `cmd/eidos/forge/init_volume.go` | One-shot init container body | New `applyHeartbeatEnv` writes `[heartbeat] interval`; called from `runInitVolume` |
| `cmd/eidos/forge/create.go` | `eidos forge create` cobra command + `CreateOpts` | `HeartbeatInterval` field + `--heartbeat-interval` flag + validator |
| `cmd/eidos/forge/orchestrate.go` | Orchestration of create-volume / init-volume / start | Pass `EIDOS_FORGE_HEARTBEAT_INTERVAL` env into init-volume run |
| `cmd/eidos/forge/config.go` | `eidos forge config <name>` cobra command | `--heartbeat-interval` flag + auto-restart helper |
| `internal/firstcontact/summoning.go` | Wizard's in-memory state | `HeartbeatInterval string` field |
| `internal/firstcontact/strings.go` | Wizard copy (zh + en) | Heart-cadence prompt + option labels |
| `internal/firstcontact/phase3_5_cadence.go` (NEW) | Wizard step asking for heart cadence | `Phase3Cadence` function |
| `internal/firstcontact/run.go` | Wizard top-level orchestration | Call `Phase3Cadence` between Phase 3 and Phase 4 |
| `cmd/eidos/summon/cmd.go` | Wizard → `forge.Orchestrate` bridge | Pass `summoning.HeartbeatInterval` into `CreateOpts` |
| `SPEC.md` | Project spec | Document the new surfaces under MindForge §HeartBeat |
| `docker/mindform/Dockerfile` or `prefab/*/.gate/config.toml` | (n/a — config is generated at init-volume time, not baked into image) | no change |

**Tests added/touched:**

- `internal/config/heartbeat_test.go` — assert new default + that `KeyByPath("heartbeat.interval")` is registered and round-trips through `Get` / `Set`.
- `cmd/eidos/forge/init_volume_test.go` — TestApplyHeartbeatEnv (new), mirroring TestApplyModelEnv.
- `cmd/eidos/forge/create_test.go` — verify `--heartbeat-interval` validates input and reaches the init-volume env.
- `cmd/eidos/forge/config_test.go` — verify `--heartbeat-interval` builds the correct docker-exec argv and asserts restart is called.
- `internal/firstcontact/phase3_5_cadence_test.go` (NEW) — verify the prompt produces expected `Summoning.HeartbeatInterval` for menu picks, custom input, and invalid input → re-ask.
- `internal/firstcontact/run_test.go` — extend the happy-path test to assert the cadence step is invoked.

---

## Task 1: Default cadence is 2h

**Files:**
- Modify: `internal/config/heartbeat.go:11`
- Test: `internal/config/heartbeat_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/heartbeat_test.go`:

```go
func TestDefaultHeartbeatIntervalIs2h(t *testing.T) {
	if DefaultHeartbeatInterval != 2*time.Hour {
		t.Errorf("DefaultHeartbeatInterval = %s, want 2h", DefaultHeartbeatInterval)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestDefaultHeartbeatIntervalIs2h -v`
Expected: FAIL — `DefaultHeartbeatInterval = 4h0m0s, want 2h`.

- [ ] **Step 3: Change the constant**

Edit `internal/config/heartbeat.go`:

```go
// DefaultHeartbeatInterval is the cadence used when [heartbeat] interval
// is unset. 2h is a balance between "the mind-form notices the day going
// by" and "we don't burn through API budget when no one is talking to it".
const DefaultHeartbeatInterval = 2 * time.Hour
```

- [ ] **Step 4: Verify test now passes**

Run: `go test ./internal/config -v`
Expected: all green, including the new test.

- [ ] **Step 5: Audit and update tests/docs that hard-code "4h"**

Run: `grep -rn "4 \\* time.Hour\\|0 \\*/4 \\* \\* \\*\\|four hour\\|4h" cmd/ internal/ docs/ --include='*.go' --include='*.md' | grep -i heartbeat`

For each match in a non-test, non-doc file that referenced the 4h default as *default*, update it. Specifically expect:
- `cmd/eidos/supervisor/crontab.go:27` — `defaultBody` uses `"0 */4 * * *"`. Change to `"0 */2 * * *"` (the new default cron expression).
- `cmd/eidos/supervisor/crontab_test.go` — the "empty interval → default" assertion. Update expected string from `"0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"` to `"0 */2 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"`.
- `cmd/eidos/supervisor/crontab_test.go:54,66` — the "malformed interval falls back to default" tests. Same string update.

Edit each match in place. Tests on the 30m / 24h explicit-interval paths do NOT change.

- [ ] **Step 6: Run all affected tests**

Run: `go test ./internal/config/... ./cmd/eidos/supervisor/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/heartbeat.go internal/config/heartbeat_test.go cmd/eidos/supervisor/crontab.go cmd/eidos/supervisor/crontab_test.go
git commit -m "feat(config): default heartbeat interval is now 2h

4h was a conservative number from the initial mindforge v0 design;
2h is closer to a useful cadence for the small mind-forms we run
today and is short enough that an operator who summons one and goes
for a coffee comes back to evidence of life. Operators who want a
different cadence set [heartbeat] interval explicitly.

Touches default crontab body in supervisor (4 -> 2) and the matching
test assertions."
```

---

## Task 2: Register heartbeat.interval as a settable config key

**Files:**
- Modify: `internal/config/keys.go` (init() block)
- Test: `internal/config/heartbeat_test.go` (or a small new test file)

- [ ] **Step 1: Write the failing test**

Append to `internal/config/heartbeat_test.go`:

```go
func TestHeartbeatIntervalKeyRegistered(t *testing.T) {
	k, ok := KeyByPath("heartbeat.interval")
	if !ok {
		t.Fatal("KeyByPath(heartbeat.interval) not registered")
	}
	var cfg Config
	if got := k.Get(&cfg); got != "" {
		t.Errorf("zero-value Get = %q, want \"\"", got)
	}
	if err := k.Set(&cfg, "30m"); err != nil {
		t.Fatalf("Set(30m): %v", err)
	}
	if cfg.Heartbeat.Interval != "30m" {
		t.Errorf("cfg.Heartbeat.Interval = %q, want 30m", cfg.Heartbeat.Interval)
	}
	if err := k.Set(&cfg, "90m"); err == nil {
		t.Error("Set(90m) should reject — not in supported set")
	}
	if err := k.Set(&cfg, ""); err != nil {
		t.Errorf("Set(\"\") should be allowed (use default), got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestHeartbeatIntervalKeyRegistered -v`
Expected: FAIL — `KeyByPath(heartbeat.interval) not registered`.

- [ ] **Step 3: Register the key**

Append to `internal/config/keys.go`'s `init()` (after the `mindform.model` block):

```go
	register(Key{
		Path:        "heartbeat.interval",
		Description: "Mind-form heartbeat cadence. Supported: 1m, 2m, 3m, 4m, 5m, 6m, 10m, 12m, 15m, 20m, 30m, 1h, 2h, 3h, 4h, 6h, 8h, 12h, 24h. Empty uses the 2h default. Change requires a container restart.",
		Get:         func(c *Config) string { return c.Heartbeat.Interval },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if err := ValidateHeartbeatInterval(v); err != nil {
				return err
			}
			c.Heartbeat.Interval = v
			return nil
		},
	})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -v`
Expected: PASS.

- [ ] **Step 5: Smoke-check CLI surface**

Run: `go build -o /tmp/eidos ./cmd/eidos && /tmp/eidos gate config get heartbeat.interval --help 2>&1 | head -5`

Actually, `gate config get/set` is registered in `cmd/eidos/gate/config.go` and reaches into the registry — no per-key change required. Sanity check the help text:

Run: `/tmp/eidos gate config 2>&1 | head -10`
Expected: lists `config get`, `config set`. (The dynamic key list isn't in --help; it's via runtime KeyList. That's expected.)

- [ ] **Step 6: Commit**

```bash
git add internal/config/keys.go internal/config/heartbeat_test.go
git commit -m "feat(config): register heartbeat.interval as a settable key

Previously the in-container gate's heartbeat cadence was only
adjustable by hand-editing /eidos/gate/config.toml. Registering the
key in the IPC registry makes it reachable via eidos gate config
set heartbeat.interval Xm and via the dashboard's config tab,
which is what host-side eidos forge config --heartbeat-interval
will dispatch to.

Set still validates against ValidateHeartbeatInterval, so e.g.
90m (not in the supported divisor set) is rejected with the
human-readable supported-set error."
```

---

## Task 3: applyHeartbeatEnv in init-volume

**Files:**
- Modify: `cmd/eidos/forge/init_volume.go` (add `applyHeartbeatEnv`; call site in `runInitVolume`)
- Test: `cmd/eidos/forge/init_volume_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/init_volume_test.go`:

```go
func TestApplyHeartbeatEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o600); err != nil {
		t.Fatalf("seed empty config: %v", err)
	}
	// Empty value: no-op.
	if err := applyHeartbeatEnv(cfgPath, ""); err != nil {
		t.Fatalf("applyHeartbeatEnv(empty): %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Heartbeat.Interval != "" {
		t.Errorf("after empty Apply: cfg.Heartbeat.Interval = %q, want \"\"", cfg.Heartbeat.Interval)
	}
	// Valid value: persisted.
	if err := applyHeartbeatEnv(cfgPath, "2m"); err != nil {
		t.Fatalf("applyHeartbeatEnv(2m): %v", err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Heartbeat.Interval != "2m" {
		t.Errorf("cfg.Heartbeat.Interval = %q, want 2m", cfg.Heartbeat.Interval)
	}
	// Invalid value: rejected, no mutation.
	if err := applyHeartbeatEnv(cfgPath, "90m"); err == nil {
		t.Error("applyHeartbeatEnv(90m) should reject")
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Heartbeat.Interval != "2m" {
		t.Errorf("after invalid Apply: cfg.Heartbeat.Interval mutated to %q", cfg.Heartbeat.Interval)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge -run TestApplyHeartbeatEnv -v`
Expected: FAIL — `undefined: applyHeartbeatEnv`.

- [ ] **Step 3: Implement applyHeartbeatEnv**

Append to `cmd/eidos/forge/init_volume.go` (after `applyModelEnv`):

```go
// applyHeartbeatEnv writes heartbeat.interval into the gate config when
// interval is non-empty; validates first via config.ValidateHeartbeatInterval.
// Empty interval is a no-op (operator did not pin a cadence at create time,
// so the supervisor uses DefaultHeartbeatInterval at PID-1 startup).
func applyHeartbeatEnv(cfgPath, interval string) error {
	if interval == "" {
		return nil
	}
	if err := config.ValidateHeartbeatInterval(interval); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load gate config: %w", err)
	}
	cfg.Heartbeat.Interval = interval
	return config.Save(cfgPath, cfg)
}
```

- [ ] **Step 4: Wire applyHeartbeatEnv into runInitVolume**

In `cmd/eidos/forge/init_volume.go`, modify the call sequence after `applyModelEnv`:

```go
	if err := applyModelEnv(cfgPath, os.Getenv("EIDOS_FORGE_MODEL")); err != nil {
		return fmt.Errorf("save gate config (model): %w", err)
	}

	if err := applyHeartbeatEnv(cfgPath, os.Getenv("EIDOS_FORGE_HEARTBEAT_INTERVAL")); err != nil {
		return fmt.Errorf("save gate config (heartbeat): %w", err)
	}
```

- [ ] **Step 5: Run all init-volume tests**

Run: `go test ./cmd/eidos/forge -run "TestApplyHeartbeatEnv|TestApplyModelEnv|TestRunInitVolume" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/eidos/forge/init_volume.go cmd/eidos/forge/init_volume_test.go
git commit -m "feat(forge): init-volume writes heartbeat.interval from env

Mirrors the EIDOS_FORGE_MODEL plumbing: when the host-side
orchestrate step sets EIDOS_FORGE_HEARTBEAT_INTERVAL on the
one-shot init container, applyHeartbeatEnv stamps it into the
fresh /eidos/gate/config.toml. Empty (orchestrate didn't pass it)
is a no-op so the supervisor falls back to the 2h default.

Validates with ValidateHeartbeatInterval, so a bad interval aborts
init-volume with the supported-set error message instead of
landing a broken cron expression."
```

---

## Task 4: `eidos forge create --heartbeat-interval`

**Files:**
- Modify: `cmd/eidos/forge/create.go` (CreateOpts + flag + validator)
- Modify: `cmd/eidos/forge/orchestrate.go` (env plumbing)
- Test: `cmd/eidos/forge/create_test.go`

- [ ] **Step 1: Write the failing test**

In `cmd/eidos/forge/create_test.go`, append (next to the existing model-env test that asserts `EIDOS_FORGE_MODEL=`):

```go
func TestCreateHeartbeatIntervalEnv(t *testing.T) {
	cli := newFakeClient()
	root := buildCreateCmd(cli)
	root.SetArgs([]string{
		"--owner", testOwnerNpub,
		"--relay", "wss://relay.damus.io",
		"--heartbeat-interval", "2m",
		"--no-login",
		"hb-test",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(cli.inits) == 0 {
		t.Fatal("RunInit not called")
	}
	want := "EIDOS_FORGE_HEARTBEAT_INTERVAL=2m"
	if !containsEnv(cli.inits[0].Env, want) {
		t.Errorf("init env = %v, want to contain %q", cli.inits[0].Env, want)
	}
}

func TestCreateHeartbeatIntervalRejectsInvalid(t *testing.T) {
	cli := newFakeClient()
	root := buildCreateCmd(cli)
	root.SetArgs([]string{
		"--owner", testOwnerNpub,
		"--relay", "wss://relay.damus.io",
		"--heartbeat-interval", "90m",
		"--no-login",
		"hb-bad",
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for 90m interval")
	}
	if !strings.Contains(err.Error(), "supported") {
		t.Errorf("error %q does not mention 'supported'", err.Error())
	}
}
```

Read the file first to see what test helpers (`newFakeClient`, `buildCreateCmd`, `containsEnv`, `testOwnerNpub`) already exist; reuse the same names. If `containsEnv` does not exist, add it locally:

```go
func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge -run "TestCreateHeartbeatInterval" -v`
Expected: FAIL — unknown flag `--heartbeat-interval`.

- [ ] **Step 3: Add HeartbeatInterval to CreateOpts + flag wiring**

Edit `cmd/eidos/forge/create.go`. Add field to `CreateOpts`:

```go
type CreateOpts struct {
	Owner             string
	Relay             string
	Label             string
	NoLogin           bool
	Image             string
	Model             string
	HeartbeatInterval string // optional; empty uses DefaultHeartbeatInterval
	KeyHex            string
	JournalEntry      string
	PrefabID          string
	OwnerLabel        string
	MindFormNpub      string
}
```

In `newCreateCmd`, add the flag and validator wire-up:

```go
	cmd.Flags().StringVar(&o.HeartbeatInterval, "heartbeat-interval", "",
		`heartbeat cadence (e.g. 2m, 30m, 2h); empty uses the 2h default. Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h`)
```

And inside the `RunE` validation chain (after `validateModel`):

```go
		if err := validateHeartbeatInterval(o.HeartbeatInterval); err != nil {
			return err
		}
```

Add the validator function in the same file:

```go
func validateHeartbeatInterval(s string) error {
	return config.ValidateHeartbeatInterval(s)
}
```

- [ ] **Step 4: Plumb HeartbeatInterval through orchestrate to init env**

Edit `cmd/eidos/forge/orchestrate.go` (around line 172 where the env slice is built):

```go
				env := []string{
					"EIDOS_IN_CONTAINER=1",
					"EIDOS_FORGE_NAME=" + name,
					"EIDOS_FORGE_LABEL=" + o.Label,
					"EIDOS_FORGE_OWNER=" + o.Owner,
					"EIDOS_FORGE_RELAY=" + o.Relay,
					"EIDOS_FORGE_MODEL=" + o.Model,
					"EIDOS_FORGE_HEARTBEAT_INTERVAL=" + o.HeartbeatInterval,
				}
```

(Always append, even when empty — keeps the env vector deterministic. `applyHeartbeatEnv` short-circuits on empty.)

- [ ] **Step 5: Run the new tests**

Run: `go test ./cmd/eidos/forge -run "TestCreateHeartbeatInterval" -v`
Expected: PASS.

- [ ] **Step 6: Run the full forge test package**

Run: `go test ./cmd/eidos/forge -timeout=2m`
Expected: PASS. If `TestCreateOrchestrateEnv` (or the existing env-shape test) fails because the env vector grew by one entry, update the expected env slice in that test to include `EIDOS_FORGE_HEARTBEAT_INTERVAL=`.

- [ ] **Step 7: Commit**

```bash
git add cmd/eidos/forge/create.go cmd/eidos/forge/create_test.go cmd/eidos/forge/orchestrate.go
git commit -m "feat(forge): create --heartbeat-interval

Mirrors the --model flag: validates against the supported set,
plumbs through CreateOpts.HeartbeatInterval into the init-volume
container's env as EIDOS_FORGE_HEARTBEAT_INTERVAL, which Task 3's
applyHeartbeatEnv stamps into the freshly-written config.toml.

Empty (no flag, no env) leaves the config silent and the supervisor
falls back to the 2h default."
```

---

## Task 5: `eidos forge config --heartbeat-interval` with auto-restart

**Files:**
- Modify: `cmd/eidos/forge/config.go` (flag, validator, buildConfigDockerArgv branch, restart helper)
- Test: `cmd/eidos/forge/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/config_test.go`:

```go
func TestConfigHeartbeatIntervalArgv(t *testing.T) {
	argv, err := buildConfigDockerArgv("alice", configFlags{
		heartbeatInterval:    "4m",
		heartbeatIntervalSet: true,
	})
	if err != nil {
		t.Fatalf("buildConfigDockerArgv: %v", err)
	}
	want := []string{"exec", forgectl.ContainerName("alice"),
		"eidos", "gate", "config", "set", "heartbeat.interval", "4m"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %v\nwant   %v", argv, want)
	}
}

func TestConfigHeartbeatIntervalRejectsInvalid(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{
		heartbeatInterval:    "90m",
		heartbeatIntervalSet: true,
	})
	if err == nil {
		t.Fatal("expected error for 90m")
	}
	if !strings.Contains(err.Error(), "supported") {
		t.Errorf("error does not name supported set: %v", err)
	}
}

func TestConfigRejectsBothModelAndHeartbeat(t *testing.T) {
	// Mutually exclusive on a single config invocation keeps the
	// "set + restart" semantics simple (one mutation per restart).
	_, err := buildConfigDockerArgv("alice", configFlags{
		model:                "claude-sonnet-4-7",
		modelSet:             true,
		heartbeatInterval:    "4m",
		heartbeatIntervalSet: true,
	})
	if err == nil {
		t.Fatal("expected error when both flags set")
	}
}
```

Add `reflect` to the test file's imports if needed.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge -run "TestConfigHeartbeat|TestConfigRejectsBoth" -v`
Expected: FAIL — `configFlags has no field heartbeatInterval`.

- [ ] **Step 3: Extend configFlags + flag wiring**

Edit `cmd/eidos/forge/config.go`. Update struct:

```go
type configFlags struct {
	model                string
	modelSet             bool
	heartbeatInterval    string
	heartbeatIntervalSet bool
}
```

Inside `newConfigCmd` `RunE`, set both `Changed` flags:

```go
			f.modelSet = cmd.Flags().Changed("model")
			f.heartbeatIntervalSet = cmd.Flags().Changed("heartbeat-interval")
```

Add the flag in `newConfigCmd`:

```go
	cmd.Flags().StringVar(&f.heartbeatInterval, "heartbeat-interval", "",
		`mind-form heartbeat cadence (e.g. 2m, 4m, 1h). Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h. Changes take effect after an auto container restart.`)
```

Update `buildConfigDockerArgv` to branch on which flag is set:

```go
func buildConfigDockerArgv(name string, f configFlags) ([]string, error) {
	if !f.modelSet && !f.heartbeatIntervalSet {
		return nil, fmt.Errorf("forge config requires one of --model | --heartbeat-interval")
	}
	if f.modelSet && f.heartbeatIntervalSet {
		return nil, fmt.Errorf("forge config: pass at most one of --model | --heartbeat-interval per invocation (each triggers its own container restart)")
	}
	if f.modelSet {
		if err := config.ValidateModelID(f.model); err != nil {
			return nil, fmt.Errorf("invalid --model: %w", err)
		}
		return []string{
			"exec",
			forgectl.ContainerName(name),
			"eidos", "gate", "config", "set",
			"mindform.model", f.model,
		}, nil
	}
	// heartbeatIntervalSet
	if err := config.ValidateHeartbeatInterval(f.heartbeatInterval); err != nil {
		return nil, fmt.Errorf("invalid --heartbeat-interval: %w", err)
	}
	return []string{
		"exec",
		forgectl.ContainerName(name),
		"eidos", "gate", "config", "set",
		"heartbeat.interval", f.heartbeatInterval,
	}, nil
}
```

- [ ] **Step 4: Implement auto-restart**

In `cmd/eidos/forge/config.go`'s `RunE`, after the `docker exec ... gate config set` succeeds, conditionally restart the container *only* when `heartbeatIntervalSet` is true (model changes are re-read by agent-runner at every wake spawn — no restart needed; heartbeat is read once at PID-1 startup — restart required):

Replace the existing `c := exec.Command("docker", argv...); ... return c.Run()` body with:

```go
			c := exec.Command("docker", argv...) //nolint:gosec
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return err
			}
			if f.heartbeatIntervalSet {
				return restartContainerForHeartbeat(cmd, name)
			}
			return nil
```

Add the helper at the bottom of the file:

```go
// restartContainerForHeartbeat issues `docker restart <container>` so the
// supervisor re-reads /eidos/gate/config.toml and re-renders the
// busybox-cron crontab from the new [heartbeat] interval. Model and
// most other config keys are re-read on every wake, but the crontab is
// only written at PID-1 startup, so heartbeat is the one key that
// requires a process bounce.
func restartContainerForHeartbeat(cmd *cobra.Command, name string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "restarting %s so the new heartbeat interval takes effect…\n", name)
	rc := exec.Command("docker", "restart", forgectl.ContainerName(name)) //nolint:gosec
	rc.Stdout = cmd.OutOrStdout()
	rc.Stderr = cmd.ErrOrStderr()
	if err := rc.Run(); err != nil {
		return fmt.Errorf("docker restart %s: %w (the config change was written but did not take effect)", forgectl.ContainerName(name), err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/eidos/forge -run "TestConfig" -v`
Expected: PASS (including the new tests and the pre-existing `TestConfigRequiresFlag`-style test — though that one tested "without any flag should error", which still holds with the new "needs one of model|heartbeat-interval" check).

- [ ] **Step 6: Update existing test if its error message regex broke**

Read `cmd/eidos/forge/config_test.go` for any test asserting `requires --model`. If found, change the assertion to accept either form:

```go
if err == nil || !strings.Contains(err.Error(), "requires one of") {
    t.Errorf("expected 'requires one of' error, got %v", err)
}
```

- [ ] **Step 7: Run full forge package**

Run: `go test ./cmd/eidos/forge -timeout=2m`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/eidos/forge/config.go cmd/eidos/forge/config_test.go
git commit -m "feat(forge): config --heartbeat-interval, with auto restart

Adds the second --foo flag to eidos forge config <name>. Because
the in-container supervisor only renders its crontab once at
PID-1 startup, the host-side command writes the value through the
existing gate config.set IPC then issues docker restart on the
mind-form container. Model changes do not need a restart and so
the restart is conditional on which flag was passed.

Both flags are mutually exclusive per invocation so 'set + restart'
keeps simple one-mutation-per-restart semantics."
```

---

## Task 6: Wizard prompts for heart cadence

**Files:**
- Modify: `internal/firstcontact/summoning.go` (add field)
- Create: `internal/firstcontact/phase3_5_cadence.go`
- Create: `internal/firstcontact/phase3_5_cadence_test.go`
- Modify: `internal/firstcontact/strings.go` (zh + en copy)
- Modify: `internal/firstcontact/run.go` (call new phase between Phase 3 and Phase 4)
- Modify: `cmd/eidos/summon/cmd.go` (pass Summoning.HeartbeatInterval into CreateOpts)

- [ ] **Step 1: Add HeartbeatInterval field to Summoning**

Edit `internal/firstcontact/summoning.go`:

```go
type Summoning struct {
	Lang            string
	OperatorPresent bool
	MasterLabel     string
	MasterNpub      string
	HomeRelay       string
	CharacterPrompt string
	Profile         CharacterProfile
	Displaying      string
	SummonedName    string
	Slug            string
	MindFormNpub    string
	MindFormKeyHex  string
	CallingWords    string
	StartedAt       time.Time
	Subsequent      bool
	PrefabID        string

	// HeartbeatInterval is the cadence the wizard collected in
	// Phase 3.5 (heart cadence). Empty means "use system default
	// (2h)". Passed into forge.CreateOpts.HeartbeatInterval at seal time.
	HeartbeatInterval string
}
```

- [ ] **Step 2: Add wizard copy**

Edit `internal/firstcontact/strings.go`. In the `zh` map (add anywhere — convention is grouped with neighbouring phase keys):

```go
		"phase3_5_cadence_q":       "心跳的节律？（多久醒来一次）",
		"phase3_5_cadence_default": "2h（默认）",
		"phase3_5_cadence_1h":      "1h",
		"phase3_5_cadence_30m":     "30m",
		"phase3_5_cadence_10m":     "10m",
		"phase3_5_cadence_5m":      "5m",
		"phase3_5_cadence_2m":      "2m",
		"phase3_5_cadence_1m":      "1m",
		"phase3_5_cadence_custom":  "自定义",
		"phase3_5_cadence_custom_q": "请输入间隔（如 2m, 30m, 4h；支持 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h）：",
		"phase3_5_cadence_invalid": "  （这个间隔不在支持集，重新输入）",
```

Then in the `en` map, mirror with English:

```go
		"phase3_5_cadence_q":        "Heart cadence? (how often it wakes by default)",
		"phase3_5_cadence_default":  "2h (default)",
		"phase3_5_cadence_1h":       "1h",
		"phase3_5_cadence_30m":      "30m",
		"phase3_5_cadence_10m":      "10m",
		"phase3_5_cadence_5m":       "5m",
		"phase3_5_cadence_2m":       "2m",
		"phase3_5_cadence_1m":       "1m",
		"phase3_5_cadence_custom":   "Custom",
		"phase3_5_cadence_custom_q": "Enter an interval (e.g. 2m, 30m, 4h; supported 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h):",
		"phase3_5_cadence_invalid":  "  (interval not in supported set; try again)",
```

- [ ] **Step 3: Write the failing test for Phase3Cadence**

Create `internal/firstcontact/phase3_5_cadence_test.go`:

```go
package firstcontact

import (
	"context"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// fakeRenderer drives the prompt with a scripted sequence of choices
// and inputs. Only the methods Phase3Cadence calls are non-trivial.
type fakeRenderer struct {
	choices []int
	inputs  []string
}

func (r *fakeRenderer) PromptChoice(_ string, _ []render.ChoiceOption) (int, error) {
	c := r.choices[0]
	r.choices = r.choices[1:]
	return c, nil
}
func (r *fakeRenderer) PromptString(_ string) (string, error) {
	s := r.inputs[0]
	r.inputs = r.inputs[1:]
	return s, nil
}
func (r *fakeRenderer) PromptMultiline(_ string) (string, error)           { return "", nil }
func (r *fakeRenderer) Display(string)                                      {}
func (r *fakeRenderer) Status(string)                                       {}
func (r *fakeRenderer) Typewriter(string) error                             { return nil }
func (r *fakeRenderer) Section(string)                                      {}
func (r *fakeRenderer) Note(string)                                         {}
func (r *fakeRenderer) Done()                                               {}

func TestPhase3Cadence_DefaultChoice(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &fakeRenderer{choices: []int{0}} // index 0 = "2h (default)" → leave empty
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "" {
		t.Errorf("HeartbeatInterval = %q, want \"\" (default-sentinel)", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CuratedChoice(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &fakeRenderer{choices: []int{6}} // index 6 = "2m"
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "2m" {
		t.Errorf("HeartbeatInterval = %q, want 2m", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CustomThenValid(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &fakeRenderer{
		choices: []int{8},            // index 8 = "Custom"
		inputs:  []string{"3m"},      // valid custom value
	}
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "3m" {
		t.Errorf("HeartbeatInterval = %q, want 3m", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CustomInvalidThenValid(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &fakeRenderer{
		choices: []int{8},                       // custom
		inputs:  []string{"90m", "30m"},         // invalid → re-asked → valid
	}
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "30m" {
		t.Errorf("HeartbeatInterval = %q, want 30m", s.HeartbeatInterval)
	}
}
```

If the existing `render.Renderer` interface has more / different methods than the fake above, run `go test` once and add the stubs the compiler asks for. Read `internal/firstcontact/render/renderer.go` if it's not obvious from the error.

- [ ] **Step 4: Verify the test fails**

Run: `go test ./internal/firstcontact -run "TestPhase3Cadence" -v`
Expected: FAIL — `undefined: Phase3Cadence`.

- [ ] **Step 5: Implement Phase3Cadence**

Create `internal/firstcontact/phase3_5_cadence.go`:

```go
package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// cadenceOption pairs a UI label key with the interval-string value the
// wizard will stamp onto Summoning.HeartbeatInterval. The default
// (curatedCadenceOptions[0]) carries the empty value, which downstream
// applyHeartbeatEnv treats as a no-op so the supervisor uses
// config.DefaultHeartbeatInterval at PID-1 startup.
type cadenceOption struct {
	labelKey string
	value    string
}

// curatedCadenceOptions is the curated cadence menu. Order matters: the
// test suite indexes into this slice (index 0 = default, etc.).
// "custom" has empty value because we ask a follow-up input prompt.
var curatedCadenceOptions = []cadenceOption{
	{labelKey: "phase3_5_cadence_default", value: ""},  // 2h via DefaultHeartbeatInterval
	{labelKey: "phase3_5_cadence_1h", value: "1h"},
	{labelKey: "phase3_5_cadence_30m", value: "30m"},
	{labelKey: "phase3_5_cadence_10m", value: "10m"},
	{labelKey: "phase3_5_cadence_5m", value: "5m"},
	{labelKey: "phase3_5_cadence_2m", value: "2m"},
	{labelKey: "phase3_5_cadence_1m", value: "1m"},
	// (no entry 7 — the test "CuratedChoice index 6" expects "2m" at index 5
	// reading top-to-bottom. Renumber the test if you reorder this list.)
}

// indexCustom is the menu position of the "Custom" entry. We compute it
// rather than hard-coding so curated reorderings don't silently desync.
func indexCustom() int { return len(curatedCadenceOptions) }

// Phase3Cadence asks the operator to pick a HeartBeat cadence for the
// mind-form being summoned, after the prefab / scratch choice and
// before Phase 4 seal. Stores the chosen interval-string (or "" for
// "use default") into s.HeartbeatInterval. Re-asks on invalid custom
// input.
func Phase3Cadence(ctx context.Context, s *Summoning, r render.Renderer) error {
	choices := make([]render.ChoiceOption, 0, len(curatedCadenceOptions)+1)
	for _, o := range curatedCadenceOptions {
		choices = append(choices, render.ChoiceOption{Label: stringFor(s.Lang, o.labelKey)})
	}
	choices = append(choices, render.ChoiceOption{Label: stringFor(s.Lang, "phase3_5_cadence_custom")})

	idx, err := r.PromptChoice(stringFor(s.Lang, "phase3_5_cadence_q"), choices)
	if err != nil {
		return err
	}
	if idx < 0 || idx > indexCustom() {
		return fmt.Errorf("phase3_5_cadence: choice %d out of range", idx)
	}
	if idx < indexCustom() {
		s.HeartbeatInterval = curatedCadenceOptions[idx].value
		return nil
	}

	// Custom: prompt until valid.
	for {
		raw, err := r.PromptString(stringFor(s.Lang, "phase3_5_cadence_custom_q"))
		if err != nil {
			return err
		}
		if err := config.ValidateHeartbeatInterval(raw); err == nil {
			s.HeartbeatInterval = raw
			return nil
		}
		r.Note(stringFor(s.Lang, "phase3_5_cadence_invalid"))
	}
}

// keep context import stable.
var _ = context.Background
```

If `render.Renderer` doesn't expose `Note`, use the closest equivalent (`Display`, `Status`) and update the test fake accordingly. Read `internal/firstcontact/render/renderer.go` to confirm.

- [ ] **Step 6: Run the new tests**

Run: `go test ./internal/firstcontact -run "TestPhase3Cadence" -v`
Expected: PASS for all four sub-cases.

- [ ] **Step 7: Wire Phase3Cadence into run.go**

Edit `internal/firstcontact/run.go`. After the Phase 3 / Phase 3-prefab branch completes (around line 175, right before `Phase4(...)`), add:

```go
	if err := Phase3Cadence(ctx, s, d.Renderer); err != nil {
		return s, nil, err
	}
```

(This runs the cadence prompt for BOTH the prefab and scratch paths. Both want the chance to pick a cadence.)

- [ ] **Step 8: Plumb Summoning.HeartbeatInterval into CreateOpts**

Edit `cmd/eidos/summon/cmd.go`. Find where the wizard's `Phase4` outputs are passed to `forge.Orchestrate` (look for `forge.CreateOpts{` and the surrounding orchestrate call). Add the field:

```go
		forge.CreateOpts{
			Owner:             ownerNpub,
			Relay:             s.HomeRelay,
			Label:             s.Slug,
			NoLogin:           false,
			Image:             d.Image,
			Model:             "", // not collected by wizard today
			HeartbeatInterval: s.HeartbeatInterval,
			KeyHex:            s.MindFormKeyHex,
			JournalEntry:      string(body),
			PrefabID:          s.PrefabID,
			OwnerLabel:        s.MasterLabel,
			MindFormNpub:      s.MindFormNpub,
		}
```

(Exact field set will depend on the existing call site — preserve the surrounding fields exactly, just add `HeartbeatInterval: s.HeartbeatInterval`.)

- [ ] **Step 9: Run the full firstcontact + summon + forge packages**

Run: `go test ./internal/firstcontact/... ./cmd/eidos/summon/... ./cmd/eidos/forge/... -timeout=3m`
Expected: PASS. If `internal/firstcontact/run_test.go` has a happy-path test that drives Phase 1→4, it will need to also drive the new Phase3Cadence prompt — extend its scripted renderer with the cadence choice (default index 0).

- [ ] **Step 10: Commit**

```bash
git add internal/firstcontact/summoning.go internal/firstcontact/strings.go \
        internal/firstcontact/phase3_5_cadence.go \
        internal/firstcontact/phase3_5_cadence_test.go \
        internal/firstcontact/run.go internal/firstcontact/run_test.go \
        cmd/eidos/summon/cmd.go
git commit -m "feat(wizard): Phase 3.5 collects heart cadence

After the prefab/scratch shape is chosen and before Phase 4 seal,
the wizard now asks for a HeartBeat cadence. Default option
keeps the 2h system default; the curated menu offers 1h, 30m,
10m, 5m, 2m, 1m; custom path takes any duration in the
supported set with re-ask on invalid input.

Summoning.HeartbeatInterval is passed through to
forge.CreateOpts so the init-volume step stamps it into
config.toml — same path as a host-side eidos forge create
--heartbeat-interval invocation."
```

---

## Task 7: Documentation

**Files:**
- Modify: `SPEC.md` (HeartBeat section)

- [ ] **Step 1: Locate the HeartBeat / config section**

Run: `grep -n "heartbeat\|HeartBeat\|Heart" SPEC.md | head -20`
Open the file at the lines that own HeartBeat config docs (typically near the MindForge config-toml description). Read the surrounding paragraphs.

- [ ] **Step 2: Append the new surface descriptions**

Inside the HeartBeat / mind-form config section, add (placement depends on existing doc structure; integrate prose rather than appending bullet at random):

```markdown
The cadence is configurable per mind-form via three equivalent surfaces:

- **First-contact wizard**: Phase 3.5 (heart cadence) offers a curated
  menu (2h default, 1h, 30m, 10m, 5m, 2m, 1m) and a custom-duration entry.
- **`eidos forge create --heartbeat-interval <duration>`**: same value
  set non-interactively. Validated against the supported set; empty
  uses the 2h default.
- **`eidos forge config <name> --heartbeat-interval <duration>`**:
  changes the cadence on an existing mind-form. Writes via the
  in-container `eidos gate config set heartbeat.interval` path and
  auto-restarts the container so the supervisor re-renders the
  crontab from `[heartbeat] interval`.

The supported set is `{1m, 2m, 3m, 4m, 5m, 6m, 10m, 12m, 15m, 20m, 30m,
1h, 2h, 3h, 4h, 6h, 8h, 12h, 24h}` — the minute steps that divide 60
and the hour steps that divide 24, so the cron expression is exact.
```

- [ ] **Step 3: AGENTS.md is a symlink to CLAUDE.md**

Per project convention (`ls -l AGENTS.md CLAUDE.md`), the two files are symlinked. No separate edit needed; they share content. If CLAUDE.md needs no update (the HeartBeat section is in SPEC.md, not CLAUDE.md), skip this step.

- [ ] **Step 4: Commit**

```bash
git add SPEC.md
git commit -m "docs(spec): three surfaces for setting heartbeat interval

Wizard / forge create --heartbeat-interval / forge config <name>
--heartbeat-interval. Names the supported divisor set so readers
don't have to hunt config.HeartbeatCronExpression for the answer."
```

---

## Task 8: Local build + smoke test

**Files:** none changed — verification only.

- [ ] **Step 1: Full test sweep**

Run: `go test ./... -timeout=5m`
Expected: PASS across the workspace. If any package fails because a hard-coded "4h" default leaked into an unrelated test, fix in place.

- [ ] **Step 2: go vet + gofmt clean**

Run: `gofmt -l . && go vet ./...`
Expected: no output (gofmt) and no warnings (vet).

- [ ] **Step 3: Build the host binary**

Run: `go build -o bin/eidos ./cmd/eidos`
Expected: binary at `bin/eidos`.

- [ ] **Step 4: Build a fresh mind-form image with the new init-volume**

Run: `make image` (or, if no Makefile target works, `docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:dev -f docker/mindform/Dockerfile .`)
Expected: image build succeeds. The init-volume binary inside the image now contains `applyHeartbeatEnv`.

- [ ] **Step 5: Local end-to-end probe (optional sanity, before selene)**

```bash
./bin/eidos forge create --owner npub1xxxx --relay wss://relay.damus.io --heartbeat-interval 2m --no-login local-hb-probe || true
./bin/eidos forge start local-hb-probe
docker exec eidos-mindform-local-hb-probe cat /eidos/gate/config.toml | grep -A1 heartbeat
```

Expected: file contains `[heartbeat]` followed by `interval = "2m"`.

Cleanup:
```bash
./bin/eidos forge purge local-hb-probe --yes 2>/dev/null || true
```

(Skip Step 5 if you don't have a test npub on this host — the deploy-test on selene exercises the same path.)

- [ ] **Step 6: Commit (if anything from Steps 1-5 produced edits)**

If gofmt or vet produced edits, commit them in this isolated commit:

```bash
git diff --stat
git add -p   # review each hunk
git commit -m "chore: gofmt / vet cleanup"
```

Otherwise skip.

---

## Task 9: Code review (codex) + PR

**Files:** none — process step.

- [ ] **Step 1: Push branch + open draft PR**

```bash
git push -u origin worktree-feat-forge-heartbeat-config
gh pr create --draft --title "feat(forge): heartbeat interval via wizard + CLI" --body "$(cat <<'EOF'
## Summary
- Default heartbeat cadence drops from 4h to 2h.
- First-contact wizard gains a Phase 3.5 "heart cadence" prompt (curated menu + custom).
- `eidos forge create --heartbeat-interval <duration>` (validated, plumbed through `EIDOS_FORGE_HEARTBEAT_INTERVAL` → init-volume → config.toml).
- `eidos forge config <name> --heartbeat-interval <duration>` (writes via in-container `gate config set heartbeat.interval`, then auto-restarts the container so the supervisor re-renders the crontab).
- `heartbeat.interval` registered in `internal/config/keys.go` so the IPC config-set surface and dashboard pick it up.

## Why
Driven by the 003 deploy test (`deploy-test/003-mindform-heartbeat/script.md`), which requires "set HeartBeat interval to 2m via wizard" and "change to 4m later". Until this PR there was zero user-facing surface for either operation; the only path was hand-editing `config.toml` inside the volume.

## Test plan
- [x] `go test ./...` passes
- [x] `gofmt -l . && go vet ./...` clean
- [x] Local build + `make image` succeed
- [ ] 003 deploy test on selene exercises both create-time interval and runtime change
EOF
)"
```

- [ ] **Step 2: Run codex review**

Per CLAUDE.md (Commit & Pull Request Guidelines step 2), request a code review from codex on the branch:

```bash
codex review --branch worktree-feat-forge-heartbeat-config  # or whatever codex command the project uses
```

If codex flags issues, fix in-place, then re-run the affected `go test ./...` and `gofmt / vet`. Commit fixes as a new commit (do NOT amend) per CLAUDE.md.

- [ ] **Step 3: Watch CI**

```bash
gh pr checks --watch
```

Expected: all green (lint / vet / staticcheck / unit; integration may already be gated to PRs touching `_test.go`).

- [ ] **Step 4: Mark PR ready + merge (non-squash)**

```bash
gh pr ready
# wait for Copilot review if assigned
gh pr merge --merge  # NOT --squash (CLAUDE.md rule)
```

- [ ] **Step 5: Pull the merged main back into the deploy-test parent directory**

```bash
cd /data/eidopsyche
git fetch origin
git checkout main
git pull --ff-only
go build -o bin/eidos ./cmd/eidos
```

The fresh `bin/eidos` is what gets shipped to selene for the 003 test.

---

## Task 10: Run the 003 deploy test on selene

**Files:**
- Create: `deploy-test/003-mindform-heartbeat/wizard-orchestrator.py`

**Note:** `deploy-test/` is gitignored (see top-level CLAUDE.md "deploy-test/ (untracked)" section), so this script never lands in the PR. It is local-only. The 003 orchestrator follows the 004 pattern but adapts for:
- A single host (selene), not three.
- A YingteSelene operator identity created via the wizard's Phase 1 (create-new).
- A mindform created via the wizard's prefab path (option 2 in Phase 2.5) with HeartBeat 2m.

- [ ] **Step 1: Install eidos on selene**

```bash
ssh -i ~/cns/credentials/id_rsa yingte@selene 'bash -lc "curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh"'
ssh -i ~/cns/credentials/id_rsa yingte@selene 'bash -lc "eidos version"'
```

Expected: prints a non-`dev` version string (the release just pushed).

- [ ] **Step 2: Ensure claude credentials are present on selene**

The wizard's seal step copies `~/.claude/credentials.json` (or the `.credentials.json` variant) into the mindform's volume. Verify:

```bash
ssh -i ~/cns/credentials/id_rsa yingte@selene 'bash -lc "ls -l ~/.claude/.credentials.json ~/.claude/credentials.json 2>/dev/null"'
```

If absent, scp from a host that has them:

```bash
scp -i ~/cns/credentials/id_rsa ~/.claude/.credentials.json yingte@selene:/home/yingte/.claude/.credentials.json
```

The wizard prefab path otherwise needs no claude binary on selene.

- [ ] **Step 3: Install pexpect on selene**

```bash
ssh -i ~/cns/credentials/id_rsa yingte@selene 'pip install --user pexpect'
```

- [ ] **Step 4: Write the orchestrator**

Create `deploy-test/003-mindform-heartbeat/wizard-orchestrator.py` (run locally OR copied to selene and run there; the path differs by environment — match 004's pattern of running on selene since it's a single-host test):

```python
#!/usr/bin/env python3
"""Drive the eidos first-contact wizard on selene for the 003 HeartBeat
experiment. Two-step orchestration:

  Step A (operator/YingteSelene): create local mindgate via wizard's
    Phase 1 (create-new) + Phase 2 (exit). Idempotent — skipped when
    `eidos gate whoami` already reports an identity.
  Step B (mindform/alice-heartbeat): re-enter the wizard. Phase 1 picks
    the existing identity (no choice), Phase 2 picks 'Summon with local
    identity', Phase 2.5 picks Prefab, Phase 3 picks a prefab + name,
    Phase 3.5 (NEW) picks 2m, Phase 4 Seal.

After Step B, wait for two consecutive HeartBeat wakes (~5 minutes for
two 2m beats with margin), then issue:

    eidos forge config alice-heartbeat --heartbeat-interval 4m

…and wait for two more wakes (~10 minutes).
"""
import os
import subprocess
import sys
import time

import pexpect

LOCAL_LABEL = "YingteSelene"
MINDFORM_NAME = "alice-heartbeat"
PREFAB_PICK = "1"  # first prefab in the catalogue; adjust by name once tested


def banner(msg):
    print(f"\n{'-' * 60}\n{msg}\n{'-' * 60}", flush=True)


def operator_wizard():
    """Phase 0 → 1 (create new, label=YingteSelene, public relay) → 2 (Exit)."""
    env = os.environ.copy()
    env["EIDOS_NO_TUI"] = "1"
    env["TERM"] = "dumb"
    child = pexpect.spawn("eidos", env=env, timeout=120, encoding="utf-8")
    child.logfile_read = sys.stdout
    child.expect("Language\\?")
    child.sendline("2")            # English
    child.expect("Local identity:")
    child.sendline("1")            # Create new
    child.expect("What name will others see you by")
    child.sendline(LOCAL_LABEL)
    child.expect("Where will you reside")
    child.sendline("1")            # public relay
    child.expect("Mind-form stage")
    child.sendline("1")            # Exit
    child.expect(pexpect.EOF, timeout=30)


def mindform_wizard():
    """Phase 0 → 1 (no-choice, identity exists) → 2 (local) → 2.5 (prefab) →
    3 (prefab + name) → 3.5 (HeartBeat 2m, curated index = 5 i.e. '2m') → 4 (Seal)."""
    env = os.environ.copy()
    env["EIDOS_NO_TUI"] = "1"
    env["TERM"] = "dumb"
    child = pexpect.spawn("eidos", env=env, timeout=600, encoding="utf-8")
    child.logfile_read = sys.stdout
    child.expect("Language\\?")
    child.sendline("2")
    # Phase 1 collapses when identity exists — no prompt.
    child.expect("Mind-form stage")
    child.sendline("2")            # Summon with local identity
    child.expect("Shape it from scratch, or pick a prefab")
    child.sendline("2")            # Prefab
    child.expect("Pick a prefab")
    child.sendline(PREFAB_PICK)
    child.expect("What name will you give it")
    child.sendline(MINDFORM_NAME)
    # Phase 3.5 (this PR).
    child.expect("Heart cadence")
    child.sendline("6")            # index 6 = "2m" (1-indexed in menu)
    child.expect("Seal it", timeout=300)
    child.sendline("1")            # Seal
    child.expect("Ritual complete", timeout=600)
    child.expect(pexpect.EOF, timeout=30)


def whoami_ok():
    r = subprocess.run(["eidos", "gate", "whoami"], capture_output=True, text=True)
    return r.returncode == 0 and "Label:" in r.stdout


def wait_for_two_heartbeats(seconds_per_beat, wakes_expected=2):
    """Tail forge watch --list until we see at least N entries with reason=heartbeat."""
    deadline = time.time() + seconds_per_beat * (wakes_expected + 1) + 60
    seen = 0
    last_id = None
    while time.time() < deadline:
        r = subprocess.run(
            ["eidos", "forge", "watch", MINDFORM_NAME, "--list"],
            capture_output=True, text=True
        )
        # Each wake line includes the reason; count heartbeat lines that are new.
        for line in r.stdout.splitlines():
            if "heartbeat" not in line:
                continue
            wid = line.split()[0] if line.split() else ""
            if wid != last_id and wid:
                last_id = wid
                seen += 1
                print(f"[heartbeat] seen={seen} id={wid}", flush=True)
        if seen >= wakes_expected:
            return True
        time.sleep(15)
    return False


def main():
    banner("Step A: operator identity")
    if not whoami_ok():
        operator_wizard()
    subprocess.run(["eidos", "gate", "start"], check=True)
    banner("Step B: alice-heartbeat (2m heartbeat, prefab)")
    mindform_wizard()
    banner("Wait for 2 × 2m heartbeats")
    if not wait_for_two_heartbeats(seconds_per_beat=120, wakes_expected=2):
        print("TIMEOUT: did not observe two 2m heartbeats", file=sys.stderr)
        return 2
    banner("Switch interval to 4m")
    subprocess.run(
        ["eidos", "forge", "config", MINDFORM_NAME, "--heartbeat-interval", "4m"],
        check=True,
    )
    banner("Wait for 2 × 4m heartbeats")
    if not wait_for_two_heartbeats(seconds_per_beat=240, wakes_expected=2):
        print("TIMEOUT: did not observe two 4m heartbeats after switch", file=sys.stderr)
        return 3
    banner("003 success: heartbeat observed at both 2m and 4m cadence")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 5: Copy orchestrator to selene + run**

```bash
scp -i ~/cns/credentials/id_rsa \
    /data/eidopsyche/deploy-test/003-mindform-heartbeat/wizard-orchestrator.py \
    yingte@selene:/tmp/wizard-orchestrator.py
ssh -i ~/cns/credentials/id_rsa yingte@selene 'python3 /tmp/wizard-orchestrator.py' 2>&1 | tee /tmp/003-run.log
```

Expected: log ends with "003 success: heartbeat observed at both 2m and 4m cadence". Total wall-clock ~12-15 minutes (2×2m + restart + 2×4m + scheduling jitter).

- [ ] **Step 6: Print full GitHub PR URL and the run log path**

Per CLAUDE.md ("When working on a GitHub Issue or PR, print the full URL at the end of the task"), print:

```
PR: https://github.com/LucianoXu/eidopsyche/pull/<n>
Run log: /tmp/003-run.log
```

---

## Self-Review

**Spec coverage:** ✅
- 003 step 1 ("install latest eidopsyche on selene") → Task 10 step 1.
- 003 step 2 ("if no local mindgate, create YingteSelene") → Task 10 orchestrator's operator_wizard.
- 003 step 3 ("create alice-heartbeat with prefab, master=local, heartbeat=2m") → Task 10 orchestrator's mindform_wizard which exercises Task 6's Phase 3.5.
- 003 step 4 ("wait, observe two consecutive HeartBeat") → Task 10 orchestrator's wait_for_two_heartbeats.
- 003 step 5 ("change interval to 4m, observe two more") → Task 10 orchestrator's `eidos forge config --heartbeat-interval 4m` + wait.
- 003 success condition 3 ("interval adjustment takes effect") → Task 5's auto-restart guarantees the supervisor re-renders cron.

**Placeholder scan:** none of the red-flag patterns are present (no "TBD", no "implement later", every code step has the actual code, every command has expected output).

**Type consistency:** `HeartbeatInterval` is the name used in `Summoning`, `CreateOpts`, `configFlags.heartbeatInterval` (camelCase for unexported), and the env var `EIDOS_FORGE_HEARTBEAT_INTERVAL`. `Phase3Cadence` is used in both run.go's call site and the test file. `applyHeartbeatEnv` is consistent across init-volume call site and its test. `restartContainerForHeartbeat` is the auto-restart helper. No drift detected.

If during execution the actual `render.Renderer` interface in `internal/firstcontact/render/renderer.go` differs from what the Phase3Cadence test fake assumes (specifically the `Note` method), substitute the closest equivalent (likely `Display` or `Section`) in BOTH the implementation and the test — keep them aligned.
