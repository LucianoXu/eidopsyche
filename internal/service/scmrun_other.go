//go:build !windows

package service

import "context"

// RunSupervised is a no-op pass-through on platforms that don't run the
// daemon under a service supervisor (linux + darwin run via systemd /
// launchd, both of which deliver SIGTERM through `signal.NotifyContext`
// rather than an out-of-band control channel). The Windows variant in
// scmrun_windows.go handles SCM dispatch.
func RunSupervised(ctx context.Context, name string, runner func(context.Context) error) error {
	return runner(ctx)
}
