package firstcontact_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
)

func TestRenderSummoningBook_ZH(t *testing.T) {
	s := &firstcontact.Summoning{
		Lang:         "zh",
		MasterLabel:  "alice",
		MasterNpub:   "npub1op",
		Displaying:   "薄雾里有一道身影。",
		SummonedName: "雨",
		MindFormNpub: "npub1mf",
		StartedAt:    time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
	}
	got := firstcontact.RenderSummoningBook(s)
	for _, want := range []string{"alice", "npub1op", "npub1mf", "雨", "薄雾里有一道身影。", "2026-05-09"} {
		if !strings.Contains(got, want) {
			t.Errorf("zh book missing %q:\n%s", want, got)
		}
	}
}

func TestRenderSummoningBook_EN(t *testing.T) {
	s := &firstcontact.Summoning{
		Lang:         "en",
		MasterLabel:  "alice",
		MasterNpub:   "npub1op",
		Displaying:   "A figure stands in the mist.",
		SummonedName: "Rain",
		MindFormNpub: "npub1mf",
		StartedAt:    time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
	}
	got := firstcontact.RenderSummoningBook(s)
	for _, want := range []string{"alice", "Rain", "npub1mf", "A figure stands in the mist."} {
		if !strings.Contains(got, want) {
			t.Errorf("en book missing %q:\n%s", want, got)
		}
	}
}

// TestPollResponseFile_GateThenBody pins the F4 fix: the waiter must
// return the body only AFTER the gate (born_at) is non-empty. Even if
// body is written first, the waiter blocks until gate appears.
func TestPollResponseFile_GateThenBody(t *testing.T) {
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "born_at")
	bodyPath := filepath.Join(dir, "response.md")
	// Write body first; waiter must NOT return until gate also appears.
	if err := os.WriteFile(bodyPath, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(gatePath, []byte("1700000000\n"), 0o600)
	}()
	start := time.Now()
	body, err := firstcontact.PollResponseFile(context.Background(), gatePath, bodyPath, 500*time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("PollResponseFile: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("waiter returned in %v — should have blocked on gate (≥50ms)", elapsed)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q", body)
	}
}

// TestPollResponseFile_GateMissingTimesOut: gate never appears → timeout.
// (Body presence alone is insufficient.)
func TestPollResponseFile_GateMissingTimesOut(t *testing.T) {
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "born_at")
	bodyPath := filepath.Join(dir, "response.md")
	if err := os.WriteFile(bodyPath, []byte("orphan response"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := firstcontact.PollResponseFile(context.Background(), gatePath, bodyPath, 50*time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Errorf("expected timeout when gate is absent")
	}
}

// TestPollResponseFile_GatePresentBodyMissing: gate present but body
// absent → error (agent broke the documented order). Pin so a regression
// can't silently truncate the wizard's response.
func TestPollResponseFile_GatePresentBodyMissing(t *testing.T) {
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "born_at")
	bodyPath := filepath.Join(dir, "response.md")
	if err := os.WriteFile(gatePath, []byte("1700000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := firstcontact.PollResponseFile(context.Background(), gatePath, bodyPath, 100*time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Errorf("expected error when gate is set but body is missing")
	}
}
