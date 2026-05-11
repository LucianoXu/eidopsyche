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
//
// On Linux this picks /etc/systemd/system over ~/.config/systemd/user. On
// macOS it picks /Library/LaunchDaemons over ~/Library/LaunchAgents. On
// Windows the SCM database is host-wide regardless, so the flag exists for
// API parity but does not change behavior.
func addSystemFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&useSystemServices, "system", false,
		"manage host-wide units (Linux: /etc/systemd/system, root; macOS: /Library/LaunchDaemons, root; Windows: no-op, SCM is host-wide)")
}

// serviceManagerFactory is the indirection tests substitute to inject a
// fake service.Manager. Production callers leave it nil and fall through
// to the real platform manager via service.New. When set, the factory's
// scope argument is the boolean useSystem flag (true = ScopeSystem).
var serviceManagerFactory func(useSystem bool) (service.Manager, error)

// buildServiceManager wires up a service.Manager from the running binary's
// path, the resolved gate state directory, and the --system flag. Returns
// a typed error when the host platform has no service-manager support so
// callers can produce a friendly message.
func buildServiceManager() (service.Manager, error) {
	return buildServiceManagerForScope(useSystemServices)
}

// buildServiceManagerForScope is buildServiceManager parameterised by the
// scope choice instead of the global --system flag. Used by `eidos gate
// restart --if-running` so it can iterate {user, system} scopes and
// restart whichever one actually has the daemon installed — without it,
// a daemon installed via `eidos gate start --system` would be missed by
// install.sh's post-install hook (which runs unprivileged in user scope).
func buildServiceManagerForScope(useSystem bool) (service.Manager, error) {
	if serviceManagerFactory != nil {
		return serviceManagerFactory(useSystem)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate eidos binary: %w", err)
	}
	stateDir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return nil, err
	}
	scope := service.ScopeUser
	if useSystem {
		scope = service.ScopeSystem
	}
	mgr, err := service.New(service.Config{
		BinaryPath: exe,
		StateDir:   stateDir,
		Scope:      scope,
	})
	if err != nil {
		if errors.Is(err, service.ErrUnsupported) {
			return nil, fmt.Errorf(`%w
Run the daemon and relay manually instead. Linux/macOS:
  eidos gate daemon &
  eidos relay start &
PowerShell:
  Start-Job -Name eidos-gate -ScriptBlock { eidos.exe gate daemon }
  Start-Job -Name eidos-relay -ScriptBlock { eidos.exe relay start }`, err)
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

// printRelaysSection appends a "Relays:" block to w with one row per
// known URL. Silently no-ops when the daemon isn't reachable. Reads
// from `state.get relays` which returns a map keyed by URL with state,
// role, last_error, and last_event_at fields merged.
func printRelaysSection(w io.Writer) {
	c, err := newClient()
	if err != nil {
		return
	}
	defer c.Close()
	relays := map[string]map[string]any{}
	if err := mustOK(c.Call("state.get", map[string]string{"path": "relays"}, &relays)); err != nil {
		return
	}
	if len(relays) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Relays:")
	for url, r := range relays {
		role, _ := r["role"].(string)
		state, _ := r["state"].(string)
		lastErr, _ := r["last_error"].(string)
		// JSON numbers come back as float64; cast through.
		var lastEventAt int64
		if v, ok := r["last_event_at"].(float64); ok {
			lastEventAt = int64(v)
		}
		extra := ""
		if lastErr != "" {
			extra = "  (" + lastErr + ")"
		} else if lastEventAt > 0 {
			extra = fmt.Sprintf("  (last event %s ago)", humanSinceUnix(lastEventAt))
		}
		fmt.Fprintf(w, "  %-9s %-32s %s%s\n", role, url, state, extra)
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
