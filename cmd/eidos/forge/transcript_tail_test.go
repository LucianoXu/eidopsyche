package forge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func TestTranscriptTail_AbsentCurrent(t *testing.T) {
	transcriptsFixture(t)
	// No `current` symlink — `transcript-tail --wake current` exits 0
	// with no output (the renderer treats this as "nothing in flight").
	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "current"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

func TestTranscriptTail_ExplicitWakeMissing(t *testing.T) {
	transcriptsFixture(t)
	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "doesnotexist"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestTranscriptTail_ExplicitWakeReadsFile(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	f, _ := s.Open("abc")
	f.WriteString(`{"type":"system"}` + "\n" + `{"type":"result"}` + "\n")
	f.Close()

	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "abc"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"type":"system"`) || !strings.Contains(out, `"type":"result"`) {
		t.Errorf("transcript content missing: %q", out)
	}
}

func TestTranscriptTail_CurrentSymlinkResolves(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	f, _ := s.Open("live")
	f.WriteString(`{"type":"assistant"}` + "\n")
	f.Close()
	// `current` points at wake-live.ndjson now.

	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "current"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"type":"assistant"`) {
		t.Errorf("current wake content missing: %q", buf.String())
	}
}

func TestTranscriptTail_FollowEndsWhenWakeFinishes(t *testing.T) {
	// Speed up timing for the test.
	prevPoll, prevIdle := transcriptTailPollInterval, transcriptTailIdleTimeout
	transcriptTailPollInterval = 5 * time.Millisecond
	transcriptTailIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		transcriptTailPollInterval = prevPoll
		transcriptTailIdleTimeout = prevIdle
	})

	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	f, _ := s.Open("done")
	f.WriteString(`{"type":"system"}` + "\n")
	f.Close()
	// Symlink still points at wake-done.ndjson — the wake hasn't been
	// finalised yet from the tail's perspective.

	go func() {
		// After a short delay, finalise the wake (clears `current`).
		time.Sleep(20 * time.Millisecond)
		_ = s.Finalize(transcript.Entry{ID: "done", StartedAt: 1, OK: true}, 0, 0)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "current", "--follow"})
	cmd.SetContext(ctx)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 900*time.Millisecond {
		t.Errorf("follow did not exit promptly after wake finished (took %s)", elapsed)
	}
	if !strings.Contains(buf.String(), `"type":"system"`) {
		t.Errorf("initial content missing: %q", buf.String())
	}
}

func TestTranscriptTail_FollowAbsentCurrentExitsImmediately(t *testing.T) {
	prevPoll := transcriptTailPollInterval
	transcriptTailPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { transcriptTailPollInterval = prevPoll })

	transcriptsFixture(t)
	cmd := newTranscriptTailCmd()
	cmd.SetArgs([]string{"--wake", "current", "--follow"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("absent-current with --follow should exit immediately, took %s", time.Since(start))
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}
