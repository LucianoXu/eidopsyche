package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/state"
)

// StateGetParams is the JSON-stable parameter shape for the "state.get"
// IPC method. Omitted/empty Path returns the full root snapshot;
// otherwise the dotted path selects a subtree or scalar.
type StateGetParams struct {
	Path string `json:"path,omitempty"`
}

// stateGet is the single read IPC method. It resolves params.Path
// against the daemon's state.Tree, which is populated by domain-side
// contributors registered at daemon startup (host) and additionally by
// lifecycle.Attach (container PID-1).
//
// Path semantics, with examples:
//
//	""                              → full root snapshot, JSON
//	"config"                        → entire Config struct
//	"config.heartbeat.interval"     → scalar "1m"
//	"contacts"                      → map keyed by pubkey
//	"contacts.<pubkey>"             → single contact entry
//	"identity"                      → npub/hex/label
//	"unknown.path"                  → ipc.ErrPathNotFound
func stateGet(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p StateGetParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	snap, err := d.stateTree.Snapshot(ctx, p.Path)
	if err != nil {
		if errors.Is(err, state.ErrPathNotFound) {
			return nil, &ipc.Error{Code: ipc.ErrPathNotFound, Message: p.Path}
		}
		return nil, internalErr(err)
	}
	return snap, nil
}
