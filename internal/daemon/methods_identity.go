package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// whoami returns our public identity plus configured home relays.
func whoami(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url, role FROM own_relays`)
	if err != nil {
		return nil, internalErr(err)
	}
	defer rows.Close()
	var relays []map[string]string
	for rows.Next() {
		var u, r string
		if err := rows.Scan(&u, &r); err != nil {
			return nil, internalErr(err)
		}
		relays = append(relays, map[string]string{"url": u, "role": r})
	}
	label, _ := d.DB.GetMeta(ctx, "label")
	return map[string]any{
		"pubkey":      d.Key.PublicHex,
		"npub":        d.Key.Npub,
		"label":       label,
		"home_relays": relays,
	}, nil
}

// setOwnLabel updates the label that this entity advertises (whoami / card).
// Empty labels are rejected — every identity must show a non-empty name.
func setOwnLabel(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Label string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "label must not be empty"}
	}
	if err := d.DB.SetMeta(ctx, "label", label); err != nil {
		return nil, internalErr(err)
	}
	d.emitDashEvent(dashboard.Event{Kind: "identity.label-changed"})
	return map[string]string{"label": label}, nil
}
