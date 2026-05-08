//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

// TestRelayHealth_ReportsConnected verifies that after a daemon successfully
// subscribes to a relay, relays.health reports the URL as 'connected'.
// This exercises the SetStateHook → relayHealthStore.setState → IPC
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
	var rows []daemon.RelayHealth
	if e, err := c.Call("relays.health", nil, &rows); err != nil || e != nil {
		t.Fatalf("relays.health: %v %+v", err, e)
	}
	var bobsViewOfA *daemon.RelayHealth
	for i := range rows {
		if rows[i].URL == a.relayURL {
			bobsViewOfA = &rows[i]
			break
		}
	}
	if bobsViewOfA == nil {
		t.Fatalf("bob did not surface a's URL %s in relays.health: rows=%+v", a.relayURL, rows)
	}
	if bobsViewOfA.State != "connected" {
		t.Fatalf("expected state=connected, got state=%q lastErr=%q", bobsViewOfA.State, bobsViewOfA.LastError)
	}
	if bobsViewOfA.Role == "" {
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
