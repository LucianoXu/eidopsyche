// Package cron renders the busybox crond crontab body that fires a
// mindform's heartbeat wake, and writes it to the in-container spool.
// The supervisor's initial PID-1 render and the heartbeat.interval
// apply hook both call into here so the rendering logic has one home.
package cron

import (
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// CrontabPath is the per-user spool file busybox crond reads. Must be
// root-owned 0600 — see install.go for the sudo path.
const CrontabPath = "/var/spool/cron/crontabs/eidos"

// HeartbeatLineFormat carries the cron expression plus the
// EIDOS_IN_CONTAINER=1 inline env. The env prefix is required because
// the supervisor spawns crond via `sudo -n` which strips environment
// variables — including the Dockerfile's ENV EIDOS_IN_CONTAINER=1.
// Without it, `eidos forge wake --reason heartbeat` resolves to the
// host-side variant (which expects a mind-form name argument) instead
// of the in-container variant, exits 1, and busybox crond swallows the
// error.
const HeartbeatLineFormat = "%s EIDOS_IN_CONTAINER=1 /usr/local/bin/eidos forge wake --reason heartbeat\n"

// Render returns the crontab body for an interval string. Empty
// interval renders config.DefaultHeartbeatInterval (currently 2h).
// Invalid intervals return an error without a body fallback — the
// caller decides whether to retry with "" for the safe default.
func Render(interval string) (string, error) {
	interval = strings.TrimSpace(interval)
	if interval == "" {
		expr, err := config.HeartbeatCronExpression(config.DefaultHeartbeatInterval)
		if err != nil {
			return "", fmt.Errorf("render default: %w", err)
		}
		return fmt.Sprintf(HeartbeatLineFormat, expr), nil
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return "", fmt.Errorf("parse interval %q: %w", interval, err)
	}
	expr, err := config.HeartbeatCronExpression(d)
	if err != nil {
		return "", fmt.Errorf("render interval %q: %w", interval, err)
	}
	return fmt.Sprintf(HeartbeatLineFormat, expr), nil
}

// RenderOrDefault returns the body for interval; on any error logs
// nothing (caller decides) and returns the default 2h body instead.
// Suited for the supervisor's startup path where a malformed config
// must not leave the mindform completely silent.
func RenderOrDefault(interval string) (body string, rendered string, err error) {
	body, err = Render(interval)
	if err == nil {
		return body, interval, nil
	}
	fallback, _ := Render("")
	return fallback, "", err
}
