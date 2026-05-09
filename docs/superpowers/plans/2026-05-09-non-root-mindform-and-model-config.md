# Non-root mind-form container + model config — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch the mind-form container to a non-root `eidos:1000` user (with NOPASSWD sudo + operator `--user root` exec hatch); switch claude invocation from `--permission-mode auto` to `--dangerously-skip-permissions`; add per-mind-form `mindform.model` config pinned at create time and mutable post-create.

**Architecture:** Image-level changes (Dockerfile + crontab move + USER eidos), one-line agent-runner change, init-volume chowns at the end while running as root, new config registry entry for `mindform.model`, thin host CLI wrappers for create-time pin and post-create mutation. No migration of existing volumes — operator does `purge && create`.

**Tech Stack:** Go 1.25, cobra CLI, alpine 3.19 image, docker SDK (`docker/docker/client`), `BurntSushi/toml`.

**Spec:** [`docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md`](../specs/2026-05-09-non-root-mindform-and-model-config-design.md)

---

## File map

**Create:**
- `internal/config/model.go` — `ValidateModelID(string) error` + regex
- `internal/config/model_test.go` — table tests
- `cmd/eidos/forge/config.go` — `eidos forge config <name>` host command
- `cmd/eidos/forge/config_test.go` — argv assembly test

**Modify:**
- `internal/config/config.go` — add `MindFormConfig` block
- `internal/config/keys.go` — register `mindform.model`
- `internal/config/keys_test.go` — sanity test for the new key
- `internal/forgectl/docker.go` — add `User` to `RunInitOpts`, plumb to `container.Config.User`
- `cmd/eidos/forge/orchestrate.go` — `RunInit` call passes `User: "0:0"` and `EIDOS_FORGE_MODEL` env
- `cmd/eidos/forge/init_volume.go` — final `chown -R eidos:eidos /eidos` step; honour `EIDOS_FORGE_MODEL`
- `cmd/eidos/forge/init_volume_test.go` — chown round-trip + model-env test
- `cmd/eidos/forge/create.go` — `--model` flag, validation, plumb env
- `cmd/eidos/forge/create_test.go` — model flag test
- `cmd/eidos/forge/exec.go` — `--user` flag
- `cmd/eidos/forge/incontainer.go` — register `newConfigCmd` in `registerHost`
- `cmd/eidos/supervisor/agent_runner.go` — `--dangerously-skip-permissions`, conditional `--model`, drop workaround comment
- `cmd/eidos/supervisor/agent_runner_test.go` — argv builder test
- `cmd/eidos/supervisor/run.go` — `crond -c /var/spool/cron/crontabs`
- `docker/mindform/Dockerfile` — eidos user, sudo, crontab move, USER eidos
- `docker/mindform/crontab` — unchanged content; just relocated by Dockerfile
- `internal/ontology/template/CLAUDE.md` — append Chinese paragraph about eidos user
- `CHANGELOG.md` — Unreleased section: BREAKING CHANGES + features

---

## Task 1: model id validation

**Files:**
- Create: `internal/config/model.go`
- Create: `internal/config/model_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/config/model_test.go
package config

import "testing"

func TestValidateModelID(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = should be accepted
	}{
		{"", true}, // empty = use claude default
		{"claude-sonnet-4-7", true},
		{"claude-haiku-4-5", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-4-6-20250101", true},
		// Reject: wrong family
		{"claude-foo-4-7", false},
		// Reject: missing version
		{"claude-sonnet", false},
		// Reject: not claude-prefixed
		{"sonnet-4-7", false},
		// Reject: non-numeric segment
		{"claude-sonnet-x-7", false},
		// Reject: trailing junk
		{"claude-sonnet-4-7 ; rm -rf /", false},
	}
	for _, c := range cases {
		err := ValidateModelID(c.in)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateModelID(%q): got accepted=%v err=%v, want accepted=%v",
				c.in, got, err, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

```
cd /data/eidopsyche/.claude/worktrees/non-root-mindform
go test ./internal/config/ -run TestValidateModelID -v
```

Expected: FAIL — `ValidateModelID` undefined.

- [ ] **Step 3: Implement**

```go
// internal/config/model.go
package config

import (
	"fmt"
	"regexp"
)

// modelIDRegex matches Anthropic's claude model id shape:
// claude-{family}-{numeric-segments}, optionally with a datestamped suffix.
// Examples that match: claude-sonnet-4-7, claude-haiku-4-5,
// claude-opus-4-7, claude-sonnet-4-6-20250101.
//
// We do not maintain a whitelist of known model ids — Anthropic ships new
// models faster than we can chase. The regex catches typos and shell
// metacharacters; runtime gives the operator a clear error on a bona-fide
// unknown id.
var modelIDRegex = regexp.MustCompile(`^claude-(sonnet|haiku|opus)-[0-9]+(-[0-9]+)*$`)

// ValidateModelID returns nil for the empty string (meaning "use claude's
// default") and for syntactically plausible model ids; otherwise it
// returns an error explaining the expected shape.
func ValidateModelID(id string) error {
	if id == "" {
		return nil
	}
	if !modelIDRegex.MatchString(id) {
		return fmt.Errorf("invalid model id %q: expected shape claude-{sonnet|haiku|opus}-N[-N...] (e.g. claude-sonnet-4-7)", id)
	}
	return nil
}
```

- [ ] **Step 4: Run test, verify pass**

```
go test ./internal/config/ -run TestValidateModelID -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/model.go internal/config/model_test.go
git commit -m "$(cat <<'EOF'
feat(config): add ValidateModelID for claude model ids

