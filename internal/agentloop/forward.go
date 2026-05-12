package agentloop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// ForwarderConfig wires the wake-stdin → claude-stdin pump.
type ForwarderConfig struct {
	// ClaudeStdin is the writer that goes to claude's stdin.
	ClaudeStdin io.Writer
	// OutstandingWakes is incremented for each rendered+written wake;
	// the drainer decrements it via ResultsSeen on each `result` event.
	OutstandingWakes *atomic.Int64
	// Config is the loaded mind-form config (for QuietStart / TZ etc.).
	Config config.Config
	// DreamState captured at startup (LastDreamFinishedAt feeds the
	// "since last dream" hint). Refreshed by the rotation goroutine.
	DreamState dreamstate.State
	// IdentityPrompt is the contents of self/identity.md. Not used in
	// wake rendering today; held here so future BuildWake variants can
	// reference it without re-loading from disk.
	IdentityPrompt string
	// IsDreaming returns the dreaming flag's current value.
	IsDreaming func() bool
	// AppendToBacklog buffers a wake during dreaming; rotation drains.
	AppendToBacklog func(s wake.Signal)
	// FirstWakeFlagGetAndClear returns (and clears) whether the next
	// wake should render with IsFirstWakeOfNewSession=true.
	FirstWakeFlagGetAndClear func() bool
}

// Forwarder pumps wake JSONL from supervisor stdin to claude stdin.
type Forwarder struct {
	cfg ForwarderConfig
}

// NewForwarder constructs a Forwarder. Run blocks until ctx cancels or
// the reader returns EOF.
func NewForwarder(cfg ForwarderConfig) *Forwarder { return &Forwarder{cfg: cfg} }

// Run reads JSONL wake.Signal lines from r and forwards them. EOF
// returns nil; ctx cancellation returns ctx.Err().
func (f *Forwarder) Run(ctx context.Context, r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20) // 1 MiB max line
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var sig wake.Signal
		if err := json.Unmarshal(sc.Bytes(), &sig); err != nil {
			fmt.Fprintf(stderr(), "agent-loop: forward: bad JSONL: %v (line=%q)\n", err, sc.Text())
			continue
		}
		if f.cfg.IsDreaming != nil && f.cfg.IsDreaming() {
			if f.cfg.AppendToBacklog != nil {
				f.cfg.AppendToBacklog(sig)
			}
			continue
		}
		if err := f.deliverToClaude(sig); err != nil {
			return fmt.Errorf("forward: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

// deliverToClaude renders one wake and writes it as a stream-json user
// message to claude's stdin. Also exported for the rotation goroutine
// to drain the dream backlog.
func (f *Forwarder) deliverToClaude(sig wake.Signal) error {
	firstWake := false
	if f.cfg.FirstWakeFlagGetAndClear != nil {
		firstWake = f.cfg.FirstWakeFlagGetAndClear()
	}
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  string(sig.Reason),
		Hint:                    sig.Hint,
		InboxUnread:             sig.Context.InboxUnread,
		SinceLastWakeSeconds:    sig.Context.SinceLastWakeSeconds,
		MasterLikelyAsleep:      sig.Context.MasterLikelyAsleep,
		QuietStart:              f.cfg.Config.MindForm.QuietStart,
		QuietEnd:                f.cfg.Config.MindForm.QuietEnd,
		TZ:                      f.cfg.Config.MindForm.TZ,
		SinceLastDreamSeconds:   sig.Context.SinceLastDreamSeconds,
		DreamEligible:           sig.Context.DreamEligible,
		LastDreamNote:           f.cfg.DreamState.LastDreamNote,
		PlanID:                  sig.Context.PlanID,
		IsFirstWakeOfNewSession: firstWake,
		DreamCount:              f.cfg.DreamState.DreamCount,
		LastDreamFinishedAt:     f.cfg.DreamState.LastDreamFinishedAt,
	})

	envelope := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": msg}},
		},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal user message: %w", err)
	}
	body = append(body, '\n')
	if _, err := f.cfg.ClaudeStdin.Write(body); err != nil {
		return fmt.Errorf("write claude stdin: %w", err)
	}
	if f.cfg.OutstandingWakes != nil {
		f.cfg.OutstandingWakes.Add(1)
	}
	return nil
}
