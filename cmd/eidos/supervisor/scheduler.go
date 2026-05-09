//go:build !windows

package supervisor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// PlanScanInterval is how often the supervisor scans /eidos/run/plans for
// due files. Visible to tests; production runs at 30s.
const PlanScanInterval = 30 * time.Second

// supervisorPlansDir is the in-container path the supervisor watches
// for plan files.
const supervisorPlansDir = "/eidos/run/plans"

// submitFunc abstracts wake.Submit so tests can capture fires.
type submitFunc func(dir string, sig wake.Signal) error

// plannerLoop runs until ctx is cancelled, scanning dir every interval.
// Each tick fires due plans via submit and renames them into dir/fired/.
func plannerLoop(ctx context.Context, dir string, interval time.Duration, submit submitFunc) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := plannerTick(dir, time.Now(), submit); err != nil {
				log.Printf("planner tick: %v", err)
			}
		}
	}
}

// plannerTick is one pass of the loop; tests call it directly with a
// fixed clock.
func plannerTick(dir string, now time.Time, submit submitFunc) error {
	due, err := scheduler.ScanDue(dir, now)
	if err != nil {
		return fmt.Errorf("scan due: %w", err)
	}
	for _, p := range due {
		sig := wake.Signal{
			V:           wake.SchemaVersion,
			ID:          fmt.Sprintf("%d-%s", now.Unix(), wake.ReasonPlanned),
			Reason:      wake.ReasonPlanned,
			TriggeredAt: now.Unix(),
			Hint:        p.Hint,
			Context: wake.Context{
				PlanID: p.ID,
			},
		}
		// Submit before MarkFired: if Submit fails we want the plan
		// to remain active so the next tick retries. wake.Submit is
		// idempotent under coalescing, so a re-fire is harmless.
		if err := submit(wakeDir, sig); err != nil {
			log.Printf("planner submit %s: %v", p.ID, err)
			continue
		}
		if err := scheduler.MarkFired(dir, p.ID); err != nil {
			log.Printf("planner mark-fired %s: %v", p.ID, err)
			// fall through; next tick will see the still-active file
			// and re-submit. Coalescing absorbs the duplicate.
		}
		log.Printf("planner: fired plan %s — %s", p.ID, p.Hint)
	}
	return nil
}