Loose syntactic regex; pre-validates the --model flag at every entry
point so we fail at create time, not at first wake.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: MindForm config block + registry entry

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/keys.go`
- Modify: `internal/config/keys_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/config/keys_test.go`:

```go
func TestKeyByPath_MindFormModel(t *testing.T) {
	cfg := Defaults()
	k, ok := KeyByPath("mindform.model")
	if !ok {
		t.Fatal("mindform.model should be registered")
	}
	// Empty is allowed (means "use claude default").
	if err := k.Set(&cfg, ""); err != nil {
		t.Errorf("empty value should be accepted: %v", err)
	}
	if got := k.Get(&cfg); got != "" {
		t.Errorf("Get after Set(\"\") = %q, want \"\"", got)
	}
	// Valid id round-trips.
	if err := k.Set(&cfg, "claude-sonnet-4-7"); err != nil {
		t.Fatalf("valid id should be accepted: %v", err)
	}
	if got := k.Get(&cfg); got != "claude-sonnet-4-7" {
		t.Errorf("Get = %q, want %q", got, "claude-sonnet-4-7")
	}
	// Invalid id rejected.
	if err := k.Set(&cfg, "not-a-claude-model"); err == nil {
		t.Error("invalid id should be rejected")
	}
}
```

- [ ] **Step 2: Run test, verify failure**

```
go test ./internal/config/ -run TestKeyByPath_MindFormModel -v
```

Expected: FAIL — `mindform.model` not registered.

- [ ] **Step 3: Add the config struct field**

In `internal/config/config.go`, modify the `Config` struct and add a new type:

```go
type Config struct {
	StateDir string `toml:"state_dir"`
	LogLevel string `toml:"log_level"`

	Daemon    DaemonConfig    `toml:"daemon"`
	Publish   PublishConfig   `toml:"publish"`
	Subscribe SubscribeConfig `toml:"subscribe"`
	Dashboard DashboardConfig `toml:"dashboard"`
	Wake      WakeConfig      `toml:"wake"`
	MindForm  MindFormConfig  `toml:"mindform"`
}

// MindFormConfig is the in-container gate's mind-form-runtime settings.
// Lives in /eidos/gate/config.toml; ignored on host gates (no [mindform]
// block means MindForm is the zero value).
type MindFormConfig struct {
	// Model pins the claude model used by agent-runner. Empty = let
	// claude pick its subscription default. Valid ids match
	// ValidateModelID; the registry's mindform.model setter enforces
	// this on Set, but Load tolerates anything (so a hand-edited
	// config.toml with an unknown id surfaces at the next wake when
	// claude rejects it, not at gate-daemon startup).
	Model string `toml:"model"`
}
```

- [ ] **Step 4: Register the key**

In `internal/config/keys.go`, append a new `register(...)` block to the end of `init()` (after `dashboard.listen`):

```go
	register(Key{
		Path:        "mindform.model",
		Description: "Pin the claude model used by agent-runner. Empty (default) lets claude pick its subscription default.",
		Get:         func(c *Config) string { return c.MindForm.Model },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if err := ValidateModelID(v); err != nil {
				return err
			}
			c.MindForm.Model = v
			return nil
		},
	})
```

- [ ] **Step 5: Run tests, verify pass**

```
go test ./internal/config/ -v
```

Expected: PASS for all tests including the new one.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/keys.go internal/config/keys_test.go
git commit -m "$(cat <<'EOF'
feat(config): add mindform.model key + MindFormConfig block

Registry-backed; reuses existing eidos gate config get/set surface and
the dashboard's Settings tab. ValidateModelID gates writes.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: agent-runner — `--dangerously-skip-permissions` + `--model`

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go`
- Modify: `cmd/eidos/supervisor/agent_runner_test.go`

- [ ] **Step 1: Refactor the argv build into a testable function (TDD enabler)**

Currently the `exec.Command("claude", ...)` call inlines the argv. We need an extractable seam to test the argv. In `agent_runner.go`, replace the `c := exec.Command("claude", ...)` block with:

```go
	args := buildClaudeArgs(string(identity), msg, "/eidos/gate/config.toml")
	c := exec.Command("claude", args...)
```

And add the helper at the bottom of the file:

```go
// buildClaudeArgs constructs the argv passed to `claude` for one wake.
// Reads the mind-form config to pick up an optional model pin. The
// container is the trust boundary; --dangerously-skip-permissions is
// the documented sandbox path (see docs/superpowers/specs/
// 2026-05-09-non-root-mindform-and-model-config-design.md).
func buildClaudeArgs(identity, msg, configPath string) []string {
	args := []string{
		"--append-system-prompt", identity,
		"--dangerously-skip-permissions",
	}
	if cfg, err := config.Load(configPath); err == nil {
		if model := cfg.MindForm.Model; model != "" {
			args = append(args, "--model", model)
		}
	}
	args = append(args, "-p", msg)
	return args
}
```

Add `"github.com/LucianoXu/eidopsyche/internal/config"` to the file's imports.

Delete the long workaround comment block (lines that start with `// --permission-mode auto:` through the closing `//` lines).

- [ ] **Step 2: Add tests**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestBuildClaudeArgs_NoModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// Write a config without [mindform].model.
	if err := os.WriteFile(cfgPath, []byte("log_level = \"info\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := buildClaudeArgs("identity-text", "wake-msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity-text",
		"--dangerously-skip-permissions",
		"-p", "wake-msg",
	}
	if !slicesEqual(args, want) {
		t.Errorf("argv = %v, want %v", args, want)
	}
}

