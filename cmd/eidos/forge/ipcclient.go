package forge

import (
	"fmt"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// newIPCClient dials the host gate daemon's unix socket. Forge
// commands that go through the daemon's method table (e.g.
// workspace.*) use this. CLI-direct commands (start/stop/restart)
// continue to construct a forgectl.Client instead.
func newIPCClient() (*ipc.Client, error) {
	dir, err := config.ResolveStateDir("")
	if err != nil {
		return nil, err
	}
	cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
	socket := filepath.Join(dir, cfg.Daemon.Socket)
	c, err := ipc.Dial(socket)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (socket %s): %w", socket, err)
	}
	return c, nil
}

// mustOK collapses an ipc.Error and a transport error into one Go
// error. Mirrors cmd/eidos/gate/client.go's helper of the same name.
func mustOK(ipcErr *ipc.Error, err error) error {
	if err != nil {
		return err
	}
	if ipcErr != nil {
		return fmt.Errorf("%s: %s", ipcErr.Code, ipcErr.Message)
	}
	return nil
}
