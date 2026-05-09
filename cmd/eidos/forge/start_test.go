package forge

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

type startStopFake struct {
	fakeClient
	state     string
	starts    []string
	stops     []string
	stopGrace int
}

func (f *startStopFake) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return f.state, nil
}
func (f *startStopFake) ContainerExists(_ context.Context, _ string) (bool, error) {
	return f.state != "absent", nil
}
func (f *startStopFake) ContainerStart(_ context.Context, name string) error {
	f.starts = append(f.starts, name)
	f.state = "running"
	return nil
}
func (f *startStopFake) ContainerStop(_ context.Context, name string, grace int) error {
	f.stops = append(f.stops, name)
	f.stopGrace = grace
	f.state = "exited"
	return nil
}

func TestStartHappyPath(t *testing.T) {
	f := &startStopFake{state: "exited"}
	err := startMindform(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.starts) != 1 || f.starts[0] != "eidos-mindform-alice" {
		t.Errorf("starts = %v", f.starts)
	}
}

func TestStartRefusesIfAbsent(t *testing.T) {
	f := &startStopFake{state: "absent"}
	err := startMindform(context.Background(), f, "alice")
	// Post-fix: forge create now provisions the persistent container in
	// stopped state, so reaching "absent" at start time means the
	// container was removed out of band. Error should still surface
	// clearly.
	if err == nil || !strings.Contains(err.Error(), "no container") {
		t.Errorf("want no-container error, got %v", err)
	}
}

func TestStartIdempotentIfRunning(t *testing.T) {
	f := &startStopFake{state: "running"}
	err := startMindform(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.starts) != 0 {
		t.Errorf("running mind-form should not be started again: %v", f.starts)
	}
}

func TestStopHappyPath(t *testing.T) {
	f := &startStopFake{state: "running"}
	err := stopMindform(context.Background(), f, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.stops) != 1 || f.stopGrace != 10 {
		t.Errorf("stops = %v grace = %d", f.stops, f.stopGrace)
	}
}

func TestStopIdempotentIfExited(t *testing.T) {
	f := &startStopFake{state: "exited"}
	err := stopMindform(context.Background(), f, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.stops) != 0 {
		t.Errorf("exited mind-form should not be stopped: %v", f.stops)
	}
}

var _ forgectl.Client = (*startStopFake)(nil)
