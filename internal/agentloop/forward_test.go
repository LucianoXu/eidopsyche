//go:build !windows

package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
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
		IdentityPrompt:           "I am test-mindform.",
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
		IdentityPrompt:           "I am test-mindform.",
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
