package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
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
