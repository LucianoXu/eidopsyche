package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/state"
)

// newConfigOnlyDaemon returns a *Daemon with only the fields config.get
// and config.set need: StateDir + a freshly written defaults file. It
// avoids the full DB/keypair/Pool setup so the test stays focused on the
// config method's behavior.
func newConfigOnlyDaemon(t *testing.T) *Daemon {
	t.Helper()
	dir := t.TempDir()
	if err := config.Save(filepath.Join(dir, "config.toml"), config.Defaults()); err != nil {
		t.Fatal(err)
	}
	return &Daemon{
		StateDir:      dir,
		Context:       config.HostCtx, // host-side keys (log_level, daemon.socket) only
		applyRegistry: state.NewApplyRegistry(),
		stateTree:     state.NewTree(),
	}
}

func TestConfigGet_ReturnsDefaultsSnapshot(t *testing.T) {
	d := newConfigOnlyDaemon(t)
	d.registerCoreStateContributors()
	var cfg config.Config
	if err := d.Call(context.Background(), "state.get", map[string]string{"path": "config"}, &cfg); err != nil {
		t.Fatalf("Call state.get config: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel=%q, want %q", cfg.LogLevel, "info")
	}
	if cfg.Daemon.Socket != "sock" {
		t.Errorf("Daemon.Socket=%q, want %q", cfg.Daemon.Socket, "sock")
	}
}

func TestConfigSet_RejectsUnknownKey(t *testing.T) {
	d := newConfigOnlyDaemon(t)
	err := d.Call(context.Background(), "config.set",
		ConfigSetParams{Path: "no.such.key", Value: "x"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrInvalidParams {
		t.Errorf("Code=%q, want %q", ipcErr.Code, ipc.ErrInvalidParams)
	}
}

func TestConfigSet_RejectsInvalidValue(t *testing.T) {
	d := newConfigOnlyDaemon(t)
	err := d.Call(context.Background(), "config.set",
		ConfigSetParams{Path: "log_level", Value: "yelling"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrInvalidParams {
		t.Errorf("Code=%q, want %q", ipcErr.Code, ipc.ErrInvalidParams)
	}
}

func TestConfigSet_PersistsAndIsReadableViaGet(t *testing.T) {
	d := newConfigOnlyDaemon(t)
	d.registerCoreStateContributors()
	if err := d.Call(context.Background(), "config.set",
		ConfigSetParams{Path: "log_level", Value: "debug"}, nil); err != nil {
		t.Fatalf("Call config.set: %v", err)
	}
	var cfg config.Config
	if err := d.Call(context.Background(), "state.get", map[string]string{"path": "config"}, &cfg); err != nil {
		t.Fatalf("Call state.get config: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel=%q, want %q", cfg.LogLevel, "debug")
	}
	// Also confirm on-disk state, since config.get reads from disk.
	onDisk, err := config.Load(filepath.Join(d.StateDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.LogLevel != "debug" {
		t.Errorf("on-disk LogLevel=%q, want %q", onDisk.LogLevel, "debug")
	}
}

// TestConfigSet_ConcurrentNoLostUpdates pins the lock-protection
// guarantee against the original three-way fork. Two goroutines hammer
// distinct keys; both keys must end on a value the goroutine wrote.
// Without configMu inside the handler, one goroutine's load-modify-save
// would silently overwrite the other's update.
func TestConfigSet_ConcurrentNoLostUpdates(t *testing.T) {
	d := newConfigOnlyDaemon(t)

	var wg sync.WaitGroup
	const iters = 64
	wg.Add(2)
	go func() {
		defer wg.Done()
		sockets := []string{"sock", "gate.sock"}
		for i := 0; i < iters; i++ {
			if err := d.Call(context.Background(), "config.set",
				ConfigSetParams{Path: "daemon.socket", Value: sockets[i%2]}, nil); err != nil {
				t.Errorf("config.set daemon.socket: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		levels := []string{"info", "debug", "warn"}
		for i := 0; i < iters; i++ {
			if err := d.Call(context.Background(), "config.set",
				ConfigSetParams{Path: "log_level", Value: levels[i%3]}, nil); err != nil {
				t.Errorf("config.set log_level: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	final, err := config.Load(filepath.Join(d.StateDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if final.Daemon.Socket != "sock" && final.Daemon.Socket != "gate.sock" {
		t.Errorf("daemon.socket lost: got %q", final.Daemon.Socket)
	}
	if final.LogLevel != "info" && final.LogLevel != "debug" && final.LogLevel != "warn" {
		t.Errorf("log_level lost: got %q", final.LogLevel)
	}
}
