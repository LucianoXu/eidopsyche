package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/state"
)

// Mutate is the common wrapper every state mutation handler funnels
// through. It performs:
//
//  1. Context check: if requires doesn't intersect d.Context, return
//     ipc.ErrContextMismatch with a message pointing to the correct verb.
//  2. Acquire d.configMu (the state-mutation lock; shared with the older
//     config write path to keep them mutually exclusive during the migration).
//  3. Run write() — the handler's per-domain persistence work. It must
//     return (old, new, err): old = value before the write, new = value
//     after. On err, Mutate aborts before apply.
//  4. Dispatch the apply hook for path. On error: if the error allows
//     rollback (default true; opt out via state.Rollbacker), re-invoke
//     write() to restore old state. If rollback itself fails, return
//     ipc.ErrInconsistent wrapping both.
//  5. emit state.changed event (best-effort; subscribers re-snapshot on
//     reconnect).
//
// Handlers wrap their per-domain logic in a closure passed as write.
// The closure should be idempotent on rollback (the second invocation
// is given a chance to read old state from old and reapply it).
func (d *Daemon) Mutate(
	ctx context.Context,
	path string,
	requires config.Context,
	write func() (old any, new any, err error),
) error {
	if requires != 0 && d.Context&requires == 0 {
		return contextMismatchError(path, requires)
	}
	d.configMu.Lock()
	defer d.configMu.Unlock()

	old, newVal, err := write()
	if err != nil {
		return err
	}

	if applyErr := d.applyRegistry.Dispatch(ctx, d.applyDepsValue, path, old, newVal); applyErr != nil {
		if shouldRollback(applyErr) {
			// Re-invoke write with a rollback hint via context — but our
			// signature doesn't carry that, so handlers that need to
			// know "this is a rollback call" must encode it themselves
			// (e.g. by closing over a flag). The simpler model: write
			// must be idempotent + the handler decides how to restore
			// using the `old` snapshot it just captured. We just call
			// write a second time and trust that it converges. If
			// handlers can't do this they should opt out via Rollbacker.
			if _, _, rbErr := write(); rbErr != nil {
				return ipc.WrapInconsistent(applyErr, rbErr)
			}
		}
		return applyErr
	}
	d.emitDashEvent(dashboard.Event{Kind: "state.changed:" + path})
	return nil
}

// shouldRollback returns true unless the error implements
// state.Rollbacker with Rollback() == false.
func shouldRollback(err error) bool {
	var rb state.Rollbacker
	if errors.As(err, &rb) {
		return rb.Rollback()
	}
	return true
}

// contextMismatchError builds a CONTEXT_MISMATCH error whose message
// points the caller at the correct verb. The hint is intentionally
// scoped to the heartbeat case (the immediate trigger) and a generic
// fallback for other paths; richer per-path hints can grow over time
// as we discover what operators try.
func contextMismatchError(path string, requires config.Context) error {
	switch {
	case requires == config.ContainerCtx && path == "config.heartbeat.interval":
		return &ipc.Error{
			Code: ipc.ErrContextMismatch,
			Message: "heartbeat.interval is a mindform-only key. To set it on a specific mindform from the host:\n" +
				"  eidos forge config <name> --heartbeat-interval <duration>",
		}
	case requires == config.ContainerCtx:
		return &ipc.Error{
			Code:    ipc.ErrContextMismatch,
			Message: fmt.Sprintf("%s is only settable inside a mindform container", path),
		}
	case requires == config.HostCtx:
		return &ipc.Error{
			Code:    ipc.ErrContextMismatch,
			Message: fmt.Sprintf("%s is only settable on the host daemon", path),
		}
	default:
		return &ipc.Error{
			Code:    ipc.ErrContextMismatch,
			Message: fmt.Sprintf("%s requires a context not available here", path),
		}
	}
}
