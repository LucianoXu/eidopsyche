package daemon

import (
	"context"
	"encoding/json"

	"github.com/yingtexu/eidopsyche/internal/ipc"
)

// handler adapts Daemon to the ipc.Handler interface by dispatching on method
// name through methodTable.
type handler struct{ d *Daemon }

func (h *handler) Handle(ctx context.Context, conn *ipc.Conn, req *ipc.Request) (any, *ipc.Error) {
	fn, ok := methodTable[req.Method]
	if !ok {
		return nil, &ipc.Error{Code: ipc.ErrUnknownMethod, Message: req.Method}
	}
	return fn(ctx, h.d, conn, req.Params)
}

// methodFunc is the signature for all registered IPC method handlers.
type methodFunc func(ctx context.Context, d *Daemon, conn *ipc.Conn, params json.RawMessage) (any, *ipc.Error)

var methodTable = map[string]methodFunc{}

// register wires a method name to its implementation. Called from init()
// functions in methods.go.
func register(name string, fn methodFunc) { methodTable[name] = fn }
