//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestPlannerFiresDuePlans(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	pastPlan := scheduler.Plan{
		V:         scheduler.SchemaVersion,
		ID:        "20260509T000000Z-plan-aaaaaaaa",
		At:        now.Add(-time.Minute).Unix(),
		Hint:      "due",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := scheduler.WritePlanForTest(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Add(dir, now, "future", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var fired []wake.Signal
	var mu sync.Mutex
	submit := func(_ string, sig wake.Signal) error {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, sig)
		return nil
	}

	if err := plannerTick(dir, now, submit); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Fatalf("len(fired) = %d, want 1; fired=%v", len(fired), fired)
	}
	if fired[0].Reason != wake.ReasonPlanned {
		t.Errorf("Reason = %q", fired[0].Reason)
	}
	if fired[0].Hint != "due" {
		t.Errorf("Hint = %q", fired[0].Hint)
	}
	if fired[0].Context.PlanID != pastPlan.ID {
		t.Errorf("PlanID = %q", fired[0].Context.PlanID)
	}
}

func TestPlannerMovesPlanToFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	pastPlan := scheduler.Plan{
		V:         scheduler.SchemaVersion,
		ID:        "20260509T000000Z-plan-bbbbbbbb",
		At:        now.Add(-time.Minute).Unix(),
		Hint:      "x",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := scheduler.WritePlanForTest(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	noopSubmit := func(_ string, _ wake.Signal) error { return nil }
	if err := plannerTick(dir, now, noopSubmit); err != nil {
		t.Fatal(err)
	}
	plans, _ := scheduler.List(dir)
	if len(plans) != 0 {
		t.Errorf("active plans after tick: %d", len(plans))
	}
	firedDir := filepath.Join(dir, "fired")
	entries, err := os.ReadDir(firedDir)
	if err != nil {
		t.Fatalf("read fired dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("fired entries: %d", len(entries))
	}
}

func TestPlannerNoDuePlans(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if _, err := scheduler.Add(dir, now, "future", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	called := false
	submit := func(_ string, _ wake.Signal) error {
		called = true
		return nil
	}
	if err := plannerTick(dir, now, submit); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("submit called despite no due plans")
	}
}

func TestPlannerSubmitFailureLeavesPlanActive(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	pastPlan := scheduler.Plan{
		V:         scheduler.SchemaVersion,
		ID:        "20260509T000000Z-plan-cccccccc",
		At:        now.Add(-time.Minute).Unix(),
		Hint:      "x",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := scheduler.WritePlanForTest(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	failingSubmit := func(_ string, _ wake.Signal) error {
		return errors.New("submit failed")
	}
	// plannerTick logs but does not return submit failures, so it returns nil.
	if err := plannerTick(dir, now, failingSubmit); err != nil {
		t.Fatal(err)
	}
	plans, _ := scheduler.List(dir)
	if len(plans) != 1 {
		t.Errorf("expected plan to remain active after submit failure, got %d", len(plans))
	}
}

func TestPlannerLoopRespectsContext(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	noopSubmit := func(_ string, _ wake.Signal) error { return nil }

	done := make(chan error, 1)
	go func() { done <- plannerLoop(ctx, dir, time.Millisecond, noopSubmit) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("plannerLoop returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Error("plannerLoop did not exit on cancel")
	}
}
