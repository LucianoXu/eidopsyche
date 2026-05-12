package agentloop

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
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

func TestDrain_OpensAndFinalizesPerTurnTranscript(t *testing.T) {
	dir := t.TempDir()
	sm := NewStateMachine(time.Unix(1715500000, 0))

	src := strings.NewReader(strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","session_id":"s1"}`,
		`{"type":"result","subtype":"success","session_id":"s1","total_cost_usd":0.01}`,
		``,
	}, "\n"))

	store, err := transcript.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDrainer(DrainerConfig{
		TranscriptsDir: dir,
		Store:          store,
		StateMachine:   sm,
		Clock:          func() time.Time { return time.Unix(1715500100, 0) },
		NextTurnID:     func() string { return "1715500000" },
		NextTurnReason: func() string { return "heartbeat" },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Run(ctx, src); err != nil && err != io.EOF {
		t.Fatalf("drain: %v", err)
	}

	wakePath := store.WakePath("1715500000")
	body, err := os.ReadFile(wakePath)
	if err != nil {
		t.Fatalf("transcript file missing: %v", err)
	}
	if !strings.Contains(string(body), `"type":"assistant"`) {
		t.Errorf("transcript should contain assistant event, got: %s", body)
	}
	idx, err := store.ReadIndex()
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].ID != "1715500000" {
		t.Errorf("index entries: got %+v, want one 1715500000 entry", idx.Wakes)
	}
	if got := idx.Wakes[0].Reason; got != "heartbeat" {
		t.Errorf("index Reason: got %q, want %q", got, "heartbeat")
	}
	if idx.Wakes[0].CostUSD == nil || *idx.Wakes[0].CostUSD != 0.01 {
		t.Errorf("index CostUSD: got %v, want pointer to 0.01", idx.Wakes[0].CostUSD)
	}
}
