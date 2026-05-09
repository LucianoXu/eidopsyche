package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/service"
)

// restartIfRunning is the package-level flag value backing --if-running on
// the restart command. Saved/restored by tests via the same global-flag
// pattern the rest of the gate CLI uses (see init / start / stop tests).
var restartIfRunning bool

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the gate daemon service",
	Long: `Restarts the gate daemon unit so a freshly-installed binary (e.g.
just placed by 'eidos self-update' or the install script) takes effect. The
daemon's identity, contacts, and inbox stay on disk; only the running
process is replaced.

By default an uninstalled or absent service is an error — restart presumes
the unit was set up via 'eidos gate start'. Pass --if-running to make the
command a silent no-op when nothing is installed; the install script uses
this mode so a fresh-install machine doesn't error out.

Auto-restart after self-update can be disabled with 'eidos self-update
--no-restart' or by exporting EIDOS_NO_RESTART=1 in the environment.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRestart(cmd.Context(), os.Stdout, restartIfRunning)
	},
}

// runRestart implements `eidos gate restart`. It is split out from the
// cobra RunE so tests can drive it directly with a fake service.Manager.
//
// Semantics:
//   - Service installed and active        → restart it; print status.
//   - Service installed but not active    → start it; print status.
//   - Service not installed, ifRunning    → exit 0 with a hint; no error.
//   - Service not installed, !ifRunning   → return an error pointing the
//     user at `eidos gate start`.
//   - Platform has no service backend     → exit 0 with a hint when
//     ifRunning is set (the install-script case on Windows pre-#15);
//     otherwise return the underlying ErrUnsupported.
//
// When --if-running is set, the function tries both scopes (user and
// system) so a daemon installed via `eidos gate start --system` is also
// picked up by install.sh's post-install hook (which runs unprivileged
// in user scope by default). The explicit --system flag narrows the
// search to system scope only, even with --if-running. Without
// --if-running, --system is respected verbatim — the user is asking us
// to act on a specific scope and we should not silently widen.
func runRestart(ctx context.Context, w io.Writer, ifRunning bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	scopes := scopesToTry(ifRunning, useSystemServices)
	tried := 0
	acted := false
	var lastErr error
	for _, useSystem := range scopes {
		ok, err := tryRestartScope(ctx, w, useSystem, ifRunning)
		if err != nil {
			lastErr = err
			if !ifRunning {
				return err
			}
			// best-effort: keep trying other scopes
			fmt.Fprintf(w, "gate restart: scope %s: %v\n", scopeName(useSystem), err)
			continue
		}
		tried++
		if ok {
			acted = true
		}
	}
	if acted {
		return nil
	}
	if ifRunning {
		fmt.Fprintln(w, "gate restart: daemon is not installed as a managed service; nothing to do")
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	if tried == 0 {
		return fmt.Errorf("gate daemon is not installed; run `eidos gate start` first")
	}
	return fmt.Errorf("gate daemon is not installed; run `eidos gate start` first")
}

// scopesToTry returns the ordered list of (useSystem) flags the
// restart loop should walk. The default is the explicitly-requested
// scope only; --if-running widens to both scopes unless --system was
// also passed (in which case the operator's narrowing intent wins).
func scopesToTry(ifRunning, explicitSystem bool) []bool {
	if !ifRunning {
		return []bool{explicitSystem}
	}
	if explicitSystem {
		return []bool{true}
	}
	return []bool{false, true}
}

// scopeName returns "user" or "system" for log lines.
func scopeName(useSystem bool) string {
	if useSystem {
		return "system"
	}
	return "user"
}

// tryRestartScope drives one scope's manager through Status → restart /
// start. Returns ok=true iff this scope had the daemon installed and
// either restarted or started it. Returns err on hard failures (Status
// error, restart error). The caller decides whether a hard failure is
// fatal (no --if-running) or just a best-effort skip (--if-running).
func tryRestartScope(ctx context.Context, w io.Writer, useSystem, ifRunning bool) (bool, error) {
	mgr, err := buildServiceManagerForScope(useSystem)
	if err != nil {
		if ifRunning && errors.Is(err, service.ErrUnsupported) {
			fmt.Fprintln(w, "gate restart: service management is not supported on this platform; nothing to do")
			return false, nil
		}
		return false, err
	}
	statuses, err := mgr.Status(ctx)
	if err != nil {
		return false, err
	}
	daemon, ok := findStatus(statuses, service.DaemonUnitName)
	if !ok || !daemon.Installed {
		return false, nil
	}

	// Installed but stopped → start. RestartDaemon on systemd / launchd
	// would do the right thing too, but going through Start makes the
	// "not running" path's intent explicit and avoids surprising the
	// operator with a "restart failed because the unit was inactive"
	// error from systemctl on edge configurations.
	if !daemon.Active {
		if err := mgr.StartDaemon(ctx); err != nil {
			return false, fmt.Errorf("start gate daemon: %w", err)
		}
		fmt.Fprintf(w, "✓ gate daemon started (was stopped, scope=%s)\n", scopeName(useSystem))
		return true, printStatus(ctx, w, mgr)
	}

	if err := mgr.RestartDaemon(ctx); err != nil {
		return false, fmt.Errorf("restart gate daemon: %w", err)
	}
	fmt.Fprintf(w, "✓ gate daemon restarted (scope=%s)\n", scopeName(useSystem))
	return true, printStatus(ctx, w, mgr)
}

// findStatus returns the Status entry for the named unit, if any.
func findStatus(statuses []service.Status, name string) (service.Status, bool) {
	for _, s := range statuses {
		if s.Name == name {
			return s, true
		}
	}
	return service.Status{}, false
}

func init() {
	addSystemFlag(restartCmd)
	restartCmd.Flags().BoolVar(&restartIfRunning, "if-running", false,
		"no-op (exit 0 with a hint) when the daemon isn't installed as a managed service; intended for install-script automation")
	rootCmd.AddCommand(restartCmd)
}
