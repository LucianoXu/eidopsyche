package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// relayList returns own relays ordered by role then URL. Result is
// the typed OwnRelayRow slice — AddedAt rides along so dashboard and
// CLI can both decode the same projection. The CLI ignores AddedAt.
func relayList(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	return rows, nil
}

// relayAdd inserts a relay URL with the given role (home|fallback).
func relayAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		URL  string `json:"url"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if err := d.AddOwnRelay(ctx, p.URL, p.Role); err != nil {
		switch {
		case errors.Is(err, errOwnRelayInvalidURL),
			errors.Is(err, errOwnRelayInvalidRole),
			errors.Is(err, errOwnRelayDuplicate):
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

// relayRemove deletes a relay URL from own_relays.
func relayRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ URL string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if err := d.RemoveOwnRelay(ctx, p.URL); err != nil {
		switch {
		case errors.Is(err, errOwnRelayNotFound):
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		case errors.Is(err, errOwnRelayInvalidURL),
			errors.Is(err, errOwnRelayHomeRequired):
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

// relaysHealth returns the daemon's per-URL connection state. Consumers:
// `eidos gate status`, `eidos gate whoami`, the dashboard.
func relaysHealth(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	if d.relayHealth == nil {
		return []RelayHealth{}, nil
	}
	return d.relayHealth.snapshot(), nil
}

// relayListContains is a local set-membership helper used by
// contact.add-from-card's relay-hint dedup. Compares after normRelayURL
// so a pre-normalization legacy hint like `wss://x/` is recognized as
// the same endpoint as a freshly-canonicalized `wss://x`; without this,
// re-scanning the same card would append a duplicate relay row to a
// contact whose existing relay was written before normalization landed.
func relayListContains(s []string, x string) bool {
	want := normRelayURL(x)
	for _, v := range s {
		if normRelayURL(v) == want {
			return true
		}
	}
	return false
}
