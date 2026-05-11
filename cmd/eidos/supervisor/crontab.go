//go:build !windows

package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// crontabPath is the path the supervisor writes the eidos user's
// crontab to before spawning crond. Matches the busybox crond `-c`
// argument in startChildren.
const crontabPath = "/var/spool/cron/crontabs/eidos"

// crontabHeartbeatLine is the per-mind-form heartbeat entry. The
// EIDOS_IN_CONTAINER=1 prefix is required because the supervisor
// spawns crond via `sudo -n` which strips environment variables —
// including the Dockerfile's ENV EIDOS_IN_CONTAINER=1. Without it,
// `eidos forge wake --reason heartbeat` resolves to the host-side
// variant (which expects a mind-form name argument) instead of the
// in-container variant, exits 1, and busybox crond swallows the
// error. The fail-loud test for this is the 003 deploy test.
const crontabHeartbeatLine = "%s EIDOS_IN_CONTAINER=1 /usr/local/bin/eidos forge wake --reason heartbeat\n"

// renderCrontab returns the file body for /var/spool/cron/crontabs/eidos.
// On a malformed [heartbeat] interval the function returns the default
// 2h crontab body AND a non-nil error; the caller decides whether to
// install the fallback. (Heartbeat is rhythm, not authority — losing it
// would leave the mind-form completely silent.) The default mirrors
// config.DefaultHeartbeatInterval.
func renderCrontab(cfg config.Config) (string, error) {
	defaultBody := fmt.Sprintf(crontabHeartbeatLine, "0 */2 * * *")
	interval := cfg.Heartbeat.Interval
	if interval == "" {
		return defaultBody, nil
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return defaultBody, fmt.Errorf("parse [heartbeat] interval %q: %w", interval, err)
	}
	cronExpr, err := config.HeartbeatCronExpression(d)
	if err != nil {
		return defaultBody, err
	}
	return fmt.Sprintf(crontabHeartbeatLine, cronExpr), nil
}

// installCrontab writes body to path, creating the parent directory.
// File mode 0o600 matches busybox crond's expected permissions for a
// user crontab (per the comment in run.go's startChildren).
func installCrontab(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir crontabs dir: %w", err)
	}
	return os.WriteFile(path, []byte(body), 0o600)
}
