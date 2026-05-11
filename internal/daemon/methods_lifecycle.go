package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

// serviceStatus returns daemon process metadata + the current
// lifecycle-job snapshot. Surfaces (dashboard Service tab,
// `eidos gate status --json`) read this method without poking at
// individual *Daemon fields.
func serviceStatus(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	out := dashboard.ServiceStatus{
		Version:      version.Version,
		Commit:       version.Commit,
		BuildDate:    version.BuildDate,
		StartedAt:    d.startedAt,
		StateDir:     d.StateDir,
		DashboardURL: "http://" + d.Cfg.Dashboard.Listen,
		IPCSocket:    filepath.Join(d.StateDir, d.Cfg.Daemon.Socket),
	}
	if life := d.LifecycleStatusSnapshot(); life.Active {
		out.ActiveJobID = life.JobID
		out.ActiveJobArgs = life.Args
		out.ActiveJobAt = life.Started
	}
	return out, nil
}

// LifecycleRunParams is the JSON-stable parameter shape for
// "lifecycle.run". Args is the eidos sub-command argv as it would be
// passed on the command line (e.g. ["gate","reconnect"]).
type LifecycleRunParams struct {
	Args []string `json:"args"`
}

// lifecycleRunMethod kicks off a lifecycle subprocess and returns the
// job id. The streaming SSE channel keeps the line pump; this method is
// unary RPC. Concurrent calls return LIFECYCLE_BUSY (mapped by the
// dashboard handler to HTTP 409).
func lifecycleRunMethod(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p LifecycleRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	id, err := d.LifecycleRun(p.Args)
	if err != nil {
		if errors.Is(err, ErrLifecycleBusy) {
			return nil, &ipc.Error{Code: ipc.ErrLifecycleBusy, Message: err.Error()}
		}
		return nil, internalErr(err)
	}
	return map[string]string{"job_id": id}, nil
}

// lifecycleStatusMethod returns the current lifecycle snapshot. Useful
// for poll callers; SSE subscribers prefer the push channel.
func lifecycleStatusMethod(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	return d.LifecycleStatusSnapshot(), nil
}

// versionMethod returns the daemon and schema versions.
func versionMethod(_ context.Context, _ *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	return map[string]any{
		"daemon_version": "0.0.1",
		"schema_version": 1,
	}, nil
}

// subscribeRefresh asks the daemon to recompute its relay set and reattach.
func subscribeRefresh(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}
