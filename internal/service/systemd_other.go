//go:build !linux

package service

import "fmt"

func platformNew(cfg Config) (Manager, error) {
	return nil, fmt.Errorf("eidos gate start/stop/status/purge needs systemd, which only ships on Linux: %w", ErrUnsupported)
}
