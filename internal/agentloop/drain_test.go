package agentloop

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDrain_FeedsStateMachineAndTranscript(t *testing.T) {
	dir := t.TempDir()
	tdir := filepath.Join(dir, "transcripts")

	sm := NewStateMachine(time.Unix(1715500000, 0))
	src := strings.NewReader(strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","session_id":"s1"}`,
		`{"type":"result","subtype":"success","session_id":"s1"}`,
		``,
	}, "\n"))

	d := NewDrainer(DrainerConfig{
		TranscriptsDir: tdir,
		StateMachine:   sm,
		Clock:          func() time.Time { return time.Unix(1715500100, 0) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, src) }()

	select {
	case err := <-done:
		if err != nil && err != io.EOF {
			t.Fatalf("drain returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not return within 3s")
	}

	snap := sm.Snapshot()
	if snap.ClaudeBusy {
		t.Errorf("after result event SM should be idle")
	}
	if snap.LastEventType != "result" {
		t.Errorf("last event type: got %q, want result", snap.LastEventType)
	}
}
