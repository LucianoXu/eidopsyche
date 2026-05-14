# Mindform Workspaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator bind host directories into a mind-form's container at `/workspace/<name>/`, manageable via `eidos forge workspace add/remove/list` and applied via `eidos forge restart`, with the `/eidos` ontology preserved across the recreate cycle.

**Architecture:** Per-mind-form list of bind mounts stored in host gate `config.toml` under `[forge.<name>]`. New IPC verbs `forge.workspace.add/remove/list` are the sole write/read surface; CLI and dashboard adapters wrap them. A new `forge restart` recreates the container (`stop → docker inspect for image → rm → create with new mount list + preserved image → start`); the `/eidos` named volume survives the cycle. Mind-form discovers workspaces by `ls /workspace/`; an in-container `state.get forge.workspaces` reads `/proc/self/mounts`.

**Tech Stack:** Go, Docker SDK (`github.com/docker/docker/client`), BurntSushi/toml, Cobra. New: none.

**Spec:** `docs/superpowers/specs/2026-05-14-mindform-workspaces-design.md`.

**Sibling spec (relationship):** `docs/superpowers/specs/2026-05-14-mindform-upgrade-design.md` introduces an `internal/forge/` package extraction. This plan **targets current `main`** (no `internal/forge/` package yet). If the upgrade plan merges first, the only mechanical rebase is: edits this plan makes to `cmd/eidos/forge/orchestrate.go` shift to `internal/forge/create.go`; the per-mind-form mutex this plan introduces (Task 9) is dropped in favour of the one upgrade adds. No logic changes.

---

## File map

**New:**
- `internal/config/workspace.go` — `WorkspaceMount` type, validation helpers, dangerous-path heuristic.
- `internal/config/workspace_test.go`
- `internal/daemon/methods_forge_workspace.go` — `forge.workspace.add/remove/list` handlers.
- `internal/daemon/methods_forge_workspace_test.go`
- `internal/daemon/methods_state_workspaces_container.go` — container-side `state.get forge.workspaces` (parses `/proc/self/mounts`).
- `internal/daemon/methods_state_workspaces_container_test.go`
- `cmd/eidos/forge/workspace.go` — `eidos forge workspace add/remove/list` cobra commands.
- `cmd/eidos/forge/workspace_test.go`
- `cmd/eidos/forge/restart.go` — `eidos forge restart` command.
- `cmd/eidos/forge/restart_test.go`

**Modified:**
- `internal/config/config.go` — add `Forge map[string]ForgeMindForm` to `Config`; new `ForgeMindForm` struct.
- `internal/forgectl/docker.go` — `CreateOpts.Mount Mount` → `CreateOpts.Mounts []Mount`; add `MountType`, `Mount.ReadOnly`; add `ContainerInspectImage(name) (string, error)`.
- `internal/forgectl/lifecycle.go` — adapt to the slice-shaped `Mounts`.
- `cmd/eidos/forge/orchestrate.go` — assemble mounts from `cfg.Forge[name].Workspaces` in create-container step.
- `cmd/eidos/forge/status.go` — render `pending_restart: workspaces changed` line when desired ≠ actual.
- `cmd/eidos/forge/cmd.go` — register `workspace` subcommand group and `restart` command.
- `internal/daemon/dashboard_adapter.go` — adapter methods for `forge.workspace.add/remove/list`.
- `internal/prompts/assets/system1-instructions.txt` — workspaces section.
- `docs/USAGE.md` — Workspaces subsection.
- `docs/specs/SPEC.md` — annotate `:137` with `/workspace/` carve-out.
- `docker/mindform/AGENTS.md` — one-line note about `/workspace/`.

---

## Task 1: `WorkspaceMount` type and TOML round-trip

**Files:**
- Create: `internal/config/workspace.go`
- Create: `internal/config/workspace_test.go`
- Modify: `internal/config/config.go` (add `Forge` map to `Config`)

- [ ] **Step 1: Write failing test for TOML round-trip**

In `internal/config/workspace_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestForgeWorkspacesRoundTrip writes a config with workspaces, loads
// it, and confirms the structure round-trips.
func TestForgeWorkspacesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
state_dir = "/tmp/eidos"
log_level = "info"

[forge.alice]
workspaces = [
  { name = "proj-x", host_path = "/home/op/code/project-x", mode = "rw" },
  { name = "photos", host_path = "/home/op/Pictures",       mode = "ro" },
]

[forge.bob]
workspaces = [
  { name = "proj-x", host_path = "/home/op/code/project-x" },
]
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]ForgeMindForm{
		"alice": {Workspaces: []WorkspaceMount{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
			{Name: "photos", HostPath: "/home/op/Pictures", Mode: "ro"},
		}},
		"bob": {Workspaces: []WorkspaceMount{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: ""},
		}},
	}
	if !reflect.DeepEqual(cfg.Forge, want) {
		t.Fatalf("Forge mismatch\nwant: %#v\n got: %#v", want, cfg.Forge)
	}
}

// TestForgeWorkspacesEmptyConfig confirms a config with no [forge.*]
// blocks loads with a nil Forge map (no panics, no extra allocations).
func TestForgeWorkspacesEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("log_level=\"info\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Forge) != 0 {
		t.Fatalf("expected empty Forge map, got %#v", cfg.Forge)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails to compile**

Run: `go test ./internal/config/ -run TestForgeWorkspaces -v`
Expected: build error — `ForgeMindForm`, `WorkspaceMount`, `Config.Forge` undefined.

- [ ] **Step 3: Add types**

Create `internal/config/workspace.go`:

```go
package config

// WorkspaceMount is one entry in a mind-form's bind-mount list. The
// container target is always /workspace/<Name>/; only Name, HostPath,
// and Mode are persisted.
type WorkspaceMount struct {
	Name     string `toml:"name"`
	HostPath string `toml:"host_path"`
	// Mode is "ro" or "rw". Empty in config.toml resolves to "rw" at
	// read time via WorkspaceMount.EffectiveMode; we keep the persisted
	// zero value empty so an absent field round-trips cleanly.
	Mode string `toml:"mode,omitempty"`
}

// ForgeMindForm is the host-gate per-mind-form section
// [forge.<name>]. Only Workspaces lives here today; future per-mind-form
// host-side knobs join this struct.
type ForgeMindForm struct {
	Workspaces []WorkspaceMount `toml:"workspaces,omitempty"`
}

// EffectiveMode returns "rw" when Mode is empty, otherwise the
// persisted value. Used by every reader that needs to materialize the
// mount; do not re-implement.
func (w WorkspaceMount) EffectiveMode() string {
	if w.Mode == "" {
		return "rw"
	}
	return w.Mode
}
```

In `internal/config/config.go`, add to the `Config` struct (alphabetical-by-key order; place after `Dashboard` to match TOML key order):

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
	Heartbeat HeartbeatConfig `toml:"heartbeat"`

	// Forge is the host-gate per-mind-form section. Keyed by mind-form
	// name. nil/empty on the in-container gate (mind-forms don't
	// configure their own bind mounts). See WorkspaceMount.
	Forge map[string]ForgeMindForm `toml:"forge,omitempty"`
}
```

- [ ] **Step 4: Run tests to confirm pass**

Run: `go test ./internal/config/ -run TestForgeWorkspaces -v`
Expected: both tests PASS.

Also run: `go test ./internal/config/ -v` to confirm no existing test broke.
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/workspace.go internal/config/workspace_test.go internal/config/config.go
git commit -m "feat(config): add per-mindform [forge.<name>].workspaces schema

Adds WorkspaceMount and ForgeMindForm types and a Forge map[string]ForgeMindForm
field on Config. Empty Mode defaults to rw via EffectiveMode(). Container-side
gates leave Forge nil since they do not configure their own mounts."
```

---

## Task 2: Pure validation helpers

**Files:**
- Modify: `internal/config/workspace.go`
- Modify: `internal/config/workspace_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/config/workspace_test.go`:

```go
func TestValidateWorkspaceName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"proj-x", true},
		{"a", true},
		{"abc123", true},
		{"a-b-c", true},
		{"", false},
		{"-foo", false},     // leading hyphen
		{"Foo", false},      // uppercase
		{"foo_bar", false},  // underscore
		{"foo/bar", false},  // slash
		{"..", false},
	}
	for _, c := range cases {
		got := ValidateWorkspaceName(c.in) == nil
		if got != c.want {
			t.Errorf("ValidateWorkspaceName(%q) ok=%v want %v", c.in, got, c.want)
		}
	}
}

func TestValidateWorkspaceMode(t *testing.T) {
	if err := ValidateWorkspaceMode(""); err != nil {
		t.Errorf("empty mode should be valid (defaults to rw), got %v", err)
	}
	if err := ValidateWorkspaceMode("rw"); err != nil {
		t.Error(err)
	}
	if err := ValidateWorkspaceMode("ro"); err != nil {
		t.Error(err)
	}
	if err := ValidateWorkspaceMode("RW"); err == nil {
		t.Error("RW should be rejected (case-sensitive)")
	}
	if err := ValidateWorkspaceMode("write"); err == nil {
		t.Error("write should be rejected")
	}
}

