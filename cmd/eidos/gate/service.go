package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

// printStatus formats Manager.Status() output for human eyes, plus a
// Relays section pulled from the daemon's relays.health IPC. The IPC
// section is best-effort: when the daemon socket isn't reachable
// (paused, between stop / start, or freshly purged) we silently skip it
// — status's primary contract is the unit table.
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
	printRelaysSection(w)
	return nil
}

// relayHealthRow is the IPC wire shape of one relays.health entry. Mirrors
// daemon.RelayHealth but kept local so the gate CLI doesn't import daemon.
type relayHealthRow struct {
	URL         string `json:"url"`
	Role        string `json:"role"`
	State       string `json:"state"`
	LastError   string `json:"last_error,omitempty"`
	LastEventAt int64  `json:"last_event_at,omitempty"`
}

// printRelaysSection appends a "Relays:" block to w with one row per
// known URL. Silently no-ops when the daemon isn't reachable.
func printRelaysSection(w io.Writer) {
	c, err := newClient()
	if err != nil {
		return
	}
	defer c.Close()
	var rows []relayHealthRow
	if err := mustOK(c.Call("relays.health", nil, &rows)); err != nil {
		return
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Relays:")
	for _, r := range rows {
		extra := ""
		if r.LastError != "" {
			extra = "  (" + r.LastError + ")"
		} else if r.LastEventAt > 0 {
			extra = fmt.Sprintf("  (last event %s ago)", humanSinceUnix(r.LastEventAt))
		}
		fmt.Fprintf(w, "  %-9s %-32s %s%s\n", r.Role, r.URL, r.State, extra)
	}
}

// humanSinceUnix is a small "Ns/Nm/Nh/Nd"-style formatter for the
// relay-health "last event ago" annotation. Imports time only for the
// math; avoids %v on a Duration which prints noisy nanoseconds.
func humanSinceUnix(unix int64) string {
	d := time.Now().Unix() - unix
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60:
		return fmt.Sprintf("%ds", d)
	case d < 3600:
		return fmt.Sprintf("%dm", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh", d/3600)
	default:
		return fmt.Sprintf("%dd", d/86400)
	}
}
