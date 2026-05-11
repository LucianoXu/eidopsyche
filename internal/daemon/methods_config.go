package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// ConfigSetParams is the JSON-stable parameter shape for the "config.set"
// IPC method. Path is a dotted TOML path registered in
// internal/config/keys.go (e.g. "log_level"); Value is the string the
// key's Set parser will validate.
type ConfigSetParams struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

// configPath is the canonical location of config.toml inside the daemon
// state directory. Both configGet and configSet route through it so they
// always touch the same file.
func (d *Daemon) configPath() string {
	return filepath.Join(d.StateDir, "config.toml")
}

// configGet returns the current on-disk config snapshot. Surfaces use
// internal/config.KeyByPath/KeyList to extract individual values; the
// handler returns the full struct so single-key and full-list callers
// share one round trip.
func configGet(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	cfg, err := config.Load(d.configPath())
	if err != nil {
		return nil, internalErr(err)
	}
	return cfg, nil
}

// configSet validates the path/value pair against the config.Key
// registry, performs a locked read-modify-write of config.toml, and
// emits config.changed. The lock lives on *Daemon so concurrent calls
// from any surface (CLI over the socket, dashboard via in-process Call)
// serialise on the same mutex — fixing the three-way fork that PR #9
// review caught (CLI direct write + dashboard direct write under a
// mutex that only protected one writer).
func configSet(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p ConfigSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	key, ok := config.KeyByPath(p.Path)
	if !ok {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "unknown config key: " + p.Path}
	}
	d.configMu.Lock()
	defer d.configMu.Unlock()
	cfg, err := config.Load(d.configPath())
	if err != nil {
		return nil, internalErr(err)
	}
	if err := key.Set(&cfg, p.Value); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if err := config.Save(d.configPath(), cfg); err != nil {
		return nil, internalErr(err)
	}
	d.emitDashEvent(dashboard.Event{Kind: "config.changed"})
	return cfg, nil
}