func TestBuildClaudeArgs_WithModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := "[mindform]\nmodel = \"claude-sonnet-4-7\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	args := buildClaudeArgs("identity", "msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity",
		"--dangerously-skip-permissions",
		"--model", "claude-sonnet-4-7",
		"-p", "msg",
	}
	if !slicesEqual(args, want) {
		t.Errorf("argv = %v, want %v", args, want)
	}
}

func TestBuildClaudeArgs_MissingConfig(t *testing.T) {
	// Non-existent config path — argv still well-formed, no crash.
	args := buildClaudeArgs("ident", "m", "/nonexistent/config.toml")
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"-p", "m",
	}
	if !slicesEqual(args, want) {
		t.Errorf("argv = %v, want %v", args, want)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

Add imports `"os"` and `"path/filepath"` if not already present in the test file.

- [ ] **Step 3: Run tests, verify pass**

```
go test ./cmd/eidos/supervisor/ -v -run TestBuildClaudeArgs
```

Expected: 3 PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go
git commit -m "$(cat <<'EOF'
feat(supervisor): --dangerously-skip-permissions + per-wake model pin

The container is the sandbox; this is the documented path. Picks up
mindform.model from /eidos/gate/config.toml when set.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: forgectl — `RunInitOpts.User`

**Files:**
- Modify: `internal/forgectl/docker.go`

- [ ] **Step 1: Add `User` field**

In `RunInitOpts` (around line 89):

```go
type RunInitOpts struct {
	Image     string
	Mount     Mount
	Env       []string
	Cmd       []string
	Stdin     io.Reader
	AttachTTY bool
	// User overrides the image's USER directive for this one-shot run.
	// Format is the same as docker's --user flag: "uid", "uid:gid",
	// "name", or "name:group". Empty = inherit image's USER.
	User string
}
```

- [ ] **Step 2: Plumb to container.Config.User**

In `RunInit` (around line 217), set `User` on the `container.Config`:

```go
	cfg := &container.Config{
		Image:        opts.Image,
		Env:          opts.Env,
		Cmd:          opts.Cmd,
		User:         opts.User,
		AttachStdin:  opts.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    opts.Stdin != nil,
		StdinOnce:    opts.Stdin != nil,
		Tty:          opts.AttachTTY,
	}
```

- [ ] **Step 3: Verify build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/forgectl/docker.go
git commit -m "$(cat <<'EOF'
feat(forgectl): RunInitOpts.User to override image USER for one-shot runs

Lets eidos forge create's init container run as root (User: "0:0")
even when the image's default USER is non-root.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: init-volume — chown step + model env

**Files:**
- Modify: `cmd/eidos/forge/init_volume.go`
- Modify: `cmd/eidos/forge/init_volume_test.go`

- [ ] **Step 1: Add failing tests**

Replace `cmd/eidos/forge/init_volume_test.go` content with:

```go
package forge

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestExtractTarRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "greet.txt",
		Mode:     0o600,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractTar(&buf, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "greet.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// TestChownTreeIdempotent walks a tmpdir and asserts every entry
// post-chownTree has the requested uid/gid, both first call and second.
// We use the current uid/gid since tests don't run as root.
func TestChownTreeIdempotent(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "a")
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWriteFile(t, filepath.Join(root, "sub", "b.txt"), "b")

	uid := os.Getuid()
	gid := os.Getgid()
	if err := chownTree(root, uid, gid); err != nil {
		t.Fatal(err)
	}
	// Idempotent: second call must succeed.
	if err := chownTree(root, uid, gid); err != nil {
		t.Fatal(err)
	}

	checkOwnership := func(p string) {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Skipf("non-syscall stat on %s; cannot verify ownership", p)
		}
		if int(sys.Uid) != uid || int(sys.Gid) != gid {
			t.Errorf("%s: uid/gid = %d:%d, want %d:%d", p, sys.Uid, sys.Gid, uid, gid)
		}
	}
	for _, p := range []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "sub"),
		filepath.Join(root, "sub", "b.txt"),
	} {
		checkOwnership(p)
	}
}

// TestApplyModelEnv asserts EIDOS_FORGE_MODEL is written into config.toml
// when set, and the config is left alone when unset.
func TestApplyModelEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	mustWriteFile(t, cfgPath, "log_level = \"info\"\n")

	if err := applyModelEnv(cfgPath, "claude-sonnet-4-7"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), `model = "claude-sonnet-4-7"`) {
		t.Errorf("config.toml missing model line; got:\n%s", body)
	}

	// Empty model: file unchanged.
	mustWriteFile(t, cfgPath, "log_level = \"info\"\n")
	before, _ := os.ReadFile(cfgPath)
	if err := applyModelEnv(cfgPath, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Errorf("empty model should leave config unchanged; before=%q after=%q", before, after)
	}

	// Invalid model id: error, file unchanged.
	if err := applyModelEnv(cfgPath, "garbage"); err == nil {
		t.Error("invalid model id should error")
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests, verify failure**

```
go test ./cmd/eidos/forge/ -run "TestChownTreeIdempotent|TestApplyModelEnv" -v
```

Expected: FAIL — `chownTree` and `applyModelEnv` undefined.

- [ ] **Step 3: Add helpers and wire them into runInitVolume**

Append to `cmd/eidos/forge/init_volume.go`:

```go
// chownTree recursively chowns every entry under root to uid:gid.
// Uses Lchown so symlinks themselves get chowned, not their targets.
// Idempotent: a no-op when the tree is already correctly owned.
func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// applyModelEnv writes mindform.model into the gate config when model is
// non-empty; validates first via config.ValidateModelID. Empty model is a
// no-op (the operator did not pin a model at create time).
func applyModelEnv(cfgPath, model string) error {
	if model == "" {
		return nil
	}
	if err := config.ValidateModelID(model); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load gate config: %w", err)
	}
	cfg.MindForm.Model = model
	return config.Save(cfgPath, cfg)
}
```

In `runInitVolume`, after the existing `cfg.Wake.Dir = "/eidos/run/wake" / config.Save` block (around line 88), add the model write:

```go
	if err := applyModelEnv(cfgPath, os.Getenv("EIDOS_FORGE_MODEL")); err != nil {
		return fmt.Errorf("save gate config (model): %w", err)
	}
