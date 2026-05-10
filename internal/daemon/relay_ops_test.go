package daemon

import (
	"context"
	"errors"
	"testing"
)

// TestAddOwnRelay_CanonicalizesURL locks the invariant that
// `wss://x` and `wss://x/` collide on the unique relay_url constraint:
// without canonicalization the daemon would open two redundant WebSocket
// connections to the same Nostr endpoint (the trailing-slash form 503s
// at some relays, e.g. relay.damus.io).
func TestAddOwnRelay_CanonicalizesURL(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	if err := d.AddOwnRelay(ctx, "wss://relay.example/", "home"); err != nil {
		t.Fatalf("AddOwnRelay first: %v", err)
	}
	err := d.AddOwnRelay(ctx, "wss://relay.example", "fallback")
	if !errors.Is(err, errOwnRelayDuplicate) {
		t.Fatalf("AddOwnRelay second: err=%v, want errOwnRelayDuplicate", err)
	}

	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		t.Fatalf("ListOwnRelays: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly one row after canonical dedup, got %d: %+v", len(rows), rows)
	}
	if rows[0].URL != "wss://relay.example" {
		t.Errorf("URL=%q, want canonical wss://relay.example", rows[0].URL)
	}
}

func TestNormRelayURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wss://relay.example/", "wss://relay.example"},
		{"wss://relay.example", "wss://relay.example"},
		{"wss://Relay.Example/", "wss://relay.example"},
		{"  wss://relay.example/  ", "wss://relay.example"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normRelayURL(tc.in); got != tc.want {
			t.Errorf("normRelayURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
