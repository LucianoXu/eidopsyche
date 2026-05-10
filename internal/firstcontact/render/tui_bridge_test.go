package render

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTUI_BridgeRoundTrip verifies that a phase-style worker can call
// Show / Logo through tuiRenderer.RunWithPhases, that the asks reach
// the Bubble Tea Model, and that the worker returns when done.
//
// It deliberately exercises only fire-and-act ops (Show, Logo) — those
// don't require simulated keystrokes, which makes the test
// deterministic. Prompt / PromptChoice are exercised by direct Update
// tests in tui_model_test.go (the bridge plumbing is identical).
func TestTUI_BridgeRoundTrip(t *testing.T) {
	pr, _ := newPipePair()
	out := &captureWriter{}
	r := NewTUI(pr, out, 0).(*tuiRenderer)

	var called []string
	worker := func(_ context.Context, ren Renderer) error {
		ren.Show("first")
		called = append(called, "show:first")
		ren.Logo(context.Background(), 100*time.Millisecond)
		called = append(called, "logo")
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- r.RunWithPhases(context.Background(), worker)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithPhases: %v", err)
		}
	case <-time.After(3 * time.Second):
		// If the TUI hangs, force quit.
		r.prog.Quit()
		t.Fatalf("worker did not complete within 3s; got: %v", called)
	}

	want := []string{"show:first", "logo"}
	if !reflect.DeepEqual(called, want) {
		t.Errorf("worker calls = %v, want %v", called, want)
	}
}

// TestTUI_BridgeWorkerPanic_Recovers verifies that a worker panic is
// caught and converted to an error via RunWithPhases.
func TestTUI_BridgeWorkerPanic_Recovers(t *testing.T) {
	pr, _ := newPipePair()
	out := &captureWriter{}
	r := NewTUI(pr, out, 0).(*tuiRenderer)

	worker := func(_ context.Context, _ Renderer) error {
		panic("boom")
	}

	done := make(chan error, 1)
	go func() {
		done <- r.RunWithPhases(context.Background(), worker)
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "panic") {
			t.Errorf("expected panic-derived error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		r.prog.Quit()
		t.Fatalf("RunWithPhases did not return after worker panic")
	}
}

// captureWriter is a thread-safe io.Writer for tests.
type captureWriter struct{ buf []byte }

func (w *captureWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// newPipePair returns the two ends of an io.Pipe; tests inject the
// reader as Bubble Tea's input. Bubble Tea normally uses the pipe to
// read keystrokes; for fire-and-act tests no keystrokes are needed,
// so the pipe stays open until cleanup.
func newPipePair() (reader interface {
	Read([]byte) (int, error)
}, writeFn func([]byte)) {
	// Use a channel-backed reader so Read blocks until data arrives.
	pr := &chanReader{ch: make(chan []byte, 16)}
	return pr, func(b []byte) {
		pr.ch <- b
	}
}

type chanReader struct {
	ch  chan []byte
	rem []byte
}

func (r *chanReader) Read(p []byte) (int, error) {
	if len(r.rem) == 0 {
		b, ok := <-r.ch
		if !ok {
			return 0, fmt.Errorf("EOF")
		}
		r.rem = b
	}
	n := copy(p, r.rem)
	r.rem = r.rem[n:]
	return n, nil
}

// Compile-time assurance that captureWriter satisfies io.Writer.
var _ = (&captureWriter{}).Write

// Avoid "imported and not used" if tea package imports drift.
var _ tea.Msg = nil