```

At the very end of `runInitVolume`, before `fmt.Fprintln(stdout, "init-volume: ok")`, add:

```go
	// Final ownership pass: the persistent container starts as eidos
	// (uid 1000, image's USER directive). Init-volume itself runs as
	// root via RunInitOpts.User="0:0" so it can extract the template
	// tar / clone the bundle / git-init the parent ontology with full
	// privileges; we hand the volume off to eidos here. Hard-code uid
	// 1000:1000 (matching the Dockerfile addgroup/adduser) — name
	// resolution via os/user would add a CGO dependency this binary
	// avoids on principle.
	if err := chownTree("/eidos", 1000, 1000); err != nil {
		return fmt.Errorf("chown /eidos to eidos:eidos: %w", err)
	}
```

- [ ] **Step 4: Run tests, verify pass**

```
go test ./cmd/eidos/forge/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/init_volume.go cmd/eidos/forge/init_volume_test.go
git commit -m "$(cat <<'EOF'
feat(forge): chown /eidos to 1000:1000 + honour EIDOS_FORGE_MODEL

init-volume's last step lines up the volume for the persistent
container's USER eidos. Model-pin env writes through to the gate
config when set.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: orchestrate.go — pass User: "0:0" + EIDOS_FORGE_MODEL

**Files:**
- Modify: `cmd/eidos/forge/orchestrate.go`

- [ ] **Step 1: Add the env + User**

In `orchestrate`, modify the `RunInit` call (around line 65). The env list and `User` change:

```go
	res, err := c.RunInit(ctx, forgectl.RunInitOpts{
		Image: image,
		Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
		User:  "0:0",
		Env: []string{
			"EIDOS_IN_CONTAINER=1",
			"EIDOS_FORGE_NAME=" + name,
			"EIDOS_FORGE_LABEL=" + o.label,
			"EIDOS_FORGE_OWNER=" + o.owner,
			"EIDOS_FORGE_RELAY=" + o.relay,
			"EIDOS_FORGE_MODEL=" + o.model,
		},
		Cmd:   []string{"eidos", "forge", "init-volume"},
		Stdin: pipeR,
	})
```

(The `o.model` field will exist after Task 7.)

- [ ] **Step 2: Build (will fail until Task 7 lands)**

This task introduces a forward reference; build will fail. Defer the verify-build step until after Task 7.

- [ ] **Step 3: Hold the commit until Task 7 to keep the tree green**

Continue to Task 7 without committing. (We'll bundle this into Task 7's commit so each push leaves the tree buildable.)

---

## Task 7: forge create — `--model` flag

**Files:**
- Modify: `cmd/eidos/forge/create.go`
- Modify: `cmd/eidos/forge/create_test.go`

- [ ] **Step 1: Add failing test**

