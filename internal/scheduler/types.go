// Package scheduler owns the on-disk plan-file format used by the
// mind-form's supervisor to fire future wakes.
//
// Plans live under /eidos/run/plans/<id>.json. When the supervisor's
// scheduler goroutine fires a plan it submits a wake.Signal with
// Reason=planned and renames the file to plans/fired/<id>.json for
// audit. Pure filesystem operations live here; the goroutine itself
// lives in cmd/eidos/supervisor/scheduler.go.
package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// SchemaVersion is bumped when the on-disk format changes.
const SchemaVersion = 1

// Plan is the on-disk plan record.
type Plan struct {
	V         int    `json:"v"`
	ID        string `json:"id"`
	At        int64  `json:"at"`
	Hint      string `json:"hint"`
	CreatedAt int64  `json:"created_at"`
}

// newIDAt builds a plan ID with the project-wide
// "<UTC-timestamp>-<reason-slug>-<hex>" shape used by wake.Signal.ID.
// Format: 20260509T123000Z-plan-7f2eab19
//
// 8 hex chars (4 random bytes) keeps collision probability negligible
// even when many plans are added within the same second.
func newIDAt(now time.Time) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("%s-plan-%s",
		now.UTC().Format("20060102T150405Z"),
		hex.EncodeToString(rnd[:]),
	)
}
