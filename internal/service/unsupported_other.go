//go:build !linux && !darwin && !windows

package service

import (
	"context"
	"fmt"
)

func platformNew(cfg Config) (Manager, error) {
	return nil, fmt.Errorf("service management is not supported on this platform; run `eidos gate daemon` and `eidos relay start` directly: %w", ErrUnsupported)
}

// unsupported is a no-op Manager returned when platformNew is not available.
// It is never actually returned to callers (platformNew returns ErrUnsupported
// instead), but it satisfies the Manager interface so the package compiles on
// unsupported platforms.
type unsupported struct{}

func (unsupported) InstallDaemon(_ context.Context) error          { return ErrUnsupported }
func (unsupported) InstallRelay(_ context.Context, _ string) error { return ErrUnsupported }
func (unsupported) UninstallDaemon(_ context.Context) error        { return ErrUnsupported }
func (unsupported) UninstallRelay(_ context.Context) error         { return ErrUnsupported }
func (unsupported) StartDaemon(_ context.Context) error            { return ErrUnsupported }
func (unsupported) StartRelay(_ context.Context, _ string) error   { return ErrUnsupported }
func (unsupported) StopDaemon(_ context.Context) error             { return ErrUnsupported }
func (unsupported) StopRelay(_ context.Context) error              { return ErrUnsupported }
func (unsupported) Status(_ context.Context) ([]Status, error)     { return nil, ErrUnsupported }
