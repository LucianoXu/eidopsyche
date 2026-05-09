package wake

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestMergeIntoNilProducesIdentity(t *testing.T) {
	in := Signal{V: 1, ID: "a", Reason: ReasonHeartBeat, TriggeredAt: 100}
	got := Merge(nil, in)
	if got.ID != "a" || got.CoalescedCount != 0 || len(got.CoalescedFrom) != 0 {
		t.Errorf("merge into nil: %+v", got)
	}
}

func TestMergeAppendsAndIncrements(t *testing.T) {
	prev := Signal{V: 1, ID: "a", Reason: ReasonHeartBeat, TriggeredAt: 100, CoalescedCount: 0}
	next := Signal{V: 1, ID: "b", Reason: ReasonMindGate, TriggeredAt: 200, Hint: "alice"}
	got := Merge(&prev, next)
	if got.ID != "b" || got.Reason != ReasonMindGate || got.TriggeredAt != 200 || got.Hint != "alice" {
		t.Errorf("merge did not adopt next: %+v", got)
	}
	if got.CoalescedCount != 1 {
		t.Errorf("count = %d, want 1", got.CoalescedCount)
	}
	if len(got.CoalescedFrom) != 1 || got.CoalescedFrom[0] != ReasonHeartBeat {
		t.Errorf("coalesced_from = %v, want [heartbeat]", got.CoalescedFrom)
	}
}

func TestMergeChainsAcrossManyArrivals(t *testing.T) {
	cur := (*Signal)(nil)
	reasons := []Reason{ReasonHeartBeat, ReasonMindGate, ReasonMindGate, ReasonManual}
	for i, r := range reasons {
		next := Signal{V: 1, ID: r.String(), Reason: r, TriggeredAt: int64(i)}
		merged := Merge(cur, next)
		cur = &merged
	}
	if cur.CoalescedCount != 3 {
		t.Errorf("count after 4 wakes = %d, want 3", cur.CoalescedCount)
	}
	if len(cur.CoalescedFrom) != 3 {
		t.Errorf("coalesced_from len = %d, want 3", len(cur.CoalescedFrom))
	}
}

func TestSubmitUnderConcurrentProducers(t *testing.T) {
	dir := t.TempDir()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			sig := Signal{V: 1, ID: "x", Reason: ReasonMindGate, TriggeredAt: int64(i)}
			if err := Submit(dir, sig); err != nil {
				t.Errorf("submit %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := ReadPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nil pending after producers")
	}
	if got.CoalescedCount != n-1 {
		t.Errorf("coalesced_count = %d, want %d", got.CoalescedCount, n-1)
	}
	if len(got.CoalescedFrom) != n-1 {
		t.Errorf("coalesced_from len = %d, want %d", len(got.CoalescedFrom), n-1)
	}
}

// TestPromoteToActiveRaceFree verifies that concurrent Submit and PromoteToActive
// calls do not trigger the race detector. 200 iterations of mixed producers and
// promoters must all complete without error.
func TestPromoteToActiveRaceFree(t *testing.T) {
	dir := t.TempDir()
	const iterations = 200

	// Seed an initial pending signal so PromoteToActive has something to work with.
	seed := Signal{V: 1, ID: "seed-0", Reason: ReasonHeartBeat, TriggeredAt: 0}
	if err := Submit(dir, seed); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	// Half goroutines submit, half promote.
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		if i%2 == 0 {
			go func(i int) {
				defer wg.Done()
				sig := Signal{
					V:           1,
					ID:          fmt.Sprintf("submit-%d", i),
					Reason:      ReasonMindGate,
					TriggeredAt: int64(i),
				}
				if err := Submit(dir, sig); err != nil {
					t.Errorf("submit %d: %v", i, err)
				}
			}(i)
		} else {
			go func(i int) {
				defer wg.Done()
				promoted, err := PromoteToActive(dir)
				if err != nil {
					t.Errorf("promote %d: %v", i, err)
					return
				}
				if promoted == nil {
					// No pending at this moment — that's fine.
					return
				}
				// Verify the returned signal matches what's now in active.json.
				// Under the flock, PromoteToActive is the sole writer of active.json,
				// so the file we read immediately after must contain the same ID.
				active, err := ReadActive(dir)
				if err != nil {
					t.Errorf("read active after promote %d: %v", i, err)
					return
				}
				if active == nil {
					t.Errorf("active.json missing right after promote %d", i)
					return
				}
				if active.ID != promoted.ID {
					t.Errorf("promote %d: returned ID %q but active.json has ID %q",
						i, promoted.ID, active.ID)
				}
			}(i)
		}
	}
	wg.Wait()
}

// TestWritePendingDirFsync exercises the dir-fsync path introduced to make
// renames durable. The test simply verifies that WritePending succeeds and the
// file is readable; any panic or error in fsyncDir is caught here.
func TestWritePendingDirFsync(t *testing.T) {
	dir := t.TempDir()
	sig := Signal{V: 1, ID: "fsync-test", Reason: ReasonManual, TriggeredAt: 42}
	if err := WritePending(dir, sig); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	got, err := ReadPending(dir)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if got == nil || got.ID != "fsync-test" {
		t.Fatalf("unexpected read result: %+v", got)
	}
}