func TestValidateWorkspaceHostPath(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.Mkdir(regular, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateWorkspaceHostPath(regular); err != nil {
		t.Errorf("regular dir: %v", err)
	}
	if err := ValidateWorkspaceHostPath("relative/path"); err == nil {
		t.Error("relative path should be rejected")
	}
	if err := ValidateWorkspaceHostPath("/tmp/../etc"); err == nil {
		t.Error("path containing .. should be rejected")
	}
	if err := ValidateWorkspaceHostPath("/no/such/path/" + t.Name()); err == nil {
		t.Error("nonexistent path should be rejected")
	}
	if err := ValidateWorkspaceHostPath(file); err == nil {
		t.Error("file (not directory) should be rejected")
	}
}

func TestDangerousHostPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = should produce a warning
	}{
		{"/", true},
		{"/proc", true},
		{"/proc/cpuinfo", true},
		{"/sys", true},
		{"/dev", true},
		{"/etc", true},
		{"/var/run/docker.sock", true},
		{"/tmp/docker.sock", true},
		{"/home/op/.ssh", true},
		{"/home/op/.ssh/keys", true},
		{"/home/op/.config/eidos", true},
		{"/home/op/.config/eidos/config.toml", true},
		{"/home/op/code/project-x", false},
		{"/home/op/Pictures", false},
		{"/var/log", false},
	}
	for _, c := range cases {
		got := DangerousHostPath(c.in) != ""
		if got != c.want {
			t.Errorf("DangerousHostPath(%q) warned=%v want %v", c.in, got, c.want)
		}
	}
}
```

Run: `go test ./internal/config/ -run TestValidateWorkspace -v`
Expected: build fails — symbols not defined.

- [ ] **Step 2: Implement helpers**

Append to `internal/config/workspace.go`:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var workspaceNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateWorkspaceName enforces the persisted name regex. The name
// becomes the last segment of the container path /workspace/<name>/.
func ValidateWorkspaceName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name must not be empty")
	}
	if !workspaceNameRE.MatchString(name) {
		return fmt.Errorf("workspace name %q does not match %s", name, workspaceNameRE.String())
	}
	return nil
}

// ValidateWorkspaceMode permits "", "ro", "rw". Empty resolves to "rw"
// at read time (see EffectiveMode). Case-sensitive.
func ValidateWorkspaceMode(mode string) error {
	switch mode {
	case "", "ro", "rw":
		return nil
	default:
		return fmt.Errorf("workspace mode %q: want \"ro\" or \"rw\"", mode)
	}
}

// ValidateWorkspaceHostPath stats the host path and enforces the
// absolute-clean-directory-exists rule. Callers should call this
// before persisting an add. Returned errors are operator-readable.
func ValidateWorkspaceHostPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("host_path %q must be absolute", p)
	}
	if filepath.Clean(p) != p {
		return fmt.Errorf("host_path %q must be cleaned (no .., no trailing slash, no double slashes)", p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("host_path %q: %w", p, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("host_path %q is not a directory", p)
	}
	return nil
}

// dangerousPathRules is the ordered list of (matcher, message) pairs
// the warning path uses. Order matters: docker.sock catches
// /var/run/docker.sock before /var/ would (it would not, but ordering
// keeps the special-case message first).
var dangerousPathRules = []struct {
	match func(string) bool
	msg   string
}{
	{
		match: func(p string) bool { return p == "/" },
		msg:   "mounting / exposes the entire host filesystem to the mind-form",
	},
	{
		match: func(p string) bool { return strings.HasSuffix(p, "/docker.sock") },
		msg:   "mounting the docker socket gives the mind-form full control over the host's docker daemon, including breaking out of its container",
	},
	{
		match: func(p string) bool { return strings.HasSuffix(p, "/.ssh") || strings.Contains(p, "/.ssh/") },
		msg:   "mounting an SSH key directory exposes private keys to the mind-form",
	},
	{
		match: func(p string) bool { return strings.HasSuffix(p, "/.config/eidos") || strings.Contains(p, "/.config/eidos/") },
		msg:   "mounting eidos host config; the mind-form will be able to modify other mind-forms' configurations",
	},
	{
		match: func(p string) bool {
			return p == "/proc" || strings.HasPrefix(p, "/proc/") ||
				p == "/sys" || strings.HasPrefix(p, "/sys/") ||
				p == "/dev" || strings.HasPrefix(p, "/dev/")
		},
		msg: "mounting a kernel virtual filesystem exposes host kernel state to the mind-form",
	},
	{
		match: func(p string) bool { return p == "/etc" || strings.HasPrefix(p, "/etc/") },
		msg:   "mounting host system configuration",
	},
}

// DangerousHostPath returns a non-empty warning message if p matches
// one of the dangerous-path heuristics, otherwise "". The IPC handler
// emits the message as a non-blocking warning so the operator can
// proceed but is alerted.
func DangerousHostPath(p string) string {
	for _, rule := range dangerousPathRules {
		if rule.match(p) {
			return rule.msg
		}
	}
	return ""
}
```

- [ ] **Step 3: Run tests to confirm pass**

Run: `go test ./internal/config/ -run TestValidateWorkspace -v -count=1`
Run: `go test ./internal/config/ -run TestDangerousHostPath -v`
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add internal/config/workspace.go internal/config/workspace_test.go
git commit -m "feat(config): add workspace validation helpers and dangerous-path heuristic

Pure functions: ValidateWorkspaceName (regex), ValidateWorkspaceMode (enum),
ValidateWorkspaceHostPath (abs + clean + exists + isdir), DangerousHostPath
(returns a warning message or empty string)."
```

---

## Task 3: UID-mismatch check helper

**Files:**
- Modify: `internal/config/workspace.go`
- Modify: `internal/config/workspace_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/config/workspace_test.go`:

```go
import (
	"syscall"
)

// TestHostPathOwnerUID exercises the helper against tempdirs the test
// process owns (uid == os.Geteuid() under any test runner). Distinct
// uid scenarios are tested in the IPC handler test with a stat-stub.
func TestHostPathOwnerUID(t *testing.T) {
	dir := t.TempDir()
	uid, err := HostPathOwnerUID(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := uint32(os.Geteuid())
	if uid != want {
		t.Errorf("HostPathOwnerUID(%q)=%d, want %d", dir, uid, want)
	}

	if _, err := HostPathOwnerUID("/no/such/path/" + t.Name()); err == nil {
		t.Error("nonexistent path should error")
	}
}

// TestUIDMismatchWarning verifies the format of the warning string
// the IPC handler should emit.
func TestUIDMismatchWarning(t *testing.T) {
	got := UIDMismatchWarning("/home/op/code/foo", 501)
	if !strings.Contains(got, "/home/op/code/foo") || !strings.Contains(got, "uid=501") || !strings.Contains(got, "uid=1000") {
		t.Errorf("warning missing key fields: %q", got)
	}
}

// Silence the syscall import lint when the helper compiles without it.
var _ = syscall.Stat_t{}
```

Run: `go test ./internal/config/ -run TestHostPathOwnerUID -v`
Expected: build fails.

- [ ] **Step 2: Implement helpers**

Append to `internal/config/workspace.go`:

```go
import (
	"syscall"
)

// MindFormContainerUID is the uid the eidos user owns inside the
// mind-form container; see docker/mindform/Dockerfile:73. Hard-coded
// because the Dockerfile is the source of truth.
const MindFormContainerUID uint32 = 1000

// HostPathOwnerUID returns the owning uid of p on the host. The
// daemon's add handler uses this to emit a uid-mismatch warning.
func HostPathOwnerUID(p string) (uint32, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", p, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat %s: cannot read owner uid (non-Unix?)", p)
	}
	return sys.Uid, nil
}

