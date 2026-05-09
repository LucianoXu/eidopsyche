//go:build !linux && !darwin && !windows

package service

import "fmt"

func platformNew(cfg Config) (Manager, error) {
	return nil, fmt.Errorf("eidos gate start/stop/status/purge needs systemd (Linux), launchd (macOS), or SCM (Windows); this OS is unsupported: %w", ErrUnsupported)
}
