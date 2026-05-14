package daemon

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// seedEmptyConfig writes an empty (defaults) config.toml into the daemon's
// state dir so config.Load succeeds for tests that read/write config.
func seedEmptyConfig(t *testing.T, d *Daemon) {
	t.Helper()
	if err := config.Save(filepath.Join(d.StateDir, "config.toml"), config.Config{}); err != nil {
		t.Fatal(err)
	}
}

func TestForgeWorkspaceAdd_Persists(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir, "mode": "rw"})
	out, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	res := out.(forgeWorkspaceAddResult)
	if !res.PendingRestart {
		t.Errorf("want pending_restart=true; got %#v", res)
	}
	cfg, err := config.Load(d.configPath())
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Forge["alice"].Workspaces
	want := []config.WorkspaceMount{{Name: "proj-x", HostPath: dir, Mode: "rw"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("config not updated:\nwant %#v\n got %#v", want, got)
	}
}

func TestForgeWorkspaceAdd_DefaultsModeRW(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	cfg, _ := config.Load(d.configPath())
	got := cfg.Forge["alice"].Workspaces[0]
	if got.Mode != "" {
		t.Errorf("Mode persisted as %q; want empty", got.Mode)
	}
	if got.EffectiveMode() != "rw" {
		t.Errorf("EffectiveMode=%q; want rw", got.EffectiveMode())
	}
}

func TestForgeWorkspaceAdd_RejectsBadName(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "Bad_Name", "host_path": dir})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrInvalidParams {
		t.Errorf("want INVALID_PARAMS; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_RejectsBadPath(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": "relative/path"})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || !strings.Contains(ipcErr.Message, "absolute") {
		t.Errorf("want abs-path validation error; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_RejectsDuplicate(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
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
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "proj-x", "host_path": dir, "mode": "wr"})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || !strings.Contains(ipcErr.Message, "mode") {
		t.Errorf("want mode validation error; got %v", ipcErr)
	}
}

func TestForgeWorkspaceAdd_DangerousPathWarn(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	d.SeedMindForm("alice")
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "etc", "host_path": "/etc", "mode": "ro"})
	out, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	res := out.(forgeWorkspaceAddResult)
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "|"), "host system configuration") {
		t.Errorf("expected /etc dangerous warning; got %#v", res.Warnings)
	}
	cfg, _ := config.Load(d.configPath())
	if len(cfg.Forge["alice"].Workspaces) != 1 {
		t.Errorf("dangerous path should still be persisted")
	}
}

func TestForgeWorkspaceAdd_RejectsUnknownMindform(t *testing.T) {
	d := newTestDaemon(t)
	seedEmptyConfig(t, d)
	// no SeedMindForm
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"mindform": "nobody", "name": "proj-x", "host_path": dir})
	_, ipcErr := forgeWorkspaceAdd(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrForgeNotFound {
		t.Errorf("want FORGE_NOT_FOUND; got %v", ipcErr)
	}
}

func TestForgeWorkspaceRemove_Drops(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	seedEmptyConfig(t, d)
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
	cfg, _ := config.Load(d.configPath())
	if _, exists := cfg.Forge["alice"]; exists {
		t.Errorf("alice's [forge] block should be removed when last workspace is dropped; got %#v", cfg.Forge["alice"])
	}
}

func TestForgeWorkspaceRemove_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	seedEmptyConfig(t, d)
	raw, _ := json.Marshal(map[string]any{"mindform": "alice", "name": "missing"})
	_, ipcErr := forgeWorkspaceRemove(context.Background(), d, nil, raw)
	if ipcErr == nil || ipcErr.Code != ipc.ErrWorkspaceNotFound {
		t.Errorf("want WORKSPACE_NOT_FOUND; got %v", ipcErr)
	}
}

// ---------------------------------------------------------------------------
// fakeForgeClient — minimal forgectl.Client stub for forge.workspace.list
// ---------------------------------------------------------------------------

type fakeForgeClient struct {
	mounts map[string][]forgectl.Mount
}

func (f *fakeForgeClient) ContainerInspectMounts(_ context.Context, name string) ([]forgectl.Mount, error) {
	if m, ok := f.mounts[name]; ok {
		return m, nil
	}
	return nil, nil
}

func (f *fakeForgeClient) VolumeExists(_ context.Context, _ string) (bool, error) { return true, nil }
func (f *fakeForgeClient) VolumeCreate(_ context.Context, _ string) error         { return nil }
func (f *fakeForgeClient) VolumeRemove(_ context.Context, _ string) error         { return nil }
func (f *fakeForgeClient) ContainerExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (f *fakeForgeClient) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return "running", nil
}
func (f *fakeForgeClient) ContainerCreate(_ context.Context, _ forgectl.CreateOpts) error { return nil }
func (f *fakeForgeClient) ContainerStart(_ context.Context, _ string) error               { return nil }
func (f *fakeForgeClient) ContainerStop(_ context.Context, _ string, _ int) error         { return nil }
func (f *fakeForgeClient) ContainerRemove(_ context.Context, _ string) error              { return nil }
func (f *fakeForgeClient) ContainerInspectImage(_ context.Context, _ string) (string, string, error) {
	return "", "", nil
}
func (f *fakeForgeClient) ImageInspectLabels(_ context.Context, _ string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f *fakeForgeClient) ImageInspectID(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *fakeForgeClient) RunInit(_ context.Context, _ forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{}, nil
}
func (f *fakeForgeClient) ContainerExec(_ context.Context, _ string, _ []string) (forgectl.ExecResult, error) {
	return forgectl.ExecResult{}, nil
}
func (f *fakeForgeClient) ImageExists(_ context.Context, _ string) (bool, error)    { return true, nil }
func (f *fakeForgeClient) ImagePull(_ context.Context, _ string, _ io.Writer) error { return nil }
func (f *fakeForgeClient) VolumeList(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeForgeClient) ContainerLogs(_ context.Context, _ string, _ bool, _ io.Writer) error {
	return nil
}
func (f *fakeForgeClient) CopyFromContainer(_ context.Context, _, _ string, _ io.Writer) error {
	return nil
}

var _ forgectl.Client = (*fakeForgeClient)(nil)

// ---------------------------------------------------------------------------
// forge.workspace.list tests
// ---------------------------------------------------------------------------

func TestForgeWorkspaceList_PendingRestart(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	seedEmptyConfig(t, d)
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
		t.Errorf("want pending_restart=true (actual lacks proj-x); got %#v", res)
	}
	if len(res.Desired) != 1 || res.Desired[0].Name != "proj-x" {
		t.Errorf("desired: %#v", res.Desired)
	}
	if len(res.Actual) != 0 {
		t.Errorf("actual should be empty (only /eidos volume mount, no /workspace/ bind); got %#v", res.Actual)
	}
}

func TestForgeWorkspaceList_InSync(t *testing.T) {
	d := newTestDaemon(t)
	d.SeedMindForm("alice")
	seedEmptyConfig(t, d)
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