// UIDMismatchWarning composes the operator-facing warning when an
// rw-mode workspace's host path is not owned by uid 1000.
func UIDMismatchWarning(hostPath string, ownerUID uint32) string {
	return fmt.Sprintf(
		"host_path %s is owned by uid=%d, but the mind-form container runs as uid=%d. "+
			"Writes from the mind-form may fail with EACCES. "+
			"Run 'chown -R %d %s' to align, or pass --no-warn-uid to suppress.",
		hostPath, ownerUID, MindFormContainerUID, MindFormContainerUID, hostPath,
	)
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/config/ -run "TestHostPathOwnerUID|TestUIDMismatchWarning" -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/config/workspace.go internal/config/workspace_test.go
git commit -m "feat(config): add UID-mismatch detection for workspace host paths

HostPathOwnerUID stats the host path and returns its uid. UIDMismatchWarning
composes the operator-facing message used by the forge.workspace.add handler
when uid != 1000 on an rw mount."
```

---

## Task 4: `forgectl` — `Mounts []Mount` with type + read-only

**Files:**
- Modify: `internal/forgectl/docker.go`
- Modify: `internal/forgectl/lifecycle.go`
- Modify: `internal/forgectl/docker_test.go` (or create if absent)
- Modify: `cmd/eidos/forge/orchestrate.go` (call sites of `forgectl.CreateOpts`)

- [ ] **Step 1: Inspect current call sites**

Run: `grep -rn "CreateOpts{" --include='*.go' .`
Expected: hits in `cmd/eidos/forge/orchestrate.go` (the create-container step) and `internal/forgectl/lifecycle.go` (the WriteFile init container). Both currently use `Mount: forgectl.Mount{...}` singular.

- [ ] **Step 2: Update `Mount` and `CreateOpts` shape**

In `internal/forgectl/docker.go`, replace the `Mount` and `CreateOpts` definitions (currently lines ~72-86):

```go
// MountType distinguishes named-volume mounts from host-bind mounts.
type MountType string

const (
	MountVolume MountType = "volume"
	MountBind   MountType = "bind"
)

// Mount is one entry in a container's mount list.
type Mount struct {
	Type     MountType
	// Source is the volume name when Type=volume, or the host path when
	// Type=bind. The Docker SDK accepts either in the same field of
	// mount.Mount, so we mirror that here.
	Source   string
	Target   string
	ReadOnly bool // honoured for bind; ignored for volume
}

// CreateOpts is the subset of container create the forge orchestrator uses.
type CreateOpts struct {
	Name       string
	Image      string
	Mounts     []Mount   // was: Mount Mount
	Env        []string
	Entrypoint []string
	Cmd        []string
}
```

- [ ] **Step 3: Update `ContainerCreate` to translate the slice**

In the same file, replace the body of `realClient.ContainerCreate` (~lines 164-180). It currently does:
```go
Mounts: []mount.Mount{{ Type: mount.TypeVolume, Source: opts.Mount.VolumeName, Target: opts.Mount.Target }},
```

Change to:
```go
func (r *realClient) ContainerCreate(ctx context.Context, opts CreateOpts) error {
	mounts := make([]mount.Mount, 0, len(opts.Mounts))
	for _, m := range opts.Mounts {
		var t mount.Type
		switch m.Type {
		case MountVolume:
			t = mount.TypeVolume
		case MountBind:
			t = mount.TypeBind
		default:
			return fmt.Errorf("container create %s: mount type %q is not supported", opts.Name, m.Type)
		}
		mounts = append(mounts, mount.Mount{
			Type:     t,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	_, err := r.c.ContainerCreate(ctx,
		&container.Config{
			Image:      opts.Image,
			Env:        opts.Env,
			Entrypoint: opts.Entrypoint,
			Cmd:        opts.Cmd,
		},
		&container.HostConfig{
			Mounts: mounts,
		},
		nil, nil, opts.Name,
	)
	return err
}
```

Apply the same pattern to `RunInit` (~lines 225-260). It still takes a single `Mount`; update its translation block:

```go
host := &container.HostConfig{
	Mounts: []mount.Mount{{
		Type:   mount.TypeVolume,
		Source: opts.Mount.VolumeName,
		Target: opts.Mount.Target,
	}},
	AutoRemove: true,
}
```

Wait — `RunInitOpts.Mount` is a different struct (`Mount{VolumeName, Target}`). Leave `RunInitOpts.Mount` alone (the init container only ever needs the `/eidos` volume). `RunInit` keeps the old `Mount{VolumeName, Target}` translation. **Do not change `RunInitOpts`.**

- [ ] **Step 4: Add `ContainerInspectImage`**

In `internal/forgectl/docker.go`, in the `Client` interface (~line 16+), add:

```go
// ContainerInspectImage returns the image ref the container was
// created with (Config.Image). Used by forge restart to preserve the
// existing image across the stop→rm→create→start cycle (so a
// previous forge upgrade is not silently reverted).
ContainerInspectImage(ctx context.Context, name string) (string, error)
```

Add the realClient implementation after `ContainerInspectState`:

```go
func (r *realClient) ContainerInspectImage(ctx context.Context, name string) (string, error) {
	resp, err := r.c.ContainerInspect(ctx, name)
	if err != nil {
		return "", err
	}
	return resp.Config.Image, nil
}
```

- [ ] **Step 5: Fix the existing call site in `orchestrate.go`**

In `cmd/eidos/forge/orchestrate.go` around line 213, change:

```go
if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
	Name:  cont,
	Image: image,
	Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
}); err != nil {
```

to:

```go
if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
	Name:  cont,
	Image: image,
	Mounts: []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
	},
}); err != nil {
```

The pre-Task-9 form only mounts `/eidos`; Task 9 extends this site with the workspace list.

- [ ] **Step 6: Fix `lifecycle.go` call site**

Inspect `internal/forgectl/lifecycle.go` around line 44; it currently does `Mount: Mount{VolumeName: ...}` against the **old** singular field. But `lifecycle.go` uses `RunInitOpts.Mount`, not `CreateOpts.Mount` — verify with:

Run: `grep -n "Mount" internal/forgectl/lifecycle.go`

Expected: the Mount references are inside `RunInitOpts{Mount: ...}` literals. **`RunInitOpts.Mount` was not changed** in Step 3, so these call sites should still compile. If `grep` shows any `CreateOpts{Mount:` literal in this file, change `Mount:` → `Mounts: []Mount{...}` to match the new shape.

- [ ] **Step 7: Build the whole module**

Run: `go build ./...`
Expected: build passes. If any file outside `internal/forgectl/`, `cmd/eidos/forge/`, or `internal/firstcontact/` references `CreateOpts.Mount`, fix that call site the same way as Step 5.

- [ ] **Step 8: Write a test for `ContainerCreate` mount translation**

Add to `internal/forgectl/docker_test.go` (create if absent):

```go
package forgectl

import "testing"

// TestMountTypeRoundTrip is a smoke check on the public enums; the
// translation to the Docker SDK's mount.Type lives behind the SDK
// client and is exercised by the integration tests.
func TestMountTypeConstants(t *testing.T) {
	if MountVolume == MountBind {
		t.Fatal("MountVolume and MountBind must be distinct values")
	}
	if MountVolume != "volume" || MountBind != "bind" {
		t.Fatalf("unexpected enum values: volume=%q bind=%q", MountVolume, MountBind)
	}
}
```

Run: `go test ./internal/forgectl/ -v`
Expected: pass.

- [ ] **Step 9: Commit**

```bash
git add internal/forgectl/docker.go internal/forgectl/docker_test.go cmd/eidos/forge/orchestrate.go
git commit -m "refactor(forgectl): support multiple typed mounts on CreateOpts

CreateOpts.Mount (single) becomes Mounts []Mount with Type
(volume|bind) and ReadOnly. ContainerCreate translates the slice into
mount.Mount entries with the appropriate Docker SDK type.
Also adds ContainerInspectImage for the upcoming forge restart
image-preservation behaviour."
```

---

## Task 5: `forge.workspace.add` IPC handler

**Files:**
- Create: `internal/daemon/methods_forge_workspace.go`
- Create: `internal/daemon/methods_forge_workspace_test.go`
- Modify: `internal/ipc/protocol.go` (new error codes)

**Convention reminders (apply to Tasks 5/6/7):**
- Handler signature is `func(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error)` (`internal/daemon/handler.go:23`).
- Return values are `any` (typically the result struct), not `json.RawMessage` — the framework marshals.
- Return errors as `&ipc.Error{Code: ..., Message: ...}`; reuse constants from `internal/ipc/protocol.go` or add new ones.
- Tests call handlers **directly by Go function name**, not via a dispatcher: `forgeWorkspaceAdd(context.Background(), d, nil, raw)`. Pattern matches `internal/daemon/methods_state_test.go:33`.
- Test daemon construction uses the existing `newTestDaemon(t)` helper. Mind-form existence probes are part of `*Daemon`; add a `SeedMindForm(name string)` test helper next to `newTestDaemon` if absent.
- `d.Mutate(ctx, path, ctxScope, mutator)` accepts arbitrary path strings (precedent: `internal/daemon/methods_contact.go:116` uses `"contacts."+pk`). No registration in `internal/config/keys.go` is needed.

- [ ] **Step 1: Add error codes**

In `internal/ipc/protocol.go`, append to the `const ( ... )` block:

```go
ErrForgeNotFound      = "FORGE_NOT_FOUND"
ErrWorkspaceExists    = "WORKSPACE_EXISTS"
ErrWorkspaceNotFound  = "WORKSPACE_NOT_FOUND"
```

- [ ] **Step 2: Write failing tests**

Create `internal/daemon/methods_forge_workspace_test.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lucianoxu/eidopsyche/internal/config"
	"lucianoxu/eidopsyche/internal/ipc"
)

func TestForgeWorkspaceAdd_Persists(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()

	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "proj-x", "host_path": dir, "mode": "rw",
	})
	out, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	res := out.(forgeWorkspaceAddResult)
	if !res.PendingRestart {
		t.Errorf("want pending_restart=true; got %#v", res)
	}
	got := d.Config().Forge["alice"].Workspaces
	want := []config.WorkspaceMount{{Name: "proj-x", HostPath: dir, Mode: "rw"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("config not updated:\nwant %#v\n got %#v", want, got)
	}
}

func TestForgeWorkspaceAdd_DefaultsModeRW(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()

	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "proj-x", "host_path": dir,
	})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	got := d.Config().Forge["alice"].Workspaces[0]
	if got.Mode != "" {
		t.Errorf("Mode persisted as %q; want empty", got.Mode)
	}
	if got.EffectiveMode() != "rw" {
		t.Errorf("EffectiveMode=%q; want rw", got.EffectiveMode())
	}
}

func TestForgeWorkspaceAdd_RejectsBadName(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "Bad_Name", "host_path": dir,
	})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrInvalidParams {
		t.Errorf("want INVALID_PARAMS; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_RejectsBadPath(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "proj-x", "host_path": "relative/path",
	})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || !strings.Contains(ipcErr.Message, "absolute") {
		t.Errorf("want abs-path validation error; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_RejectsDuplicate(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir})
	if _, e := forgeWorkspaceAdd(context.Background(), d, nil, raw); e != nil {
		t.Fatal(e)
	}
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrWorkspaceExists {
		t.Errorf("want WORKSPACE_EXISTS; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_RejectsBadMode(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "proj-x", "host_path": dir, "mode": "wr",
	})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || !strings.Contains(ipcErr.Message, "mode") {
		t.Errorf("want mode validation error; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_DangerousPathWarn(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	raw, _ := json.Marshal(map[string]any{
		"mindform": "alice", "name": "etc", "host_path": "/etc", "mode": "ro",
	})
	out, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	res := out.(forgeWorkspaceAddResult)
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "|"), "host system configuration") {
		t.Errorf("expected /etc dangerous warning; got %#v", res.Warnings)
	}
	if len(d.Config().Forge["alice"].Workspaces) != 1 {
		t.Errorf("dangerous path should still be persisted")
	}
}

func TestForgeWorkspaceAdd_RejectsUnknownMindform(t *testing.T) {
	d := newTestDaemon(t)
	// no SeedMindForm
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "nobody", "name": "proj-x", "host_path": dir})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrForgeNotFound {
		t.Errorf("want FORGE_NOT_FOUND; got %v", ipcErr)
	}
}

// Suppress unused-import lints if any helper isn't called in this slice
// of tests; remove once all imports are referenced.
var _ = os.MkdirAll
var _ = filepath.Join
```

**`SeedMindForm` helper.** Add to a test helper file (e.g. `internal/daemon/testing_helpers.go`, behind `//go:build test` or as plain code under `_test.go` since it's only referenced from tests). Pattern:

```go
// SeedMindForm marks name as a known mind-form for the duration of the
// test, so MindFormKnown(name) returns true without provisioning a
// real container.
func (d *Daemon) SeedMindForm(name string) {
	if d.testKnownMindForms == nil {
		d.testKnownMindForms = map[string]struct{}{}
	}
	d.testKnownMindForms[name] = struct{}{}
}
```

If `*Daemon` does not already carry a `testKnownMindForms` field, add one (zero-value-safe map; the production path leaves it nil and `MindFormKnown` falls through to the real probe). See Step 3 for the production probe.

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceAdd -v`
Expected: build fails — handler, types, helper not defined.

- [ ] **Step 3: Implement handler**

Create `internal/daemon/methods_forge_workspace.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"lucianoxu/eidopsyche/internal/config"
	"lucianoxu/eidopsyche/internal/forgectl"
	"lucianoxu/eidopsyche/internal/ipc"
)

func init() {
	register("forge.workspace.add", forgeWorkspaceAdd)
}

type forgeWorkspaceAddParams struct {
	MindForm  string `json:"mindform"`
	Name      string `json:"name"`
	HostPath  string `json:"host_path"`
	Mode      string `json:"mode,omitempty"`
	NoWarnUID bool   `json:"no_warn_uid,omitempty"`
}

type forgeWorkspaceAddResult struct {
	PendingRestart bool     `json:"pending_restart"`
	Warnings       []string `json:"warnings,omitempty"`
}

func forgeWorkspaceAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceAddParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if err := config.ValidateWorkspaceName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if err := config.ValidateWorkspaceMode(p.Mode); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if !d.MindFormKnown(p.MindForm) {
		return nil, &ipc.Error{Code: ipc.ErrForgeNotFound, Message: fmt.Sprintf("mind-form %q not found", p.MindForm)}
	}
	if err := config.ValidateWorkspaceHostPath(p.HostPath); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}

	var warnings []string
	if w := config.DangerousHostPath(p.HostPath); w != "" {
		warnings = append(warnings, w)
	}
	effMode := p.Mode
	if effMode == "" {
		effMode = "rw"
	}
	if effMode == "rw" && !p.NoWarnUID {
		if uid, err := config.HostPathOwnerUID(p.HostPath); err == nil && uid != config.MindFormContainerUID {
			warnings = append(warnings, config.UIDMismatchWarning(p.HostPath, uid))
		}
	}

	var dupErr error
	merr := d.Mutate(ctx, "forge."+p.MindForm+".workspaces", config.HostCtx,
		func(c *config.Config) error {
			if c.Forge == nil {
				c.Forge = map[string]config.ForgeMindForm{}
			}
			mf := c.Forge[p.MindForm]
			for _, w := range mf.Workspaces {
				if w.Name == p.Name {
					dupErr = fmt.Errorf("workspace %q already exists on mind-form %q", p.Name, p.MindForm)
					return dupErr
				}
			}
			mf.Workspaces = append(mf.Workspaces, config.WorkspaceMount{
				Name: p.Name, HostPath: p.HostPath, Mode: p.Mode,
			})
			c.Forge[p.MindForm] = mf
			return nil
		})
	if dupErr != nil {
		return nil, &ipc.Error{Code: ipc.ErrWorkspaceExists, Message: dupErr.Error()}
	}
	if merr != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: merr.Error()}
	}
	_ = forgectl.MountVolume // ensure import is used; remove if Go reports unused
	return forgeWorkspaceAddResult{PendingRestart: true, Warnings: warnings}, nil
}
```

(Drop the `_ = forgectl.MountVolume` line — that import isn't needed in this file; it's listed only because Tasks 7 and 10 reuse forgectl symbols. Remove it if `goimports` flags the unused import here.)

- [ ] **Step 4: Add the `MindFormKnown` probe**

In the daemon's main file (`internal/daemon/daemon.go` or wherever `*Daemon` lives), add:

```go
// MindFormKnown returns true if name has a container or a volume on
// this host. Used by forge.workspace.* handlers to reject typos
// before persisting config.
func (d *Daemon) MindFormKnown(name string) bool {
	// Test-only short-circuit so unit tests don't need a docker daemon.
	if _, ok := d.testKnownMindForms[name]; ok {
		return true
	}
	if d.forgectlClient == nil {
		return false
	}
	ctx := context.Background()
	cont := forgectl.ContainerName(name)
	if exists, _ := d.forgectlClient.ContainerExists(ctx, cont); exists {
		return true
	}
	if exists, _ := d.forgectlClient.VolumeExists(ctx, forgectl.VolumeName(name)); exists {
		return true
	}
	return false
}
```

Field: add `testKnownMindForms map[string]struct{}` to the `Daemon` struct (zero-value-safe).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceAdd -v`
Expected: all subtests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/ipc/protocol.go internal/daemon/methods_forge_workspace.go internal/daemon/methods_forge_workspace_test.go internal/daemon/daemon.go
git commit -m "feat(daemon): add forge.workspace.add IPC method

Validates name regex, mode enum, host path absolute/clean/exists/dir,
duplicate name. Emits non-blocking warnings on dangerous host paths
and uid mismatch (rw only). Returns pending_restart=true so the
operator knows to forge restart for the mount to take effect."
```

---

## Task 6: `forge.workspace.remove` IPC handler

**Files:**
- Modify: `internal/daemon/methods_forge_workspace.go`
- Modify: `internal/daemon/methods_forge_workspace_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/daemon/methods_forge_workspace_test.go`:

```go
func TestForgeWorkspaceRemove_Drops(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	rawAdd, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir})
	if _, e := forgeWorkspaceAdd(context.Background(), d, nil, rawAdd); e != nil {
		t.Fatal(e)
	}
	rawRm, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x"})
	out, ipcErr := forgeWorkspaceRemove(context.Background(), d, nil, rawRm)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	if !out.(forgeWorkspaceAddResult).PendingRestart {
		t.Errorf("expected pending_restart=true")
	}
	if len(d.Config().Forge["alice"].Workspaces) != 0 {
		t.Errorf("workspace not removed: %#v", d.Config().Forge["alice"].Workspaces)
	}
}

func TestForgeWorkspaceRemove_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "missing"})
	_, ipcErr := forgeWorkspaceRemove(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrWorkspaceNotFound {
		t.Errorf("want WORKSPACE_NOT_FOUND; got %v", ipcErr)
	}
}
```

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceRemove -v`
Expected: fails — handler not defined.

- [ ] **Step 2: Implement handler**

Append to `internal/daemon/methods_forge_workspace.go`:

```go
func init() {
	register("forge.workspace.remove", forgeWorkspaceRemove)
}

type forgeWorkspaceRemoveParams struct {
	MindForm string `json:"mindform"`
	Name     string `json:"name"`
}

func forgeWorkspaceRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceRemoveParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if err := config.ValidateWorkspaceName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if !d.MindFormKnown(p.MindForm) {
		return nil, &ipc.Error{Code: ipc.ErrForgeNotFound, Message: fmt.Sprintf("mind-form %q not found", p.MindForm)}
	}

	var notFound bool
	merr := d.Mutate(ctx, "forge."+p.MindForm+".workspaces", config.HostCtx,
		func(c *config.Config) error {
			mf := c.Forge[p.MindForm]
			filtered := mf.Workspaces[:0]
			found := false
			for _, w := range mf.Workspaces {
				if w.Name == p.Name {
					found = true
					continue
				}
				filtered = append(filtered, w)
			}
			if !found {
				notFound = true
				return fmt.Errorf("no workspace %q on mind-form %q", p.Name, p.MindForm)
			}
			mf.Workspaces = filtered
			if len(mf.Workspaces) == 0 {
				delete(c.Forge, p.MindForm)
			} else {
				c.Forge[p.MindForm] = mf
			}
			return nil
		})
	if notFound {
		return nil, &ipc.Error{Code: ipc.ErrWorkspaceNotFound, Message: fmt.Sprintf("no workspace %q on mind-form %q", p.Name, p.MindForm)}
	}
	if merr != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: merr.Error()}
	}
	return forgeWorkspaceAddResult{PendingRestart: true}, nil
}
```

The `delete(c.Forge, p.MindForm)` on empty-list is intentional: keeps the persisted TOML clean of empty `[forge.<name>]` blocks.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceRemove -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/methods_forge_workspace.go internal/daemon/methods_forge_workspace_test.go
git commit -m "feat(daemon): add forge.workspace.remove IPC method

Removes the named entry from the per-mindform list. If the list
becomes empty, the parent [forge.<name>] block is dropped so the
persisted toml stays tidy. Returns pending_restart=true."
```

---

## Task 7: `forge.workspace.list` IPC handler (desired + actual)

**Files:**
- Modify: `internal/daemon/methods_forge_workspace.go`
- Modify: `internal/daemon/methods_forge_workspace_test.go`
- Modify: `internal/forgectl/docker.go` (add `ContainerInspectMounts`)

- [ ] **Step 1: Add `ContainerInspectMounts` to forgectl**

In `internal/forgectl/docker.go`, in the `Client` interface, add:

```go
// ContainerInspectMounts returns the mount list as currently configured
// on the named container. Used by forge.workspace.list to compute the
// desired-vs-actual diff and pending_restart.
ContainerInspectMounts(ctx context.Context, name string) ([]Mount, error)
```

Add the realClient implementation alongside `ContainerInspectImage`:

```go
func (r *realClient) ContainerInspectMounts(ctx context.Context, name string) ([]Mount, error) {
	resp, err := r.c.ContainerInspect(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]Mount, 0, len(resp.HostConfig.Mounts))
	for _, m := range resp.HostConfig.Mounts {
		var t MountType
		switch m.Type {
		case "volume":
			t = MountVolume
		case "bind":
			t = MountBind
		default:
			continue
		}
		out = append(out, Mount{
			Type:     t,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return out, nil
}
```

- [ ] **Step 2: Write failing test for list**

The list handler reads `actual` from the docker client. Wire `*Daemon` to a fake `forgectl.Client` for tests. If the existing `newTestDaemon` already supports injecting a forgectl mock, use that; otherwise add a `d.SetForgectlClient(c forgectl.Client)` helper.

Append to `internal/daemon/methods_forge_workspace_test.go`:

```go
type fakeForgeClient struct {
	forgectl.Client // embed for the methods we don't override
	mounts          map[string][]forgectl.Mount
}

func (f *fakeForgeClient) ContainerInspectMounts(ctx context.Context, name string) ([]forgectl.Mount, error) {
	if m, ok := f.mounts[name]; ok {
		return m, nil
	}
	return nil, nil
}
func (f *fakeForgeClient) ContainerExists(ctx context.Context, name string) (bool, error) { return true, nil }
func (f *fakeForgeClient) VolumeExists(ctx context.Context, name string) (bool, error)    { return true, nil }

func TestForgeWorkspaceList_PendingRestart(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	d.SetForgectlClient(&fakeForgeClient{mounts: map[string][]forgectl.Mount{
		forgectl.ContainerName("alice"): {
			{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		},
	}})
	dir := t.TempDir()
	rawAdd, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir})
	if _, e := forgeWorkspaceAdd(context.Background(), d, nil, rawAdd); e != nil {
		t.Fatal(e)
	}

	rawLs, _ := json.Marshal(map[string]any{"mindform": "alice"})
	out, ipcErr := forgeWorkspaceList(context.Background(), d, nil, rawLs)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	res := out.(forgeWorkspaceListResult)
	if !res.PendingRestart {
		t.Errorf("expected pending_restart=true because actual lacks proj-x")
	}
	if len(res.Desired) != 1 || res.Desired[0].Name != "proj-x" {
		t.Errorf("desired: %#v", res.Desired)
	}
	if len(res.Actual) != 0 {
		t.Errorf("actual should have no workspace mounts; got %#v", res.Actual)
	}
}

func TestForgeWorkspaceList_InSync(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	rawAdd, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir, "mode": "rw"})
	if _, e := forgeWorkspaceAdd(context.Background(), d, nil, rawAdd); e != nil {
		t.Fatal(e)
	}
	d.SetForgectlClient(&fakeForgeClient{mounts: map[string][]forgectl.Mount{
		forgectl.ContainerName("alice"): {
			{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
			{Type: forgectl.MountBind, Source: dir, Target: "/workspace/proj-x", ReadOnly: false},
		},
	}})
	rawLs, _ := json.Marshal(map[string]any{"mindform": "alice"})
	out, ipcErr := forgeWorkspaceList(context.Background(), d, nil, rawLs)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	if out.(forgeWorkspaceListResult).PendingRestart {
		t.Errorf("expected pending_restart=false")
	}
}
```

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceList -v`
Expected: fails — handler missing.

- [ ] **Step 3: Implement handler**

Append to `internal/daemon/methods_forge_workspace.go`:

