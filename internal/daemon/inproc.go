package daemon

import (
	"context"
	"encoding/json"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// Call invokes a registered IPC method by name without going through the
// unix socket. params is marshaled to JSON, the method's handler runs,
// and the result is unmarshaled into out (which may be nil to discard).
//
// In-process callers (dashboard adapter, MCP server, future surfaces)
// MUST use Call rather than reaching into Daemon internals. The IPC
// socket and Call share the exact same handler functions, so any
// behavior change to a method takes effect for both transports at once.
//
// Naming note: this mirrors ipc.Client.Call's signature so the dashboard
// adapter and a future MCP server present the same shape as the CLI's
// transport-bound caller. The existing dispatchEnvelope on *Daemon is
// unrelated — it routes inbound Nostr rumors, not IPC method calls.
//
// Streaming methods (e.g. inbox.tail) are out of scope: they require a
// *ipc.Conn and Call passes nil. In-process surfaces use the daemon's
// dashboard event channel instead. See
// docs/superpowers/specs/2026-05-09-unified-call-path-design.md §4.1.
func (d *Daemon) Call(ctx context.Context, method string, params any, out any) error {
	fn, ok := methodTable[method]
	if !ok {
		return &ipc.Error{Code: ipc.ErrUnknownMethod, Message: method}
	}
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
		raw = b
	}
	res, ipcErr := fn(ctx, d, nil, raw)
	if ipcErr != nil {
		return ipcErr
	}
	if out == nil || res == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
	return nil
}
