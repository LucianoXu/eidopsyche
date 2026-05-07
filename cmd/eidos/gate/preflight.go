package gate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

// preflightRelayPort verifies that the configured relay listen address can
// be bound right now. Called from `eidos gate start` before handing off to
// the OS service manager so the user gets a clear, actionable error instead
// of a cryptic systemd "failed" / launchd silent crash with the real cause
// hidden in journalctl.
//
// Behaviour:
//   - config.toml missing or unreadable → silent skip (let later steps
//     surface the real problem; the relay won't actually be started without
//     `gate init` first anyway).
//   - relay.listen empty after defaults → silent skip (no port to check).
//   - bind succeeds → returns nil after immediately releasing the port.
//   - bind fails with EADDRINUSE → returns a wrapped error naming the port
//     and pointing at lsof for finding the conflicting holder.
//   - bind fails for any other reason (e.g., permission denied on a
//     privileged port) → wrap and return — those are real preflight
//     failures the user should see before we install anything.
func preflightRelayPort(stateDir string) error {
	cfg, err := config.Load(filepath.Join(stateDir, "config.toml"))
	if err != nil {
		return nil
	}
	listen := cfg.Relay.Listen
	if listen == "" {
		listen = config.Defaults().Relay.Listen
	}
	if listen == "" {
		return nil
	}

	ln, err := net.Listen("tcp", listen)
	if err == nil {
		_ = ln.Close()
		return nil
	}
	if isAddrInUse(err) {
		port := portFromHostPort(listen)
		return fmt.Errorf(`relay port %s is already in use.

Find the holder with one of:
  lsof -i tcp:%s -sTCP:LISTEN
  ss -tlnp 'sport = :%s'   (Linux)
  netstat -anv -p tcp | grep .%s   (macOS fallback)

Then either stop the conflicting process, or change the gate's listen
address with:
  eidos gate config set relay.listen <new-host:port>
  eidos gate config set relay.public_url ws://<new-host:port>`,
			listen, port, port, port)
	}
	return fmt.Errorf("preflight: bind %s: %w", listen, err)
}

// isAddrInUse covers the syscall code on POSIX plus a substring fallback
// for hosts where errors.Is doesn't match (or the syscall package shape
// differs across platforms in unexpected ways).
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "address already in use")
}

// portFromHostPort returns the port component of a host:port string, or
// the original string if it doesn't parse.
func portFromHostPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}

// relayAlreadyManaged reports whether our managed relay unit is currently
// active. Used by `gate start` to decide whether to run the port preflight:
// if our own relay holds the port, that's not a conflict, it's idempotent
// success.
func relayAlreadyManaged(ctx context.Context, mgr service.Manager) bool {
	if mgr == nil {
		return false
	}
	statuses, err := mgr.Status(ctx)
	if err != nil {
		return false
	}
	for _, s := range statuses {
		if s.Name == service.RelayUnitName && s.Active && s.PID > 0 {
			return true
		}
	}
	return false
}
