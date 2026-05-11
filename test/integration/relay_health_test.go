//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

// TestRelayHealth_ReportsConnected verifies that after a daemon successfully
// subscribes to a relay, state.get relays reports the URL as 'connected'.
// This exercises the SetStateHook → relayHealthStore.setState → state.get
// snapshot path end to end.
func TestRelayHealth_ReportsConnected(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	a := bringUp(t, "alice", relayd.ModePublic)
	b := bringUpDaemonOnly(t, "bob", a.relayURL)

	addContact(t, a, b)
	addContact(t, b, a)

	// Allow subscriber loops to attach.
	time.Sleep(1500 * time.Millisecond)

	c := dialIPC(t, b)
	resp := map[string]map[string]any{}
	if e, err := c.Call("state.get", map[string]string{"path": "relays"}, &resp); err != nil || e != nil {
		t.Fatalf("state.get relays: %v %+v", err, e)
	}
	entry, ok := resp[a.relayURL]
	if !ok {
		t.Fatalf("bob did not surface a's URL %s in state.get relays: resp=%+v", a.relayURL, resp)
	}
	state, _ := entry["state"].(string)
	lastErr, _ := entry["last_error"].(string)
	role, _ := entry["role"].(string)
	if state != "connected" {
		t.Fatalf("expected state=connected, got state=%q lastErr=%q", state, lastErr)
	}
	if role == "" {
		t.Errorf("Role should be set (home), got empty")
	}
}

// (Disconnect-detection coverage lives at the unit-test level —
// internal/daemon/relayhealth_test.go exercises the state machine
// directly. Driving a force-disconnect through a real WebSocket from
// the integration layer is structurally hard: http.Server.Close does
// not terminate hijacked WebSocket connections, so the daemon's
// Pool.Subscribe pump keeps reading from a TCP socket that's only
// dead by network timeout. A targeted disconnect test would need a
// custom listener wrapper that exposes per-connection close — out of
// scope for this milestone. The connect-detection test above is the
// meaningful end-to-end signal: it proves the SetStateHook plumbing
// reaches the IPC snapshot.)
