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

func TestPollResponseFile_AppearsBeforeTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "response.md")
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(path, []byte("hello world"), 0o600)
	}()
	body, err := firstcontact.PollResponseFile(context.Background(), path, 500*time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("PollResponseFile: %v", err)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q", body)
	}
}

func TestPollResponseFile_TimeoutErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "response.md")
	_, err := firstcontact.PollResponseFile(context.Background(), path, 50*time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Errorf("expected timeout error")
	}
}