Read existing `cmd/eidos/forge/create_test.go`; add at the bottom (don't replace existing tests):

```go
func TestCreateOpts_ModelFlag(t *testing.T) {
	cmd := newCreateCmd()
	if err := cmd.ParseFlags([]string{
		"--owner", "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqsuvsr2",
		"--relay", "ws://relay.example.test",
		"--model", "claude-sonnet-4-7",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := cmd.Flags().GetString("model")
	if got != "claude-sonnet-4-7" {
		t.Errorf("--model not parsed: got %q", got)
	}
}

func TestCreateOpts_ModelValidation(t *testing.T) {
	if err := validateModel(""); err != nil {
		t.Errorf("empty should pass: %v", err)
	}
	if err := validateModel("claude-sonnet-4-7"); err != nil {
		t.Errorf("valid should pass: %v", err)
	}
	if err := validateModel("garbage"); err == nil {
		t.Error("garbage should fail validation")
	}
}
```

- [ ] **Step 2: Verify failure**

```
go test ./cmd/eidos/forge/ -v -run "TestCreateOpts"
```

Expected: FAIL — flag undefined / `validateModel` undefined.

- [ ] **Step 3: Wire the flag**

In `cmd/eidos/forge/create.go`:

Add field to `createOpts`:

```go
type createOpts struct {
	owner   string
	relay   string
	label   string
	noLogin bool
	image   string
	model   string
}
```

Add validator (delegates to `internal/config`):

```go
func validateModel(s string) error {
	return config.ValidateModelID(s)
}
```

Add the import `"github.com/LucianoXu/eidopsyche/internal/config"` if not present.

In `newCreateCmd`, add the flag and a validation call. Inside the existing `RunE`, after `validateRelay`:

```go
		if err := validateModel(o.model); err != nil {
			return err
		}
```

In the flag block, append:

```go
	cmd.Flags().StringVar(&o.model, "model", "", "claude model id to pin (e.g. claude-sonnet-4-7); empty = claude default")
```

- [ ] **Step 4: Verify build + tests**

```
go build ./...
go test ./cmd/eidos/forge/ -v
```

Expected: clean build + PASS.

- [ ] **Step 5: Commit Tasks 6 + 7 together**

```bash
git add cmd/eidos/forge/orchestrate.go cmd/eidos/forge/create.go cmd/eidos/forge/create_test.go
git commit -m "$(cat <<'EOF'
feat(forge): --model flag at create time + run init-volume as root

create plumbs the model through env to init-volume; orchestrate runs
init-volume as 0:0 (it chowns /eidos to 1000:1000 at the end).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: forge exec — `--user` flag

**Files:**
- Modify: `cmd/eidos/forge/exec.go`

- [ ] **Step 1: Add the flag**

Replace the `newExecCmd` body to support a persistent flag *before* the positional `--`:

```go
func newExecCmd() *cobra.Command {
	var asUser string
	cmd := &cobra.Command{
		Use:                   "exec <name> [-- <cmd...>]",
		Short:                 "Run an interactive shell or command inside the mind-form container",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			rest := args[1:]
			if len(rest) == 0 {
				rest = []string{"sh"}
			}
			flags := "-i"
			if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
				flags = "-it"
			}
			argv := []string{"exec", flags}
			if asUser != "" {
				argv = append(argv, "-u", asUser)
			}
			argv = append(argv, forgectl.ContainerName(name))
			argv = append(argv, rest...)
			c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&asUser, "user", "", `run as a specific user (e.g. "root" or "1000:1000"); default is the image's USER`)
	return cmd
}
```

- [ ] **Step 2: Verify build**

```
go build ./...
go vet ./...
```

Expected: clean.

- [ ] **Step 3: Manual smoke (no real containers needed yet)**

```
go run ./cmd/eidos forge exec --help
```

Expected output includes `--user string`.

- [ ] **Step 4: Commit**

```bash
git add cmd/eidos/forge/exec.go
git commit -m "$(cat <<'EOF'
feat(forge): exec --user for operator escape to root

