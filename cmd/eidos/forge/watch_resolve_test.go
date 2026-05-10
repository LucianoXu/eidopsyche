package forge

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// fakeTranscriptList responds to `transcript-list --json` calls; used
// by the resolveWakeID / latestWakeID tests below.
type fakeTranscriptList struct {
	fakeClient
	indexJSON string
	exitCode  int
}

func (f *fakeTranscriptList) ContainerExec(_ context.Context, _ string, cmd []string) (forgectl.ExecResult, error) {
	if len(cmd) >= 3 && cmd[2] == "transcript-list" {
		return forgectl.ExecResult{ExitCode: f.exitCode, Stdout: []byte(f.indexJSON)}, nil
	}
	return forgectl.ExecResult{ExitCode: 1}, nil
}

func TestResolveWakeID_ExactMatch(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[{"id":"abcdef1234"},{"id":"9876fedcba"}]}`}
	got, err := resolveWakeID(context.Background(), f, "cont", "abcdef1234")
	if err != nil || got != "abcdef1234" {
		t.Errorf("exact match: got=%q err=%v", got, err)
	}
}

func TestResolveWakeID_PrefixMatch(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[{"id":"abcdef1234"},{"id":"9876fedcba"}]}`}
	got, err := resolveWakeID(context.Background(), f, "cont", "abcdef")
	if err != nil || got != "abcdef1234" {
		t.Errorf("prefix expansion: got=%q err=%v", got, err)
	}
}

func TestResolveWakeID_AmbiguousPrefix(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[{"id":"abcd1"},{"id":"abcd2"}]}`}
	_, err := resolveWakeID(context.Background(), f, "cont", "abcd")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %v", err)
	}
}

func TestResolveWakeID_NoMatchPassesThrough(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[{"id":"abc"}]}`}
	got, err := resolveWakeID(context.Background(), f, "cont", "doesnotexist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "doesnotexist" {
		t.Errorf("no-match should pass through verbatim, got %q", got)
	}
}

func TestResolveWakeID_IndexUnavailableFallsThrough(t *testing.T) {
	// transcript-list exits non-zero (no transcripts dir yet).
	f := &fakeTranscriptList{exitCode: 1}
	got, _ := resolveWakeID(context.Background(), f, "cont", "anything")
	if got != "anything" {
		t.Errorf("when index unavailable, should pass input through, got %q", got)
	}
}

func TestLatestWakeID_NonEmpty(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[{"id":"newest","started_at":2},{"id":"older","started_at":1}]}`}
	got := latestWakeID(context.Background(), f, "cont")
	if got != "newest" {
		t.Errorf("latestWakeID = %q, want newest", got)
	}
}

func TestLatestWakeID_Empty(t *testing.T) {
	f := &fakeTranscriptList{indexJSON: `{"v":1,"wakes":[]}`}
	if got := latestWakeID(context.Background(), f, "cont"); got != "" {
		t.Errorf("empty index should yield \"\", got %q", got)
	}
}

func TestLatestWakeID_Unavailable(t *testing.T) {
	f := &fakeTranscriptList{exitCode: 1}
	if got := latestWakeID(context.Background(), f, "cont"); got != "" {
		t.Errorf("unavailable index should yield \"\", got %q", got)
	}
}
