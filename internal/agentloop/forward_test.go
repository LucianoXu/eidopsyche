//go:build !windows

package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestForward_RendersWakeAsStreamJSONUserMessage(t *testing.T) {
	sig := wake.Signal{
		V:           wake.SchemaVersion,
		ID:          "wake-test-1",
		Reason:      wake.ReasonHeartBeat,
		TriggeredAt: 1715500000,
	}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		DreamState:               dreamstate.State{},
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.Run(ctx, in); err != nil {
		// EOF after one line is acceptable.
	}

	if outstanding.Load() != 1 {
		t.Errorf("outstanding wakes: got %d, want 1", outstanding.Load())
	}
	if !strings.Contains(out.String(), `"type":"user"`) {
		t.Errorf("expected stream-json user message in output, got: %s", out.String())
	}
	if !strings.Contains(out.String(), `heartbeat`) {
		t.Errorf("expected heartbeat reason in rendered text, got: %s", out.String())
	}
}

func TestForward_BufferedWhenDreaming(t *testing.T) {
	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-dream-1", Reason: wake.ReasonMindGate}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	captured := []wake.Signal{}
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return true },
		AppendToBacklog:          func(s wake.Signal) { captured = append(captured, s) },
		FirstWakeFlagGetAndClear: func() bool { return false },
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = f.Run(ctx, in)

	if outstanding.Load() != 0 {
		t.Errorf("outstanding wakes during dream: got %d, want 0", outstanding.Load())
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be written to claude stdin during dream, got %d bytes", out.Len())
	}
	if len(captured) != 1 || captured[0].ID != "wake-dream-1" {
		t.Errorf("backlog: got %+v, want [wake-dream-1]", captured)
	}
}

func TestForward_FirstWordsPending_AddsPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Phase 1 done: born_at present, first_words_at absent.
	if err := os.WriteFile(filepath.Join(dir, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-1", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	for _, want := range []string{"chest/first-message.md", "eidos gate send", "Alice"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected first-words prefix marker %q in output; got:\n%s", want, out.String())
		}
	}
}

func TestForward_FirstWordsSent_NoPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Both markers present: Phase 2 already complete.
	if err := os.WriteFile(filepath.Join(dir, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "self", "first_words_at"), []byte("1715600100\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-2", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	if strings.Contains(out.String(), "chest/first-message.md") {
		t.Errorf("first-words prefix must NOT appear when first_words_at exists; got:\n%s", out.String())
	}
}

func TestForward_NoBornAt_NoPrefix(t *testing.T) {
	dir := t.TempDir()

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-fw-3", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	if strings.Contains(out.String(), "chest/first-message.md") {
		t.Errorf("first-words prefix must NOT appear when born_at is absent; got:\n%s", out.String())
	}
}

func TestForward_FirstWordsStatError_FailClosed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod 0 has no effect as root")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make self/ unreadable so stat(first_words_at) returns a non-ErrNotExist error.
	selfDir := filepath.Join(dir, "self")
	if err := os.Chmod(selfDir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(selfDir, 0o700) //nolint:errcheck

	var logBuf bytes.Buffer
	origStderr := stderrW
	stderrW = &logBuf
	defer func() { stderrW = origStderr }()

	sig := wake.Signal{V: wake.SchemaVersion, ID: "wake-err-1", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	in := strings.NewReader(string(body) + "\n")
	out := &bytes.Buffer{}

	var outstanding atomic.Int64
	f := NewForwarder(ForwarderConfig{
		ClaudeStdin:              out,
		OutstandingWakes:         &outstanding,
		Config:                   config.Defaults(),
		IsDreaming:               func() bool { return false },
		AppendToBacklog:          func(s wake.Signal) {},
		FirstWakeFlagGetAndClear: func() bool { return false },
		OntologyDir:              dir,
		OwnerLabel:               "Alice",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = f.Run(ctx, in)

	if strings.Contains(out.String(), "chest/first-message.md") {
		t.Errorf("first-words prefix must NOT appear on stat error (fail-closed); got:\n%s", out.String())
	}
	if !strings.Contains(logBuf.String(), "stat") {
		t.Errorf("expected stat error log; got: %s", logBuf.String())
	}
}
