package wake

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSignalJSONRoundTrip(t *testing.T) {
	in := Signal{
		V:           1,
		ID:          "20260509T103045Z-mindgate-7f2e",
		Reason:      ReasonMindGate,
		TriggeredAt: 1715251845,
		Context: Context{
			InboxUnread:          3,
			FirstUnreadFromNpub:  "npub1alice",
			FirstUnreadSummary:   "hello",
			SinceLastWakeSeconds: 14400,
			LastWakeReason:       ReasonHeartBeat,
			Scheduled:            false,
		},
		CoalescedCount: 0,
		CoalescedFrom:  []Reason{},
		Hint:           "Alice sent a message",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Signal
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Reason != in.Reason || out.Context.InboxUnread != 3 {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestReasonValues(t *testing.T) {
	if string(ReasonMindGate) != "mindgate" || string(ReasonHeartBeat) != "heartbeat" || string(ReasonManual) != "manual" {
		t.Errorf("reason constants drifted")
	}
}

func TestWritePendingCreatesAndReads(t *testing.T) {
	dir := t.TempDir()
	sig := Signal{V: 1, ID: "abc", Reason: ReasonHeartBeat, TriggeredAt: 100}
	if err := WritePending(dir, sig); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "abc" {
		t.Fatalf("read mismatch: %+v", got)
	}
}

func TestReadPendingMissingReturnsNil(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestWritePendingIsAtomic(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		sig := Signal{V: 1, ID: "x", Reason: ReasonManual, TriggeredAt: int64(i)}
		if err := WritePending(dir, sig); err != nil {
			t.Fatal(err)
		}
	}
	// no .tmp leftovers
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("found .tmp leftover: %s", e.Name())
		}
	}
}
