package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

// useSystemServices is a shared package-level flag value, written by the
// --system flag attached to start / stop / status / purge. Mutually consistent
// across the four commands by virtue of being the same variable.
var useSystemServices bool

// addSystemFlag attaches the --system flag to a service-management command.
func addSystemFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&useSystemServices, "system", false,
		"manage system-wide units (/etc/systemd/system, requires root) instead of user units")
}

// buildServiceManager wires up a service.Manager from the running binary's
// path, the resolved gate state directory, and the --system flag. Returns
// a typed error when the host platform has no service-manager support so
// callers can produce a friendly message.
//
// withRelay controls whether Install / Start will manage the relay unit.
// stop / status / purge pass false because their backing methods iterate
// both unit names regardless; only start needs the live config value
// (use buildServiceManagerForStart for that).
func buildServiceManager(withRelay bool) (service.Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate eidos binary: %w", err)
	}
	stateDir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return nil, err
	}
	scope := service.ScopeUser
	if useSystemServices {
		scope = service.ScopeSystem
	}
	mgr, err := service.New(service.Config{
		BinaryPath: exe,
		StateDir:   stateDir,
		Scope:      scope,
		WithRelay:  withRelay,
	})
	if err != nil {
		if errors.Is(err, service.ErrUnsupported) {
			return nil, fmt.Errorf(`%w
Run the daemon and relay manually instead:
  eidos gate daemon &
  eidos gate relay &`, err)
		}
		return nil, err
	}
	return mgr, nil
}

// loadGateConfig resolves the state dir, reads config.toml from it, and
// returns the parsed config plus the state dir. A missing config.toml is
// reported as a typed error suggesting `eidos gate init`; a malformed
// config.toml is surfaced verbatim. Used wherever a command's behavior
// depends on the persisted config — silent fallback to Defaults() can
// hide real problems (uninitialized state dir, hand-edited typo).
func loadGateConfig() (config.Config, string, error) {
	stateDir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return config.Config{}, "", err
	}
	path := filepath.Join(stateDir, "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, stateDir, fmt.Errorf("state directory not initialized at %s; run `eidos gate init`", stateDir)
		}
		return config.Config{}, stateDir, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg, stateDir, nil
}

// printStatus formats Manager.Status() output for human eyes.
func printStatus(ctx context.Context, w io.Writer, m service.Manager) error {
	statuses, err := m.Status(ctx)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		state := "not-installed"
		switch {
		case s.Active:
			state = "active"
		case s.Enabled:
			state = "enabled-but-stopped"
		case s.Installed:
			state = "installed"
		}
		pid := ""
		if s.PID > 0 {
			pid = fmt.Sprintf("  pid=%d", s.PID)
		}
		fmt.Fprintf(w, "  %-22s %s%s\n", s.Name, state, pid)
	}
	return nil
}