```go
import "strings" // add to import block

func init() {
	register("forge.workspace.list", forgeWorkspaceList)
}

type forgeWorkspaceListParams struct {
	MindForm string `json:"mindform"`
}

type forgeWorkspaceListEntry struct {
	Name     string `json:"name"`
	HostPath string `json:"host_path"`
	Mode     string `json:"mode"`
}

type forgeWorkspaceListResult struct {
	Desired        []forgeWorkspaceListEntry `json:"desired"`
	Actual         []forgeWorkspaceListEntry `json:"actual"`
	PendingRestart bool                      `json:"pending_restart"`
}

func forgeWorkspaceList(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if !d.MindFormKnown(p.MindForm) {
		return nil, &ipc.Error{Code: ipc.ErrForgeNotFound, Message: fmt.Sprintf("mind-form %q not found", p.MindForm)}
	}

	desired := []forgeWorkspaceListEntry{}
	for _, w := range d.Config().Forge[p.MindForm].Workspaces {
		desired = append(desired, forgeWorkspaceListEntry{
			Name: w.Name, HostPath: w.HostPath, Mode: w.EffectiveMode(),
		})
	}

	actual := []forgeWorkspaceListEntry{}
	if c := d.ForgectlClient(); c != nil {
		mounts, err := c.ContainerInspectMounts(ctx, forgectl.ContainerName(p.MindForm))
		if err == nil {
			for _, m := range mounts {
				if m.Type != forgectl.MountBind {
					continue
				}
				const prefix = "/workspace/"
				if !strings.HasPrefix(m.Target, prefix) {
					continue
				}
				name := strings.TrimPrefix(m.Target, prefix)
				if name == "" || strings.Contains(name, "/") {
					continue
				}
				mode := "rw"
				if m.ReadOnly {
					mode = "ro"
				}
				actual = append(actual, forgeWorkspaceListEntry{
					Name: name, HostPath: m.Source, Mode: mode,
				})
			}
		}
		// If err != nil (container absent before first start), actual stays
		// empty; this is a normal pre-create state.
	}

	return forgeWorkspaceListResult{
		Desired:        desired,
		Actual:         actual,
		PendingRestart: !workspaceListsEqual(desired, actual),
	}, nil
}

func workspaceListsEqual(a, b []forgeWorkspaceListEntry) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]forgeWorkspaceListEntry{}
	for _, e := range b {
		idx[e.Name] = e
	}
	for _, e := range a {
		other, ok := idx[e.Name]
		if !ok || other != e {
			return false
		}
	}
	return true
}
```

`forgectl.ContainerName(name)` is the existing helper in `internal/forgectl/names.go`. `d.ForgectlClient()` returns `*Daemon`'s injected forgectl client; if no such accessor exists, add it as a one-liner getter alongside the existing daemon fields.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestForgeWorkspaceList -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/forgectl/docker.go internal/daemon/methods_forge_workspace.go internal/daemon/methods_forge_workspace_test.go
git commit -m "feat(daemon): add forge.workspace.list IPC method with pending-restart diff

Returns desired (from config.toml) and actual (from docker inspect)
workspace lists, plus pending_restart=true when they differ.
forgectl gains ContainerInspectMounts to expose the running mount
list in the same Mount shape used by CreateOpts."
```

---

## Task 8: Container-side `state.get forge.workspaces`

**Files:**
- Create: `internal/daemon/methods_state_workspaces_container.go`
- Create: `internal/daemon/methods_state_workspaces_container_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/daemon/methods_state_workspaces_container_test.go`:

```go
package daemon

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseWorkspacesFromMounts feeds canned /proc/self/mounts content
// and confirms the expected (name, mode) list comes out.
func TestParseWorkspacesFromMounts(t *testing.T) {
	mounts := strings.Join([]string{
		// Typical container mount lines:
		"overlay / overlay rw,relatime,lowerdir=...,upperdir=... 0 0",
		"/dev/sda1 /eidos ext4 rw,relatime 0 0",
		"/dev/sda1 /workspace/proj-x ext4 rw,relatime 0 0",
		"/dev/sda1 /workspace/photos ext4 ro,relatime 0 0",
		"proc /proc proc rw,nosuid,nodev,noexec 0 0",
		"/dev/sda1 /workspace/nested/dir ext4 rw,relatime 0 0",
		"",
	}, "\n")

	got := parseWorkspacesFromMounts([]byte(mounts))
	want := []workspaceContainerEntry{
		{Name: "proj-x", Mode: "rw"},
		{Name: "photos", Mode: "ro"},
		// "nested/dir" is filtered: only direct /workspace/<name>/ entries count.
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorkspacesFromMounts mismatch\nwant %#v\n got %#v", want, got)
	}
}
```

Run: `go test ./internal/daemon/ -run TestParseWorkspacesFromMounts -v`
Expected: fails — symbol missing.

- [ ] **Step 2: Implement**

Create `internal/daemon/methods_state_workspaces_container.go`:

```go
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
)

// workspaceContainerEntry is the shape returned to mind-forms by the
// in-container state.get forge.workspaces handler. No host_path field
// — that would leak host filesystem layout into the container, which
// is meaningless to the mind-form anyway.
type workspaceContainerEntry struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// stateGetWorkspacesContainer runs on container-side daemons only. It
// reads /proc/self/mounts, filters to direct /workspace/<name>
// entries, and reports (name, ro|rw) per entry. Host-side daemons
// register a different handler for the same state path (or simply
// don't expose it) — see Task 5/7 note about HostCtx-only behaviour.
func stateGetWorkspacesContainer(ctx context.Context, d *Daemon, raw json.RawMessage) (json.RawMessage, error) {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	return json.Marshal(parseWorkspacesFromMounts(data))
}

// parseWorkspacesFromMounts is the pure helper, split for testability.
func parseWorkspacesFromMounts(data []byte) []workspaceContainerEntry {
	out := []workspaceContainerEntry{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		// /proc/mounts format: <src> <mountpoint> <fstype> <opts> 0 0
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mp, opts := fields[1], fields[3]
		const prefix = "/workspace/"
		if !strings.HasPrefix(mp, prefix) {
			continue
		}
		name := strings.TrimPrefix(mp, prefix)
		if name == "" || strings.Contains(name, "/") {
			continue // exclude /workspace/ root or /workspace/nested/dir
		}
		mode := "rw"
		for _, o := range strings.Split(opts, ",") {
			if o == "ro" {
				mode = "ro"
				break
			}
		}
		out = append(out, workspaceContainerEntry{Name: name, Mode: mode})
	}
	return out
}
```

- [ ] **Step 3: Wire into the state.get dispatch**

The container daemon's `state.get` already has a per-path dispatcher (see `internal/daemon/methods_state.go`). Add a case so `state.get forge.workspaces` (on container-context daemons only) routes to `stateGetWorkspacesContainer`. The host-side daemon's `state.get forge.workspaces` is a deliberate **no-op / not-found** (see spec §9 — host-side state.get isn't expected to need this path; if anything calls it on the host, return an empty list).

Identify the dispatch table:

Run: `grep -n "state.get" internal/daemon/methods_state.go`

Wire the new handler at the appropriate switch / table entry, gated on `d.Context() == config.ContainerCtx`. If the existing pattern uses per-path registration, register `forge.workspaces` only when initializing as a container daemon.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestParseWorkspacesFromMounts -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/methods_state_workspaces_container.go internal/daemon/methods_state_workspaces_container_test.go internal/daemon/methods_state.go
git commit -m "feat(daemon): add container-side state.get forge.workspaces

Parses /proc/self/mounts and returns {name, mode} for every direct
/workspace/<name> mount. Mind-forms call this to learn what mode
their workspaces are in without trying writes."
```

---

## Task 9: `orchestrate.go` — assemble mounts from config

**Files:**
- Modify: `cmd/eidos/forge/orchestrate.go` (or `internal/forge/create.go` if upgrade plan merged first)

- [ ] **Step 1: Thread `*config.Config` through `Orchestrate`**

Current signature (`cmd/eidos/forge/orchestrate.go:92`):

```go
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error
```

Change to:

```go
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts, cfg *config.Config) error
```

Update both call sites:
- `cmd/eidos/forge/orchestrate.go:236` — `runCreate2` already loads `o`; load the config there too:
  ```go
  cfg, err := config.LoadDefault()   // or the existing host-gate config loader
  if err != nil {
      return err
  }
  if err := Orchestrate(ctx, c, name, o, cfg); err != nil { ... }
  ```
