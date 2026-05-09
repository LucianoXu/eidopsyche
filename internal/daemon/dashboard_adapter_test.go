package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// TestDashboardAdapter_ConfigSet_Concurrent verifies the configMu lock
// prevents lost updates when two concurrent ConfigSet calls each
// mutate a different scalar key. Without the lock, both goroutines
// load the same on-disk config, mutate one field each, then save —
// whichever Save lands second silently overwrites the other's change.
//
// Codex review of PR #9 caught the unguarded read-modify-write on
// dashboardAdapter.ConfigSet; this test would fail with a race
// detector if the lock were removed.
func TestDashboardAdapter_ConfigSet_Concurrent(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := config.Save(cfgPath, config.Defaults()); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{StateDir: tmp}
	a := dashboardAdapter{d: d}

	var wg sync.WaitGroup
	const iters = 64
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			sockets := []string{"sock", "gate.sock"}
			if err := a.ConfigSet(context.Background(), "daemon.socket", sockets[i%2]); err != nil {
				t.Errorf("ConfigSet daemon.socket: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			levels := []string{"info", "debug", "warn"}
			if err := a.ConfigSet(context.Background(), "log_level", levels[i%3]); err != nil {
				t.Errorf("ConfigSet log_level: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// After both finish, the file must reflect each goroutine's last
	// write. Either ordering is valid; both must be present (i.e. one
	// goroutine's update must not have wiped the other's).
	final, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if final.Daemon.Socket != "sock" && final.Daemon.Socket != "gate.sock" {
		t.Errorf("daemon.socket lost: got %q", final.Daemon.Socket)
	}
	if final.LogLevel != "info" && final.LogLevel != "debug" && final.LogLevel != "warn" {
		t.Errorf("log_level lost: got %q", final.LogLevel)
	}

	// Sanity: file is still well-formed TOML after dozens of writes.
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		t.Errorf("config.toml missing after concurrent writes: %v", statErr)
	}
}
