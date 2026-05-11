package firstcontact

import (
	"context"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// scriptedRenderer drives Phase3Cadence with a scripted sequence of
// PromptChoice returns and Prompt() inputs. Only the methods
// Phase3Cadence calls do anything; the rest satisfy the interface.
type scriptedRenderer struct {
	choices []int
	inputs  []string
	shown   []string
}

func (r *scriptedRenderer) Capabilities() render.Capabilities {
	return render.Capabilities{}
}
func (r *scriptedRenderer) Frame(string)                    {}
func (r *scriptedRenderer) Show(s string)                   { r.shown = append(r.shown, s) }
func (r *scriptedRenderer) Typewriter(context.Context, string) {}
func (r *scriptedRenderer) Prompt(_ string, _ render.PromptOpts) (string, error) {
	s := r.inputs[0]
	r.inputs = r.inputs[1:]
	return s, nil
}
func (r *scriptedRenderer) PromptChoice(_ string, _ []render.ChoiceOption) (int, error) {
	c := r.choices[0]
	r.choices = r.choices[1:]
	return c, nil
}
func (r *scriptedRenderer) Status(string) render.StatusHandle { return nopStatus{} }
func (r *scriptedRenderer) Logo(context.Context, time.Duration) {}

type nopStatus struct{}

func (nopStatus) Update(string) {}
func (nopStatus) Stop()         {}

func TestPhase3Cadence_DefaultChoice(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &scriptedRenderer{choices: []int{0}} // "2h (default)" → empty value
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "" {
		t.Errorf("HeartbeatInterval = %q, want \"\" (default sentinel)", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CuratedChoice(t *testing.T) {
	// Index 5 = "2m" in curatedCadenceOptions
	// (order: 2h default, 1h, 30m, 10m, 5m, 2m, 1m).
	s := &Summoning{Lang: "en"}
	r := &scriptedRenderer{choices: []int{5}}
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "2m" {
		t.Errorf("HeartbeatInterval = %q, want 2m", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CustomValid(t *testing.T) {
	// Index 7 = Custom (one past the curated set of 7 entries 0..6).
	s := &Summoning{Lang: "en"}
	r := &scriptedRenderer{
		choices: []int{7},
		inputs:  []string{"3m"},
	}
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "3m" {
		t.Errorf("HeartbeatInterval = %q, want 3m", s.HeartbeatInterval)
	}
}

func TestPhase3Cadence_CustomInvalidThenValid(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &scriptedRenderer{
		choices: []int{7},
		inputs:  []string{"90m", "30m"},
	}
	if err := Phase3Cadence(context.Background(), s, r); err != nil {
		t.Fatalf("Phase3Cadence: %v", err)
	}
	if s.HeartbeatInterval != "30m" {
		t.Errorf("HeartbeatInterval = %q, want 30m", s.HeartbeatInterval)
	}
	if len(r.shown) != 1 {
		t.Errorf("expected 1 invalid-feedback Show() call, got %d (%v)", len(r.shown), r.shown)
	}
}

func TestPhase3Cadence_OutOfRange(t *testing.T) {
	s := &Summoning{Lang: "en"}
	r := &scriptedRenderer{choices: []int{99}}
	err := Phase3Cadence(context.Background(), s, r)
	if err == nil {
		t.Fatal("expected error for choice 99")
	}
}

func TestIndexCustomMatchesCuratedLen(t *testing.T) {
	if indexCustom() != len(curatedCadenceOptions) {
		t.Errorf("indexCustom()=%d, want %d", indexCustom(), len(curatedCadenceOptions))
	}
}
