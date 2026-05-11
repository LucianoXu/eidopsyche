package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
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
// registry and routes the write through daemon.Mutate. The Mutate
// helper performs the context check (rejects mindform-only keys on
// host with a CONTEXT_MISMATCH error pointing to forge config),
// serializes via stateMu, dispatches the apply hook (e.g. crontab
// hot-reload for heartbeat.interval), and emits state.changed.
func configSet(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p ConfigSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	key, ok := config.KeyByPath(p.Path)
	if !ok {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "unknown config key: " + p.Path}
	}

	var rollbackArmed bool
	var savedOld string
	err := d.Mutate(context.Background(), "config."+p.Path, key.Contexts,
		func() (any, any, error) {
			cfg, err := config.Load(d.configPath())
			if err != nil {
				return nil, nil, err
			}
			currentVal := key.Get(&cfg)
			// Second invocation is the rollback path: restore savedOld
			// instead of applying p.Value again.
			target := p.Value
			isRollback := rollbackArmed
			if !isRollback {
				savedOld = currentVal
				rollbackArmed = true
			} else {
				target = savedOld
			}
			if err := key.Set(&cfg, target); err != nil {
				if isRollback {
					return nil, nil, err
				}
				// Initial-call validation failure: surface as INVALID_PARAMS.
				return nil, nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
			}
			if err := config.Save(d.configPath(), cfg); err != nil {
				return nil, nil, err
			}
			return currentVal, key.Get(&cfg), nil
		})
	if err != nil {
		return nil, asIPCError(err)
	}
	cfg, err := config.Load(d.configPath())
	if err != nil {
		return nil, internalErr(err)
	}
	return cfg, nil
}

// asIPCError unwraps an error into the typed IPC shape. Mutate may
// return either an *ipc.Error (from context-mismatch / inconsistent
// rollback / handlers that surface typed errors directly) or a plain
// error from a handler's write / apply hook (which becomes INTERNAL).
func asIPCError(err error) *ipc.Error {
	var ipcErr *ipc.Error
	if errors.As(err, &ipcErr) {
		return ipcErr
	}
	return internalErr(err)
}