- `internal/firstcontact/phase4_seal.go:169` — change to:
  ```go
  if err := forge.Orchestrate(ctx, d.DockerClient, s.Slug, createOpts, d.Config); err != nil { ... }
  ```
  Pass whatever `*config.Config` the firstcontact session already holds. If the session does not carry one, load via `config.LoadDefault()` immediately before the call (it's the same data the host gate uses).

Match the existing config-load helper's actual name (e.g. `config.Load`, `config.LoadDefault`, `config.LoadHost`):

Run: `grep -rn "func Load\b\|func LoadDefault" internal/config/`

- [ ] **Step 2: Build the mount slice**

Replace the `create-container` step body (currently just builds the `/eidos` volume mount):

```go
do: func(ctx context.Context) error {
	mounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
	}
	for _, w := range cfg.Forge[name].Workspaces {
		mounts = append(mounts, forgectl.Mount{
			Type:     forgectl.MountBind,
			Source:   w.HostPath,
			Target:   "/workspace/" + w.Name,
			ReadOnly: w.EffectiveMode() == "ro",
		})
	}
	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:   cont,
		Image:  image,
		Mounts: mounts,
	}); err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	return nil
},
```

- [ ] **Step 3: Write a test**

Add to `cmd/eidos/forge/orchestrate_image_test.go` (existing file) or create `cmd/eidos/forge/orchestrate_workspaces_test.go`:

```go
package forge

import (
	"context"
	"testing"

	"lucianoxu/eidopsyche/internal/config"
	"lucianoxu/eidopsyche/internal/forgectl"
)

func TestOrchestrate_AssemblesWorkspaceMounts(t *testing.T) {
	cfg := &config.Config{
		Forge: map[string]config.ForgeMindForm{
			"alice": {Workspaces: []config.WorkspaceMount{
				{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
				{Name: "photos", HostPath: "/home/op/Pictures", Mode: "ro"},
			}},
		},
	}
	c := &fakeForgectlClient{}
	if err := Orchestrate(context.Background(), c, "alice", CreateOpts{Label: "alice"}, cfg); err != nil {
		t.Fatal(err)
	}
	want := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		{Type: forgectl.MountBind, Source: "/home/op/code/project-x", Target: "/workspace/proj-x", ReadOnly: false},
		{Type: forgectl.MountBind, Source: "/home/op/Pictures", Target: "/workspace/photos", ReadOnly: true},
	}
	if !reflect.DeepEqual(c.createMounts, want) {
		t.Fatalf("mount list mismatch\nwant %#v\n got %#v", want, c.createMounts)
	}
}

// fakeForgectlClient records ContainerCreate's Mounts param. Match
// the existing forgectl.Client interface — fill in no-op stubs for
// the methods Orchestrate happens to call.
type fakeForgectlClient struct {
	createMounts []forgectl.Mount
	// ... other recorded fields per Orchestrate's call graph
}

func (f *fakeForgectlClient) ContainerCreate(ctx context.Context, opts forgectl.CreateOpts) error {
	f.createMounts = opts.Mounts
	return nil
}
// stub all other Client methods to satisfy the interface
```

Run: `grep -n "type Client interface" internal/forgectl/docker.go`
Use the listed methods to fill in stubs for `fakeForgectlClient`.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/eidos/forge/ -run TestOrchestrate -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/orchestrate.go cmd/eidos/forge/orchestrate_workspaces_test.go
git commit -m "feat(forge): include configured workspaces in create-container mount list

Orchestrate reads cfg.Forge[name].Workspaces and appends a Mount for
each entry as MountBind at /workspace/<name>, honouring the ro/rw
mode. The /eidos volume mount stays first."
```

---

## Task 10: `eidos forge restart` — recreate flow

**Files:**
- Create: `cmd/eidos/forge/restart.go`
- Create: `cmd/eidos/forge/restart_test.go`
- Modify: `cmd/eidos/forge/cmd.go` (register the command)

- [ ] **Step 1: Write failing test**

Create `cmd/eidos/forge/restart_test.go`:

```go
package forge

import (
	"context"
	"reflect"
	"testing"

	"lucianoxu/eidopsyche/internal/config"
	"lucianoxu/eidopsyche/internal/forgectl"
)

// TestRestart_PreservesImage_AppliesNewMounts confirms the new
// forge restart flow stops, reads the current image, removes, creates
// with the same image plus the latest config mount list, then starts.
func TestRestart_PreservesImage_AppliesNewMounts(t *testing.T) {
	c := &fakeForgectlClient{
		inspectImageReturn: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3", // a previously-upgraded image
	}
	cfg := &config.Config{
		Forge: map[string]config.ForgeMindForm{
			"alice": {Workspaces: []config.WorkspaceMount{
				{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
			}},
		},
	}
	if err := Restart(context.Background(), c, "alice", cfg, 10); err != nil {
		t.Fatal(err)
	}
	wantOps := []string{"stop:alice", "inspect-image:alice", "remove:alice", "create:alice", "start:alice"}
	if !reflect.DeepEqual(c.ops, wantOps) {
		t.Fatalf("ops sequence\nwant %v\n got %v", wantOps, c.ops)
	}
	if c.createImage != "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3" {
		t.Errorf("create used image %q, want preserved v0.11.3", c.createImage)
	}
	wantMounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		{Type: forgectl.MountBind, Source: "/home/op/code/project-x", Target: "/workspace/proj-x", ReadOnly: false},
	}
	if !reflect.DeepEqual(c.createMounts, wantMounts) {
		t.Errorf("create mounts\nwant %#v\n got %#v", wantMounts, c.createMounts)
	}
}

// TestRestart_ImageInspectError_AbortsCleanly: if reading the current
// image fails, we abort before doing rm — container still exists,
// safe to retry.
func TestRestart_ImageInspectError_AbortsCleanly(t *testing.T) {
	c := &fakeForgectlClient{inspectImageErr: errors.New("docker oops")}
	cfg := &config.Config{}
	err := Restart(context.Background(), c, "alice", cfg, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, op := range c.ops {
		if op == "remove:alice" {
			t.Errorf("must not remove after inspect-image failure: ops=%v", c.ops)
		}
	}
}
```

`fakeForgectlClient` is the one from Task 9; extend with `inspectImageReturn`, `inspectImageErr`, `createImage`, `ops` fields.

Run: `go test ./cmd/eidos/forge/ -run TestRestart -v`
Expected: fails — `Restart` undefined.

- [ ] **Step 2: Implement `Restart`**

Create `cmd/eidos/forge/restart.go`:

```go
package forge

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"lucianoxu/eidopsyche/internal/config"
	"lucianoxu/eidopsyche/internal/forgectl"
)

// Restart performs the recreate cycle: stop → inspect-image → rm →
// create (with preserved image + latest config mounts) → start. The
// /eidos named volume is unaffected.
//
// Image preservation reads from the existing container's
// Config.Image rather than re-resolving DefaultImage(); this ensures
// a previous forge upgrade is not silently reverted.
func Restart(ctx context.Context, c forgectl.Client, name string, cfg *config.Config, graceSeconds int) error {
	cont := forgectl.ContainerName(name)
	vol := forgectl.VolumeName(name)

	if err := c.ContainerStop(ctx, cont, graceSeconds); err != nil {
		return fmt.Errorf("stop %s: %w", cont, err)
	}

	image, err := c.ContainerInspectImage(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", cont, err)
	}

	if err := c.ContainerRemove(ctx, cont); err != nil {
		return fmt.Errorf("remove %s: %w", cont, err)
	}

	mounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
	}
	for _, w := range cfg.Forge[name].Workspaces {
		mounts = append(mounts, forgectl.Mount{
			Type:     forgectl.MountBind,
			Source:   w.HostPath,
			Target:   "/workspace/" + w.Name,
			ReadOnly: w.EffectiveMode() == "ro",
		})
	}

	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:   cont,
		Image:  image,
		Mounts: mounts,
	}); err != nil {
		return fmt.Errorf("create %s: %w", cont, err)
	}

	if err := c.ContainerStart(ctx, cont); err != nil {
		return fmt.Errorf("start %s: %w", cont, err)
	}
	return nil
}

