package gate

import (
	"context"
	"strings"
	"testing"
)

// TestHeartbeatApplyHook_RenderError surfaces invalid intervals via the
// returned error. Mutate's default rollback would re-run the write
// closure with the old value; this hook is purely render+install, so
// the test focuses on the render failure path.
func TestHeartbeatApplyHook_RenderError(t *testing.T) {
	err := heartbeatApplyHook(context.Background(), nil, "2h", "garbage")
	if err == nil {
		t.Fatal("expected error for unsupported interval")
	}
	if !strings.Contains(err.Error(), "heartbeat apply") {
		t.Errorf("error should mention heartbeat apply; got %v", err)
	}
}

// TestHeartbeatApplyHook_EmptyAcceptsDefault confirms "" interval is
// treated as DefaultHeartbeatInterval by cron.Render and the hook
// proceeds past render. The install step still fails in this unit test
// environment because there's no /var/spool/cron, but the failure is
// from install, not render — which validates the empty-string semantic.
func TestHeartbeatApplyHook_EmptyAcceptsDefault(t *testing.T) {
	err := heartbeatApplyHook(context.Background(), nil, "1m", "")
	// Either nil (sudo+tee actually works in dev env) or an install error
	// is acceptable here; what we DON'T want is a render error.
	if err != nil && strings.Contains(err.Error(), "render") {
		t.Errorf("empty interval should not fail render; got %v", err)
	}
}
