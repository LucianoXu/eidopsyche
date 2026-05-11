package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// setOwnLabel updates the label that this entity advertises (whoami / card).
// Empty labels are rejected — every identity must show a non-empty name.
// Routes through daemon.Mutate so the change emits state.changed via the
// common path; the legacy dashboard.Event{Kind: "identity.label-changed"}
// is preserved for backward-compat dashboard subscribers.
func setOwnLabel(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Label string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "label must not be empty"}
	}
	err := d.Mutate(ctx, "identity.label", config.BothCtx,
		func() (any, any, error) {
			old, _ := d.DB.GetMeta(ctx, "label")
			if err := d.DB.SetMeta(ctx, "label", label); err != nil {
				return nil, nil, err
			}
			return old, label, nil
		})
	if err != nil {
		return nil, asIPCError(err)
	}
	d.emitDashEvent(dashboard.Event{Kind: "identity.label-changed"})
	return map[string]string{"label": label}, nil
}
