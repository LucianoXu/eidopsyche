package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/cron"
)

// withTempInstaller swaps the package-level heartbeatInstaller for a
// tempdir-backed installer with no sudo. Hermetic: tests never touch
// the host's /var/spool/cron, never shell out, never depend on
// passwordless sudo. Returns the spool path so tests can read what
// the hook wrote.
func withTempInstaller(t *testing.T) string {
	t.Helper()
	spool := filepath.Join(t.TempDir(), "eidos")
	prev := heartbeatInstaller
	heartbeatInstaller = func() *cron.Installer {
		return &cron.Installer{SpoolPath: spool} // SudoCommand="" → direct write
	}
	t.Cleanup(func() { heartbeatInstaller = prev })
	return spool
}

// TestHeartbeatApplyHook_RendersAndInstalls confirms the happy path:
// the hook renders the interval and writes the crontab body atomically.
func TestHeartbeatApplyHook_RendersAndInstalls(t *testing.T) {
	spool := withTempInstaller(t)
	if err := heartbeatApplyHook(context.Background(), nil, "2h", "1m"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(spool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "*/1 * * * * ") {
		t.Errorf("expected */1 crontab; got %q", body)
	}
}

// TestHeartbeatApplyHook_RenderError surfaces invalid intervals via
// the returned error before any install attempt.
func TestHeartbeatApplyHook_RenderError(t *testing.T) {
	withTempInstaller(t)
	err := heartbeatApplyHook(context.Background(), nil, "2h", "garbage")
	if err == nil {
		t.Fatal("expected error for unsupported interval")
	}
	if !strings.Contains(err.Error(), "heartbeat apply") {
		t.Errorf("error should mention heartbeat apply; got %v", err)
	}
	if !strings.Contains(err.Error(), "render") {
		t.Errorf("error should mention render stage; got %v", err)
	}
}

// TestHeartbeatApplyHook_EmptyDefaultsTo2h confirms "" interval is
// treated as DefaultHeartbeatInterval — the operator clearing the
// override falls back to the system default.
func TestHeartbeatApplyHook_EmptyDefaultsTo2h(t *testing.T) {
	spool := withTempInstaller(t)
	if err := heartbeatApplyHook(context.Background(), nil, "1m", ""); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(spool)
	if !strings.HasPrefix(string(body), "0 */2 * * * ") {
		t.Errorf("expected default 2h crontab; got %q", body)
	}
}

// TestHeartbeatApplyHook_NonStringRejected guards against future
// registry bugs where a non-string value reaches the hook — should
// fail loudly rather than silently render the default.
func TestHeartbeatApplyHook_NonStringRejected(t *testing.T) {
	withTempInstaller(t)
	err := heartbeatApplyHook(context.Background(), nil, nil, 42)
	if err == nil {
		t.Fatal("expected error for non-string interval")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("error should mention type; got %v", err)
	}
}
