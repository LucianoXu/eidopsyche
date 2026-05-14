// Package forge contains the orchestration core shared between the
// `eidos forge create` CLI flow and the host gate daemon's
// `forge.upgrade` IPC method. CLI commands and daemon handlers both
// call into this package; the package itself does not depend on
// cobra or IPC.
package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Step is one reversible action in an orchestrate pipeline. Undo may
// be nil when a step has nothing to roll back (a read-only check, or
// an idempotent shared-infrastructure mutation like an image pull).
type Step struct {
	Name string
	Do   func(ctx context.Context) error
	Undo func(ctx context.Context) error
}

// RunSteps executes steps in order. On the first error, it runs the
// undo of every step that completed successfully (in reverse) and
// returns the original error. Rollback failures are logged to stderr
// and joined onto the returned error so the operator knows when
// orphan state may need manual cleanup; the do-failure remains the
// primary cause so existing error-string contracts at call sites are
// preserved.
func RunSteps(ctx context.Context, steps []Step) error {
	done := make([]Step, 0, len(steps))
	for _, s := range steps {
		if err := s.Do(ctx); err != nil {
			rollbackErrs := []error{err}
			for i := len(done) - 1; i >= 0; i-- {
				if done[i].Undo == nil {
					continue
				}
				if uerr := done[i].Undo(ctx); uerr != nil {
					fmt.Fprintf(os.Stderr, "orchestrate: rollback of %s failed: %v\n", done[i].Name, uerr)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("rollback %s: %w", done[i].Name, uerr))
				}
			}
			if len(rollbackErrs) == 1 {
				return err
			}
			return errors.Join(rollbackErrs...)
		}
		done = append(done, s)
	}
	return nil
}