func RestartCmd() *cobra.Command {
	var grace int
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Recreate the mind-form container, picking up any workspace config changes",
		Long: `Recreate the mind-form's container with the latest configured mounts.
The /eidos volume (ontology, identity, transcripts, claudeauth) is
preserved; the current container's image is preserved across the
recreate cycle. To change the image, use 'eidos forge upgrade'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}
			if err := Restart(cmd.Context(), c, args[0], cfg, grace); err != nil {
				return err
			}
			cmd.Printf("restarted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().IntVar(&grace, "grace", 10, "seconds before SIGKILL during stop")
	return cmd
}
```

`config.LoadDefault()` — match the existing config-load helper name in the codebase. If the only helper is `config.Load(path)`, replace with whatever the existing `eidos forge stop` / `eidos forge start` commands use.

- [ ] **Step 3: Register the command**

In `cmd/eidos/forge/cmd.go`, find the parent command builder and add:

```go
forgeCmd.AddCommand(RestartCmd())
```

- [ ] **Step 4: Extend `fakeForgectlClient`**

In `cmd/eidos/forge/restart_test.go` (or wherever `fakeForgectlClient` lives), add fields:

```go
type fakeForgectlClient struct {
	ops          []string
	createImage  string
	createMounts []forgectl.Mount

	inspectImageReturn string
	inspectImageErr    error
}

func (f *fakeForgectlClient) ContainerStop(ctx context.Context, name string, _ int) error {
	f.ops = append(f.ops, "stop:"+name)
	return nil
}
func (f *fakeForgectlClient) ContainerInspectImage(ctx context.Context, name string) (string, error) {
	f.ops = append(f.ops, "inspect-image:"+name)
	return f.inspectImageReturn, f.inspectImageErr
}
func (f *fakeForgectlClient) ContainerRemove(ctx context.Context, name string) error {
	f.ops = append(f.ops, "remove:"+name)
	return nil
}
func (f *fakeForgectlClient) ContainerCreate(ctx context.Context, opts forgectl.CreateOpts) error {
	f.ops = append(f.ops, "create:"+opts.Name)
	f.createImage = opts.Image
	f.createMounts = opts.Mounts
	return nil
}
func (f *fakeForgectlClient) ContainerStart(ctx context.Context, name string) error {
	f.ops = append(f.ops, "start:"+name)
	return nil
}
// stub remaining Client methods
```

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/eidos/forge/ -run TestRestart -v`
Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/eidos/forge/restart.go cmd/eidos/forge/restart_test.go cmd/eidos/forge/cmd.go
git commit -m "feat(forge): add forge restart command with recreate semantics

stop → inspect current image → rm → create (preserved image, latest
config mounts) → start. The /eidos volume survives unchanged; the
previous container's image is preserved so a prior forge upgrade is
not silently reverted."
```

---

## Task 11: `forge status` — `pending_restart` line

**Files:**
- Modify: `cmd/eidos/forge/status.go`
- Modify: `cmd/eidos/forge/status_test.go`

- [ ] **Step 1: Read current status renderer**

Run: `grep -n "func " cmd/eidos/forge/status.go | head -20`

Locate the function that assembles the status output (likely `runStatus` or `renderStatus`). Find where it queries the daemon and where it writes lines.

- [ ] **Step 2: Add a failing test**

Append to `cmd/eidos/forge/status_test.go`:

```go
func TestStatus_RendersPendingRestart(t *testing.T) {
	// Build a fake daemon response: desired has proj-x, actual doesn't.
	resp := forgeStatusResp{
		Name:  "alice",
		State: "running",
		WorkspacesDesired: []forgeWorkspaceListEntry{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
		},
		WorkspacesActual: []forgeWorkspaceListEntry{},
		PendingRestart:   true,
	}
	var buf bytes.Buffer
	renderStatus(&buf, resp)
	out := buf.String()
	if !strings.Contains(out, "pending restart") {
		t.Errorf("expected pending-restart line; got:\n%s", out)
	}
	if !strings.Contains(out, "proj-x") {
		t.Errorf("expected proj-x in desired; got:\n%s", out)
	}
}

func TestStatus_HidesPendingRestartWhenSynced(t *testing.T) {
	resp := forgeStatusResp{
		Name:           "alice",
		State:          "running",
		PendingRestart: false,
	}
	var buf bytes.Buffer
	renderStatus(&buf, resp)
	if strings.Contains(buf.String(), "pending restart") {
		t.Errorf("did not expect pending-restart line when synced")
	}
}
```

`forgeStatusResp` is the existing status response type — match its actual name in `status.go`.

Run: `go test ./cmd/eidos/forge/ -run TestStatus -v`
Expected: fails — fields not on the struct.

- [ ] **Step 3: Add fields to the status response and render**

In `status.go`, add to the status response type:

```go
WorkspacesDesired []forgeWorkspaceListEntry `json:"workspaces_desired,omitempty"`
WorkspacesActual  []forgeWorkspaceListEntry `json:"workspaces_actual,omitempty"`
PendingRestart    bool                      `json:"pending_restart,omitempty"`
```

(If the type is in `internal/daemon/methods_forge_status.go`, edit there instead.)

In the daemon's status handler, populate these fields by calling `forgeWorkspaceList` internally (or by inlining the same desired/actual computation from Task 7) and storing the result.

In `cmd/eidos/forge/status.go`'s renderer, after the existing lines, add:

```go
if len(resp.WorkspacesDesired) > 0 || len(resp.WorkspacesActual) > 0 {
	fmt.Fprintf(w, "workspaces (desired): %s\n", formatWorkspaces(resp.WorkspacesDesired))
	fmt.Fprintf(w, "workspaces (actual):  %s\n", formatWorkspaces(resp.WorkspacesActual))
}
if resp.PendingRestart {
	fmt.Fprintf(w, "⚠ pending restart: workspaces changed — run 'eidos forge restart %s' to apply\n", resp.Name)
}
```

Add helper:

```go
func formatWorkspaces(entries []forgeWorkspaceListEntry) string {
	if len(entries) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s (%s)", e.Name, e.Mode))
	}
	return strings.Join(parts, ", ")
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/eidos/forge/ -run TestStatus -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/status.go cmd/eidos/forge/status_test.go internal/daemon/methods_forge_status.go
git commit -m "feat(forge): show workspaces and pending-restart in status output

Status now lists desired and actual workspaces (name + mode) and
prints a pending-restart hint when they differ, guiding the operator
to 'eidos forge restart' to apply config changes."
```

---

## Task 12: CLI — `eidos forge workspace add/remove/list`

**Files:**
- Create: `cmd/eidos/forge/workspace.go`
- Create: `cmd/eidos/forge/workspace_test.go`
- Modify: `cmd/eidos/forge/cmd.go`

- [ ] **Step 1: Write the cobra command tree**

Create `cmd/eidos/forge/workspace.go`:

```go
package forge

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"lucianoxu/eidopsyche/internal/ipc"
)

func WorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage a mind-form's shared workspace mounts",
		Long: `Workspaces are host directories bind-mounted into a
mind-form's container at /workspace/<name>/. Changes take effect on
the next 'eidos forge restart <mindform>'.`,
	}
	cmd.AddCommand(workspaceAddCmd(), workspaceRemoveCmd(), workspaceListCmd())
	return cmd
}

func workspaceAddCmd() *cobra.Command {
	var mode string
	var noWarnUID bool
	cmd := &cobra.Command{
		Use:   "add <mindform> <name> <host-path>",
		Short: "Bind a host directory into a mind-form's container",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ipc.DialDefault()
			if err != nil {
				return err
			}
			defer client.Close()
			params := map[string]any{
				"mindform":     args[0],
				"name":         args[1],
				"host_path":    args[2],
				"mode":         mode,
				"no_warn_uid":  noWarnUID,
			}
			resp, err := client.Call(cmd.Context(), "forge.workspace.add", params)
			if err != nil {
				return err
			}
			var r struct {
				PendingRestart bool     `json:"pending_restart"`
				Warnings       []string `json:"warnings"`
			}
			_ = json.Unmarshal(resp, &r)
			for _, w := range r.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			if r.PendingRestart {
				cmd.Printf("added %s to %s (pending restart — run 'eidos forge restart %s' to apply)\n", args[1], args[0], args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "mount mode: ro or rw (default rw)")
	cmd.Flags().BoolVar(&noWarnUID, "no-warn-uid", false, "suppress the uid-mismatch warning")
	return cmd
}

func workspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <mindform> <name>",
		Short: "Remove a workspace binding from a mind-form's config",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ipc.DialDefault()
			if err != nil {
				return err
			}
			defer client.Close()
			_, err = client.Call(cmd.Context(), "forge.workspace.remove",
				map[string]any{"mindform": args[0], "name": args[1]})
			if err != nil {
				return err
			}
			cmd.Printf("removed %s from %s (pending restart — run 'eidos forge restart %s' to apply)\n", args[1], args[0], args[0])
			return nil
		},
	}
}

func workspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <mindform>",
		Short: "List a mind-form's configured workspaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ipc.DialDefault()
			if err != nil {
				return err
			}
			defer client.Close()
			resp, err := client.Call(cmd.Context(), "forge.workspace.list",
				map[string]any{"mindform": args[0]})
			if err != nil {
				return err
			}
			var r struct {
				Desired []struct {
					Name     string `json:"name"`
					HostPath string `json:"host_path"`
					Mode     string `json:"mode"`
				} `json:"desired"`
				Actual []struct {
					Name     string `json:"name"`
					HostPath string `json:"host_path"`
					Mode     string `json:"mode"`
				} `json:"actual"`
				PendingRestart bool `json:"pending_restart"`
			}
			_ = json.Unmarshal(resp, &r)
			cmd.Printf("workspaces for %s:\n", args[0])
			if len(r.Desired) == 0 {
				cmd.Println("  (none)")
				return nil
			}
			for _, w := range r.Desired {
				cmd.Printf("  %s\t%s\t%s\n", w.Name, w.Mode, w.HostPath)
			}
			if r.PendingRestart {
				cmd.Printf("\n⚠ pending restart — run 'eidos forge restart %s' to apply\n", args[0])
			}
			return nil
		},
	}
}
```

`ipc.DialDefault()` / `client.Call` — match the actual IPC client helpers used by the existing `send`, `config-set`, `forge-list` commands. Update names accordingly.

- [ ] **Step 2: Register `WorkspaceCmd`**

In `cmd/eidos/forge/cmd.go`, find where the forge subcommands are added (likely a single `forgeCmd.AddCommand(...)` block) and append:

```go
forgeCmd.AddCommand(WorkspaceCmd())
```

- [ ] **Step 3: Write a smoke test**

Create `cmd/eidos/forge/workspace_test.go`:

```go
package forge

import "testing"

func TestWorkspaceCmd_SubcommandsRegistered(t *testing.T) {
	cmd := WorkspaceCmd()
	wants := []string{"add", "remove", "list"}
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Name()] = true
	}
	for _, w := range wants {
		if !have[w] {
			t.Errorf("missing subcommand: %s", w)
		}
	}
}
```

Run: `go test ./cmd/eidos/forge/ -run TestWorkspaceCmd -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/eidos/forge/workspace.go cmd/eidos/forge/workspace_test.go cmd/eidos/forge/cmd.go
git commit -m "feat(forge): add 'eidos forge workspace add/remove/list' CLI

Wraps the forge.workspace.* IPC methods. List renders the desired
list plus a pending-restart hint when the actual diverges. Add
streams warnings (dangerous path, uid mismatch) to stderr."
```

---

## Task 13: System prompt addendum

**Files:**
- Modify: `internal/prompts/assets/system1-instructions.txt`
- Modify: `internal/prompts/system_test.go`

- [ ] **Step 1: Find the insertion point**

Run: `grep -n "ontology\|/eidos" internal/prompts/assets/system1-instructions.txt`

Identify the line just after the ontology-introduction paragraph. The workspaces section goes there.

- [ ] **Step 2: Insert the section**

Add (after the ontology paragraph and before the tool catalogue):

```
### Shared workspaces

Beyond your ontology at /eidos/, you may have shared workspaces mounted under /workspace/<name>/. These are host directories bound into your container, intended for collaboration (e.g. a code repository you and the operator both edit, or a reference directory shared with another mind-form).

- A workspace may be read-only — if a write fails with EROFS / "Read-only file system", treat it as ro and do not retry.
- Files in /workspace/<name>/ may change concurrently (operator edits, other mind-forms write). Re-read before relying on their contents.
- You cannot add or remove workspaces yourself.

```

- [ ] **Step 3: Update the system-prompt test if it asserts on content**

Run: `grep -n "Shared workspaces\|workspace" internal/prompts/system_test.go`

If the test asserts that the rendered prompt contains specific substrings (typical for snapshot-style tests), add an assertion that the new section is present:

```go
if !strings.Contains(out, "/workspace/<name>/") {
	t.Errorf("system prompt missing workspaces section")
}
if !strings.Contains(out, "EROFS") {
	t.Errorf("system prompt missing ro EROFS hint")
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/prompts/ -v`
Expected: pass. If a snapshot test fails because the golden file is stale, regenerate it per the package's existing convention (probably `go test -run TestSystemPrompt -update` or a Makefile target — check existing patterns).

- [ ] **Step 5: Commit**

```bash
git add internal/prompts/assets/system1-instructions.txt internal/prompts/system_test.go
git commit -m "feat(prompts): describe shared workspaces in the system prompt

Mind-forms now learn at wake time that /workspace/<name>/ exists, may
be read-only, may change between wakes, and cannot be self-mounted."
```

---

## Task 14: Dashboard adapter wiring

**Files:**
- Modify: `internal/daemon/dashboard_adapter.go`

- [ ] **Step 1: Read the adapter pattern**

Run: `grep -n "func (a dashboardAdapter)" internal/daemon/dashboard_adapter.go | head -10`

Locate the adapter-method style. Each method packs its params into JSON and calls `a.d.Call` (the in-process dispatcher), per the lint test in `dashboard_adapter_lint_test.go`.

- [ ] **Step 2: Add three adapter methods**

Append to `internal/daemon/dashboard_adapter.go`:

```go
// WorkspaceAdd packs params and dispatches forge.workspace.add through
// the same method table the CLI uses. The dashboard handler renders
// the warnings and pending-restart flag from the typed result.
func (a dashboardAdapter) WorkspaceAdd(ctx context.Context, opts dashboard.WorkspaceAddOpts) (dashboard.WorkspaceAddResult, error) {
	raw, _ := json.Marshal(map[string]any{
		"mindform":    opts.MindForm,
		"name":        opts.Name,
		"host_path":   opts.HostPath,
		"mode":        opts.Mode,
		"no_warn_uid": opts.NoWarnUID,
	})
	resp, err := a.d.Call(ctx, "forge.workspace.add", raw)
	if err != nil {
		return dashboard.WorkspaceAddResult{}, err
	}
	var r dashboard.WorkspaceAddResult
	_ = json.Unmarshal(resp, &r)
	return r, nil
}

func (a dashboardAdapter) WorkspaceRemove(ctx context.Context, mindForm, name string) error {
	raw, _ := json.Marshal(map[string]any{"mindform": mindForm, "name": name})
	_, err := a.d.Call(ctx, "forge.workspace.remove", raw)
	return err
}

func (a dashboardAdapter) WorkspaceList(ctx context.Context, mindForm string) (dashboard.WorkspaceListResult, error) {
	raw, _ := json.Marshal(map[string]any{"mindform": mindForm})
	resp, err := a.d.Call(ctx, "forge.workspace.list", raw)
	if err != nil {
		return dashboard.WorkspaceListResult{}, err
	}
	var r dashboard.WorkspaceListResult
	_ = json.Unmarshal(resp, &r)
	return r, nil
}
```

`dashboard.WorkspaceAddOpts`, `dashboard.WorkspaceAddResult`, `dashboard.WorkspaceListResult` are new types — add them to the dashboard's public deps interface (mirror the existing `InviteCreateOpts` pattern referenced at `dashboard_adapter.go:297` per the spec).

- [ ] **Step 3: Run the dashboard-adapter lint test**

Run: `go test ./internal/daemon/ -run TestDashboardAdapter -v`
Expected: pass. The lint test asserts every adapter method routes through `a.d.Call`; the three new methods conform.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/dashboard_adapter.go internal/dashboard/types.go
git commit -m "feat(daemon): expose forge.workspace.* via dashboard adapter

Three new adapter methods (Add, Remove, List) pack params and call
through the daemon's in-process dispatcher, matching the single-call-
path policy. New types in internal/dashboard for the deps interface."
```

---

## Task 15: Integration tests

**Files:**
- Create: `test/integration/forge_workspaces_test.go` (or `cmd/eidos/forge/workspace_integration_test.go`, matching the repo's integration-test placement convention — check via `grep -rln "//go:build integration" .`)

- [ ] **Step 1: Write integration tests**

Create the file with `//go:build integration` tag at the top.

```go
//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const integrationImage = "ghcr.io/lucianoxu/eidopsyche-mindform:dev"

// TestWorkspaceLifecycle: create a mindform, add an rw workspace,
// pending_restart=true, actual unchanged. Restart. Verify the file
// written inside the container appears on the host.
func TestWorkspaceLifecycle(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	hostDir := t.TempDir()
	mfName := "wstest-" + randSuffix(t)
	t.Cleanup(func() { eidos(t, "forge", "purge", mfName, "--force") })

	eidos(t, "forge", "create", mfName, "--label", mfName)
	eidos(t, "forge", "start", mfName)

	out := eidos(t, "forge", "workspace", "add", mfName, "proj", hostDir)
	if !strings.Contains(out, "pending restart") {
		t.Fatalf("expected pending-restart hint; got %q", out)
	}
	listOut := eidos(t, "forge", "workspace", "list", mfName)
	if !strings.Contains(listOut, "proj") {
		t.Fatalf("workspace not listed: %q", listOut)
	}

	eidos(t, "forge", "restart", mfName)

	// Wait briefly for the in-container daemon to come up.
	time.Sleep(3 * time.Second)

	// Container writes a file.
	dockerExec(t, mfName, "sh", "-c", "echo hello > /workspace/proj/from-mindform.txt")
	got, err := os.ReadFile(filepath.Join(hostDir, "from-mindform.txt"))
	if err != nil || strings.TrimSpace(string(got)) != "hello" {
		t.Fatalf("host did not see container write: err=%v got=%q", err, got)
	}

	// Host writes a file, container reads.
	if err := os.WriteFile(filepath.Join(hostDir, "from-host.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}
	got2 := dockerExec(t, mfName, "cat", "/workspace/proj/from-host.txt")
	if strings.TrimSpace(got2) != "world" {
		t.Fatalf("container did not see host write: got %q", got2)
	}
}

// TestWorkspaceReadOnlyEnforced: ro mount produces EROFS on container
// write attempts.
func TestWorkspaceReadOnlyEnforced(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip()
	}
	hostDir := t.TempDir()
	mfName := "wsrotest-" + randSuffix(t)
	t.Cleanup(func() { eidos(t, "forge", "purge", mfName, "--force") })

	eidos(t, "forge", "create", mfName, "--label", mfName)
	eidos(t, "forge", "start", mfName)
	eidos(t, "forge", "workspace", "add", mfName, "ro", hostDir, "--mode", "ro")
	eidos(t, "forge", "restart", mfName)
	time.Sleep(3 * time.Second)

	cmd := exec.Command("docker", "exec", forgectlContainerName(mfName), "sh", "-c", "touch /workspace/ro/x 2>&1; echo done")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Read-only") {
		t.Fatalf("expected EROFS; got %q", out)
	}
}

// TestWorkspaceSharedBetweenMindforms: alice and bob both bind the
// same host_path. alice writes, bob reads.
func TestWorkspaceSharedBetweenMindforms(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip()
	}
	hostDir := t.TempDir()
	a := "wsalice-" + randSuffix(t)
	b := "wsbob-" + randSuffix(t)
	t.Cleanup(func() {
		eidos(t, "forge", "purge", a, "--force")
		eidos(t, "forge", "purge", b, "--force")
	})

	for _, name := range []string{a, b} {
		eidos(t, "forge", "create", name, "--label", name)
		eidos(t, "forge", "start", name)
		eidos(t, "forge", "workspace", "add", name, "share", hostDir)
		eidos(t, "forge", "restart", name)
	}
	time.Sleep(3 * time.Second)

	dockerExec(t, a, "sh", "-c", "echo from-alice > /workspace/share/note.txt")
	got := dockerExec(t, b, "cat", "/workspace/share/note.txt")
	if strings.TrimSpace(got) != "from-alice" {
		t.Fatalf("bob did not see alice's write: %q", got)
	}
}

// eidos runs the host binary and returns combined stdout/stderr.
func eidos(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("eidos", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("eidos %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func dockerExec(t *testing.T, mf string, args ...string) string {
	t.Helper()
	full := append([]string{"exec", forgectlContainerName(mf)}, args...)
	cmd := exec.Command("docker", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %v: %v\n%s", full, err, out)
	}
	return string(out)
}

func forgectlContainerName(mf string) string {
	return "eidos-mindform-" + mf
}

func randSuffix(t *testing.T) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil { t.Fatal(err) }
	return fmt.Sprintf("%x", b)
}
```

Add the `rand`, `fmt` imports.

- [ ] **Step 2: Run integration tests**

Run: `go test -tags=integration ./test/integration/ -run TestWorkspace -v -timeout 5m`
Expected: all three tests pass. If docker is unavailable they skip cleanly.

- [ ] **Step 3: Commit**

```bash
git add test/integration/forge_workspaces_test.go
git commit -m "test(forge): integration tests for workspace lifecycle, ro, sharing

Three integration tests behind -tags=integration: end-to-end add →
restart → host/container file visibility, ro mode EROFS enforcement,
and two-mindform shared-substrate pattern."
```

---

## Task 16: Documentation

**Files:**
- Modify: `docs/USAGE.md`
- Modify: `docs/specs/SPEC.md`
- Modify: `docker/mindform/AGENTS.md`

- [ ] **Step 1: Add USAGE.md section**

Append to `docs/USAGE.md` under an existing top-level heading or as a new section after the `forge` family:

```markdown
### Shared workspaces

Mount a host directory into a mind-form's container so the mind-form
can `Read` / `Write` / `Edit` files that you also see natively.

```
# Bind ~/code/project-x into alice as /workspace/proj-x (rw default)
eidos forge workspace add alice proj-x ~/code/project-x

# Read-only mount, e.g. for a shared reference dataset
eidos forge workspace add alice photos ~/Pictures --mode ro

# Apply the change (recreates the container; /eidos is preserved)
eidos forge restart alice

# Inspect current state
eidos forge workspace list alice
eidos forge status alice            # shows pending-restart if config diverges from running container

# Remove a workspace
eidos forge workspace remove alice proj-x
eidos forge forge restart alice
```

To share a directory between two mind-forms, bind the same host path
to both:

```
eidos forge workspace add alice proj-x ~/code/project-x
eidos forge workspace add bob   proj-x ~/code/project-x
eidos forge restart alice && eidos forge restart bob
```

Workspaces appear inside the container at `/workspace/<name>/`. They
are **not** part of the mind-form's ontology (`/eidos/...`). The
mind-form can list them with `ls /workspace/` and learns about them
through its system prompt at every wake.

**UID note.** The mind-form runs as uid 1000 inside the container. If
the host directory is owned by a different uid (typical on macOS or
non-default Linux setups), `rw` writes from the mind-form may fail
with `EACCES`. `forge workspace add` warns when this would apply.
Fix with `chown -R 1000 <path>` or pass `--no-warn-uid` to silence.
```

- [ ] **Step 2: Annotate `docs/specs/SPEC.md`**

Around line 137 (the paragraph about `/eidos` not being bind-mounted), add a follow-up paragraph:

```markdown
> **关于 `/workspace/`** —— 心智体的本体边界在容器和 `/eidos/` 卷上,
> 这一点不变。容器之内可以挂载**操作者明确授权的**宿主目录到
> `/workspace/<name>/`,作为本体之外的共享工作区(代码仓库、共享数据
> 集、跨心智体的协作目录)。这些挂载与本体正交:它们不在 `/eidos/`
> 之内,不被视为心智体生命的一部分,且只能由操作者(而非心智体自己)
> 增减。详见 `docs/superpowers/specs/2026-05-14-mindform-workspaces-design.md`。
```

- [ ] **Step 3: Add the one-line note in `docker/mindform/AGENTS.md`**

Find the section that describes the container's filesystem layout (probably near top, mentioning `/eidos`). Append a sentence:

```markdown
Operators may additionally bind host directories under `/workspace/<name>/`
via `eidos forge workspace add` (see `docs/USAGE.md`); these are orthogonal
to the ontology and survive across `eidos forge restart`.
```

- [ ] **Step 4: Commit**

```bash
git add docs/USAGE.md docs/specs/SPEC.md docker/mindform/AGENTS.md
git commit -m "docs: describe shared workspaces (USAGE + SPEC + mindform AGENTS)

USAGE gets a worked example for single-mindform binds, ro mounts,
shared substrate between two mindforms, and the uid note. SPEC §137
annotated to clarify /workspace/ is orthogonal to the ontology
boundary. docker/mindform/AGENTS.md gets a one-line pointer."
```

---

## Final verification

- [ ] **Step 1: Lint and vet**

Run: `gofmt -l . && go vet ./...`
Expected: no output.

- [ ] **Step 2: Full unit test suite**

Run: `go test ./...`
Expected: all pass.

- [ ] **Step 3: Integration tests**

Run: `go test -tags=integration ./...`
Expected: all pass (requires Docker daemon).

- [ ] **Step 4: Build the binary and a quick smoke**

```bash
go build -o /tmp/eidos ./cmd/eidos
/tmp/eidos forge workspace --help
```

Expected: the help text shows `add`, `remove`, `list` subcommands.
