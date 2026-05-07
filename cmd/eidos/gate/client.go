package gate

import (
	"fmt"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func newClient() (*ipc.Client, error) {
	dir, err := config.ResolveStateDir(globalStateDir)
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

func mustOK(ipcErr *ipc.Error, err error) error {
	if err != nil {
		return err
	}
	if ipcErr != nil {
		return fmt.Errorf("%s: %s", ipcErr.Code, ipcErr.Message)
	}
	return nil
}
