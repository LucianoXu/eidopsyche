package state

import (
	"context"
	"sync"
)

// ApplyFunc is invoked by the daemon's Mutate helper after a successful
// write. deps is opaque (interface{}); the package that registers a
// hook defines its own concrete deps shape and casts on entry. This
// keeps internal/state independent of cron/lifecycle/daemon (avoiding
// the import cycle that comes from "state knows what hooks need").
type ApplyFunc func(ctx context.Context, deps any, old, new any) error

// Rollbacker is an optional interface on errors returned from ApplyFunc.
// If the returned error implements Rollbacker and Rollback() reports
// false, the Mutate helper SKIPS its default rollback. Default behavior
// (error doesn't implement Rollbacker, or Rollback() returns true) is
// to rollback by re-invoking the mutation's write closure with the old
// value.
type Rollbacker interface {
	Rollback() bool
}

// ApplyRegistry holds per-state-path apply hooks. Constructed via
// NewApplyRegistry and owned by a Daemon; registration is typically
// done at daemon startup by the lifecycle package (in-container) or
// not at all (host).
type ApplyRegistry struct {
	mu    sync.RWMutex
	hooks map[string]ApplyFunc
}

// NewApplyRegistry returns an empty registry.
func NewApplyRegistry() *ApplyRegistry {
	return &ApplyRegistry{hooks: map[string]ApplyFunc{}}
}

// Register installs fn as the hook for path. Duplicate registrations
// for the same path overwrite — the lifecycle attach phase is the only
// expected caller, and it runs once.
func (r *ApplyRegistry) Register(path string, fn ApplyFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[path] = fn
}

// Dispatch looks up the hook for path and runs it. A missing hook is
// not an error (most paths have no apply behavior) — Dispatch returns
// nil silently. The caller is responsible for the rollback decision
// based on the returned error.
func (r *ApplyRegistry) Dispatch(ctx context.Context, deps any, path string, old, new any) error {
	r.mu.RLock()
	fn, ok := r.hooks[path]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return fn(ctx, deps, old, new)
}

// Has reports whether a hook is registered for path. Useful in tests.
func (r *ApplyRegistry) Has(path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.hooks[path]
	return ok
}