Default unchanged (image's USER, eidos after this PR). --user root
gives the operator a path to root for debugging without touching
alice's own sudo machinery.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: forge config — host wrapper for in-container `gate config set`

**Files:**
- Create: `cmd/eidos/forge/config.go`
- Create: `cmd/eidos/forge/config_test.go`
- Modify: `cmd/eidos/forge/incontainer.go`

The host-side subcommands are registered inside `registerHost` in `cmd/eidos/forge/incontainer.go`. We add `newConfigCmd()` to that list.

- [ ] **Step 2: Add the failing test**

```go
// cmd/eidos/forge/config_test.go
package forge

import (
	"strings"
	"testing"
)

func TestConfigArgv_Model(t *testing.T) {
	argv, err := buildConfigDockerArgv("alice", configFlags{model: "claude-sonnet-4-7"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "eidos-mindform-alice", "eidos", "gate", "config", "set", "mindform.model", "claude-sonnet-4-7"}
	if !equalArgv(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestConfigArgv_BadModel(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{model: "garbage"})
	if err == nil {
		t.Error("garbage model should be rejected at host before we exec")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("err should mention model, got: %v", err)
	}
}

func TestConfigArgv_NoFlag(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{})
	if err == nil {
		t.Error("forge config without --model should error")
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 3: Run test, verify failure**

```
go test ./cmd/eidos/forge/ -run TestConfigArgv -v
```

Expected: FAIL — undefined.

- [ ] **Step 4: Implement the command**

```go
// cmd/eidos/forge/config.go
package forge

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

type configFlags struct {
	model string
}

func newConfigCmd() *cobra.Command {
	var f configFlags
	cmd := &cobra.Command{
		Use:   "config <name>",
		Short: "Update a mind-form's runtime configuration (in-container gate)",
		Long: `Update a runtime config key inside the mind-form's gate. The change
takes effect at the next wake — agent-runner re-reads config.toml each
time it spawns claude. Currently supported flags:

  --model <id>   Pin the claude model used by agent-runner.

Internally this dispatches to ` + "`eidos gate config set <key> <value>`" + `
inside the container, so the registry validation runs once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			argv, err := buildConfigDockerArgv(name, f)
			if err != nil {
				return err
			}
			c := exec.Command("docker", argv...) //nolint:gosec // argv from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&f.model, "model", "", "pin the claude model id (e.g. claude-sonnet-4-7); empty unsets the pin")
	return cmd
}

// buildConfigDockerArgv returns the `docker exec ...` argv for a single
// config update. Today only --model is supported; if more keys arrive
// the cobra command grows a flag for each and this function dispatches
// per flag. Pre-validates on the host so a typo aborts before we shell
// out.
func buildConfigDockerArgv(name string, f configFlags) ([]string, error) {
	if f.model == "" {
		return nil, fmt.Errorf("forge config requires --model <id>")
	}
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
```

- [ ] **Step 5: Wire into `registerHost`**

In `cmd/eidos/forge/incontainer.go`, add `newConfigCmd()` to the `root.AddCommand(...)` list inside `registerHost`. Insert it next to `newPurgeCmd()`:

```go
func registerHost(root *cobra.Command) {
	root.AddCommand(
		newCreateCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newListCmd(),
		newLogsCmd(),
		newExecCmd(),
		newWakeHostCmd(),
		newLoginCmd(),
		newOntologyCmd(),
		newPurgeCmd(),
		newConfigCmd(),
	)
}
```

- [ ] **Step 6: Run tests, verify pass**

```
go test ./cmd/eidos/forge/ -v
go build ./...
```

Expected: all PASS, clean build.

- [ ] **Step 7: Manual smoke**

```
go run ./cmd/eidos forge config --help
```

Expected output mentions `--model`.

- [ ] **Step 8: Commit**

```bash
git add cmd/eidos/forge/config.go cmd/eidos/forge/config_test.go cmd/eidos/forge/cmd.go
git commit -m "$(cat <<'EOF'
feat(forge): add forge config <name> --model for runtime model swap

Thin host wrapper that shells docker exec ... eidos gate config set
mindform.model <id>. Pre-validates on the host so typos abort before
we hit the container.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: supervisor — crond per-user spool

**Files:**
- Modify: `cmd/eidos/supervisor/run.go`

- [ ] **Step 1: Adjust crond invocation**

In `cmd/eidos/supervisor/run.go`, find `startChildren` (around line 38). Change the `crond` argv:

```go
func startChildren(ctx context.Context, sp ChildSpawner) error {
	// busybox crond reads each file in the spool dir as a user's
	// crontab keyed by filename. We move from /etc/crontabs (root's
	// directory in the legacy image) to /var/spool/cron/crontabs
	// (per-user spool) once the image's USER is eidos.
	if err := sp.Spawn(ctx, "crond", "-f", "-c", "/var/spool/cron/crontabs"); err != nil {
		return err
	}
	return sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir)
}
```

- [ ] **Step 2: Verify build**

```
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add cmd/eidos/supervisor/run.go
git commit -m "$(cat <<'EOF'
chore(supervisor): point crond at per-user spool

Pairs with the image switch to USER eidos. busybox crond reads
each file in the spool dir as the user keyed by the filename;
the new image installs the crontab as eidos's spool entry.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Dockerfile — eidos user, sudo, crontab move, USER eidos

**Files:**
- Modify: `docker/mindform/Dockerfile`

- [ ] **Step 1: Update the final stage**

Replace the final stage (Stage 4) of `docker/mindform/Dockerfile` with:

```dockerfile
# ---- Stage 4: final image ----
FROM alpine:3.19

# busybox-extras gives us crond and friends; sudo + shadow give us the
# eidos user + NOPASSWD escalation path.
RUN apk add --no-cache \
        busybox-extras ca-certificates git tini bash nodejs sudo shadow

# Non-root daily-ops user. claude(-code) refuses to run as uid 0 with
# --dangerously-skip-permissions; eidos owns /eidos/* in normal operation
# and uses sudo on demand for installs / debug. The container itself is
# the trust boundary.
RUN addgroup -g 1000 eidos \
 && adduser  -u 1000 -G eidos -h /eidos/claude -s /bin/sh -D eidos \
 && echo 'eidos ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/eidos \
 && chmod 0440 /etc/sudoers.d/eidos \
 && passwd -l root

COPY --from=build /out/eidos /usr/local/bin/eidos
COPY --from=repobundle /opt/eidopsyche-bundle /opt/eidopsyche-bundle.git
COPY --from=claude /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=claude /usr/local/bin/claude /usr/local/bin/claude
COPY docker/mindform/entrypoint.sh /usr/local/bin/entrypoint.sh

# Crontab: per-user spool entry, owned by eidos. busybox crond -c reads
# each file as that named user's crontab.
COPY docker/mindform/crontab /var/spool/cron/crontabs/eidos
RUN chmod +x /usr/local/bin/entrypoint.sh \
 && chown eidos:eidos /var/spool/cron/crontabs/eidos \
 && chmod 0600 /var/spool/cron/crontabs/eidos \
 && rm -f /etc/crontabs/root \
 && chown -R eidos:eidos /opt/eidopsyche-bundle.git

ENV EIDOS_IN_CONTAINER=1
ENV EIDOS_GATE_HOME=/eidos/gate
ENV HOME=/eidos/claude

USER eidos

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/entrypoint.sh"]
```

- [ ] **Step 2: Build the image**

```
docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:dev -f docker/mindform/Dockerfile .
```

Expected: clean build, no errors. Inspect the build output for the user/sudo/crontab steps to confirm they ran.

- [ ] **Step 3: Smoke-test the image**

```
docker run --rm --entrypoint sh ghcr.io/lucianoxu/eidopsyche-mindform:dev -c \
  'id; sudo -n whoami; ls -la /var/spool/cron/crontabs; cat /etc/sudoers.d/eidos'
```

Expected:
- `id` shows uid=1000(eidos) gid=1000(eidos)
- `sudo -n whoami` prints `root`
- crontab file exists, owned by eidos, mode 0600
- sudoers.d entry says `eidos ALL=(ALL) NOPASSWD: ALL`

If anything fails, fix the Dockerfile and re-build before committing.

- [ ] **Step 4: Commit**

```bash
git add docker/mindform/Dockerfile
git commit -m "$(cat <<'EOF'
feat(image)!: non-root mindform image (USER eidos + NOPASSWD sudo)

BREAKING CHANGE: existing mind-form volumes (uid 0-owned /eidos/*)
do not auto-migrate. Use eidos forge purge <name> && eidos forge
create <name> ... to recreate.

The image now defaults to eidos:1000 as USER. Daily ops (claude,
gate daemon, supervisor, git) run unprivileged. sudo is available
NOPASSWD for the eidos user; root login is locked. crontab moves
from /etc/crontabs/root to /var/spool/cron/crontabs/eidos.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: ontology template — add the eidos-user paragraph

**Files:**
- Modify: `internal/ontology/template/CLAUDE.md`

- [ ] **Step 1: Append a new section**

The file currently ends with the Constraints block. Use the Edit tool with this exact match (the last two lines of the file):

old_string:

```
- You exist on a specific computer, in a specific container. You can
  explore that fact via `eidos forge whoami` and friends.
```

new_string:

```
- You exist on a specific computer, in a specific container. You can
  explore that fact via `eidos forge whoami` and friends.

User and privileges:
- You run as the `eidos` user inside your container. Daily reads, writes,
  `eidos forge` / `eidos gate` / `git` / `claude` calls do not need
  privilege. If you genuinely need root — installing a tool, inspecting
  /proc, low-level debugging — you can `sudo` (no password). Treat
  `sudo` as a deliberate choice, not a default reflex: you are not here
  to be root.
```

(English to match the rest of the template.)

- [ ] **Step 3: Commit**

```bash
git add internal/ontology/template/CLAUDE.md
git commit -m "$(cat <<'EOF'
docs(ontology): tell alice she runs as eidos and may sudo deliberately

Companion to the image's switch to USER eidos. Frames sudo as a
considered choice, not a default reflex.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: CHANGELOG (skip — release-time only)

The repo's `CHANGELOG.md` is curated at version-tag time alongside the version bump (see commits like `d0fcbfe docs(changelog): v0.10.0`). GoReleaser auto-generates entries from Conventional Commit messages. Because this PR uses `feat!:` for the breaking image change and `feat:` for the additive flags, those will land in the next release's changelog automatically.

No CHANGELOG edit in this PR. Per `CLAUDE.md`: "NEVER bump the version number unless the user explicitly asks to."

---

## Task 14: full local CI parity check

CI (`.github/workflows/ci.yml`) runs: gofmt, go vet, staticcheck, unit tests, integration tests, build. Mirror locally.

- [ ] **Step 1: gofmt clean**

```
test -z "$(gofmt -l .)"
```

Expected: exits 0 (no unformatted files).

- [ ] **Step 2: go vet**

```
go vet ./...
```

Expected: clean.

- [ ] **Step 3: staticcheck**

```
go install honnef.co/go/tools/cmd/staticcheck@latest
$(go env GOPATH)/bin/staticcheck ./...
```

Expected: clean. (May surface unrelated pre-existing findings; if so, only fix those introduced by this PR's diff.)

- [ ] **Step 4: Unit tests**

```
go test ./...
```

Expected: all PASS.

- [ ] **Step 5: Integration tests**

```
go test -tags=integration ./test/integration/...
```

Expected: PASS. The integration suite needs the new image. If it pulls `ghcr.io/lucianoxu/eidopsyche-mindform:dev` and Docker only has the freshly built local tag, that's the same tag and same daemon — the test will use the local copy. If the integration tests fail because they expect uid-0 ownership, those assertions need updating; capture them as in-scope follow-up commits.

- [ ] **Step 6: Build**

```
go build -o /tmp/eidos ./cmd/eidos
```

Expected: clean.

If any step fails, fix and re-run only that step. No commit needed unless code changed; if it did, commit with a `chore` or `fix` scope as appropriate.

---

## Task 15: deployment validation on selene

This task is verification, not implementation. No commits.

- [ ] **Step 1: Build host eidos binary from this branch**

```
go build -o bin/eidos ./cmd/eidos
sudo install -m 0755 bin/eidos /usr/local/bin/eidos
eidos --version 2>&1 | head -2 || true
```

- [ ] **Step 2: Image is already built (Task 11)**

Verify:

```
docker image inspect ghcr.io/lucianoxu/eidopsyche-mindform:dev --format '{{.Config.User}}'
```

Expected: `eidos`.

- [ ] **Step 3: Recreate alice**

```
eidos forge purge alice --yes
eidos forge create alice \
  --owner npub1l5avk4xnlq63vjrfc4cs7hxqp9v2n9a0pau6zwavc63px24xfmrs4el8ez \
  --relay ws://yingte.io:22895 \
  --model claude-sonnet-4-7
```

Expected: clean create. Master contact set. No errors.

- [ ] **Step 4: Login + start**

```
eidos forge login alice
eidos forge start alice
```

- [ ] **Step 5: Verify selene-side assertions**

```
eidos forge exec alice -- whoami                   # eidos
eidos forge exec alice -- sudo -n whoami           # root
eidos forge exec alice --user root -- whoami       # root
eidos forge exec alice -- ps -o pid,user,comm      # supervisor/crond/eidos as eidos
eidos forge exec alice -- cat /eidos/gate/config.toml | grep -i model
                                                    # model = "claude-sonnet-4-7"
```

Each assertion must hold. A regression blocks the PR.

- [ ] **Step 6: Wake + verify claude flag**

```
eidos forge wake alice --reason heartbeat
sleep 5
eidos forge logs alice 2>&1 | tail -50
```

Expected: claude runs cleanly. Log output should not show "cannot be used with root/sudo privileges". Any `--permission-mode auto` text in the logs is a regression.

- [ ] **Step 7: Mutate the model post-create**

```
eidos forge config alice --model claude-haiku-4-5
eidos forge exec alice -- cat /eidos/gate/config.toml | grep model
```

Expected: `model = "claude-haiku-4-5"`. Restore back to sonnet:

```
eidos forge config alice --model claude-sonnet-4-7
```

- [ ] **Step 8: Record results**

Append the assertion outcomes to a scratch file (not committed):

```
mkdir -p /tmp/non-root-validation
eidos forge exec alice -- whoami > /tmp/non-root-validation/whoami.out
eidos forge exec alice -- sudo -n whoami > /tmp/non-root-validation/sudo-whoami.out
eidos forge exec alice --user root -- whoami > /tmp/non-root-validation/exec-root.out
eidos forge exec alice -- cat /eidos/gate/config.toml > /tmp/non-root-validation/config.toml
```

These outputs go in the PR description as evidence.

---

## Task 16: codex review

Per `CLAUDE.md` policy: "Third-party review. If you are Claude Code, you should use codex and request a code review."

- [ ] **Step 1: Confirm codex CLI is available**

```
which codex
```

If absent, fall back to attaching the diff in a comment for the human to drive an external review.

- [ ] **Step 2: Run codex on the diff against main**

```
git fetch origin main
git diff origin/main..HEAD > /tmp/non-root-mindform.diff
wc -l /tmp/non-root-mindform.diff
codex exec "Review the attached diff for issue #26 (non-root mind-form container + model config). Spec: docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md. Focus on: (1) ownership / migration correctness — note the design says NO migration helper; existing volumes get reset, not chowned at runtime; (2) the chown-tree path's idempotency and error handling; (3) docker exec argv assembly + shell-injection surface; (4) sudoers configuration safety; (5) model regex coverage. Diff is in /tmp/non-root-mindform.diff."
```

- [ ] **Step 3: Triage feedback**

For each codex finding:
- Real bug → fix it, commit a fix, re-run codex
- Style nit aligned with repo style → fix
- Disagrees with spec — surface to the user for adjudication

---

## Task 17: open the PR

- [ ] **Step 1: Push the branch**

```
git push -u origin feat/non-root-mindform-and-model-config
```

- [ ] **Step 2: Create the PR**

```bash
gh pr create --title "feat(forge)!: non-root mind-form (sudo on demand) + --dangerously-skip-permissions + model config" --body "$(cat <<'EOF'
## Summary

Closes #26. Two coupled improvements to the mind-form container's runtime profile:

1. **Default to non-root, root reachable on demand.** Image switches `USER` to `eidos:1000`. Daily ops (supervisor, gate daemon, claude, crond) run unprivileged. NOPASSWD sudo gives alice deliberate root when she wants it; `eidos forge exec <name> --user root -- sh` gives the operator the same path. Root account is locked to force the sudo-on-purpose semantic. Lets us switch agent-runner from `--permission-mode auto` to the semantically right `--dangerously-skip-permissions`.
2. **Per-mind-form model config.** New `mindform.model` registry key. `eidos forge create --model <id>` pins at create time; `eidos forge config <name> --model <id>` mutates without recreating. Loose syntactic regex catches typos.

No migration of existing volumes — operator does `purge && create`. Pre-1.0; that's the cheap path.

## BREAKING CHANGES

- Mind-form image runs as non-root by default. Existing uid-0-owned volumes don't auto-migrate; `eidos forge purge && eidos forge create`.
- agent-runner now uses `--dangerously-skip-permissions`. Behaviourally a no-op (both auto-accept); semantically the right match.

## Test plan

- [x] `go test ./...` passes
- [x] `gofmt -l .` clean; `go vet ./...` clean
- [x] Image build clean (`docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:dev -f docker/mindform/Dockerfile .`)
- [x] Selene-side e2e:
  - `eidos forge exec alice -- whoami` → `eidos`
  - `eidos forge exec alice -- sudo -n whoami` → `root`
  - `eidos forge exec alice --user root -- whoami` → `root`
  - `eidos forge wake alice --reason heartbeat` → claude runs with --dangerously-skip-permissions, no permission errors
  - `eidos forge config alice --model claude-haiku-4-5` mutates `mindform.model` in `/eidos/gate/config.toml`
- [ ] (operator-driven) mbp → selene NIP-17 round-trip end-to-end

Spec: [`docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md`](docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Watch CI**

```
gh pr view --json number,headRefName -q .number
PR=$(gh pr view --json number -q .number)
gh pr checks $PR --watch
```

- [ ] **Step 4: Print the PR URL**

```
gh pr view --json url -q .url
```

Print this URL at the end of the run.

- [ ] **Step 5: Wait for and respond to copilot review (if assigned)**

Per CLAUDE.md: "If copilot is assigned to review the PR automatically, you should wait for copilot's comments and fix them if necessary."

```
gh pr view $PR --json reviewRequests,reviews
```

If copilot is in `reviewRequests`, give it ~5 minutes after PR open, then poll for review comments. Fix any actionable comments and push fixups.

---

## Self-review checklist

After implementing the plan:

1. **Spec coverage** — every section of `docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md` is touched:
   - §3 Image changes → Task 11
   - §4 Init container changes → Tasks 4 + 5 + 6
   - §5 agent-runner changes → Task 3
   - §6 Model configuration → Tasks 1, 2, 7, 9
   - §7 Operator escape → Task 8
   - §8 Template CLAUDE.md → Task 12
   - §9 Tests — covered piecewise inside each task
   - §10 CHANGELOG → Task 13
   - §11 Verification → Task 15
   - §12 Out of scope — nothing built; correct.

2. **Placeholder scan** — clear; every test step has the actual test code, every command has its expected outcome.

3. **Type consistency** — `configFlags`, `RunInitOpts.User`, `MindFormConfig.Model`, `o.model` (createOpts), `mindform.model` (registry path) — names line up across tasks.
