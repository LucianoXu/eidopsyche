//go:build !linux && !darwin

package service

import "fmt"

func platformNew(cfg Config) (Manager, error) {
	return nil, fmt.Errorf("eidos gate start/stop/status/purge needs systemd (Linux) or launchd (macOS); this OS is unsupported: %w", ErrUnsupported)
}
