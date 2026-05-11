package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// cadenceOption pairs a UI label key with the interval-string value the
// wizard stamps onto Summoning.HeartbeatInterval. The default entry
// (curatedCadenceOptions[0]) carries the empty value, which downstream
// applyHeartbeatEnv treats as a no-op so the supervisor uses
// config.DefaultHeartbeatInterval (currently 2h) at PID-1 startup.
type cadenceOption struct {
	labelKey string
	value    string
}

// curatedCadenceOptions is the curated cadence menu. Order matters: the
// CLI renderer's 1-indexed input maps to len(this)+1 menu positions
// (curated + Custom). Append-only — inserting in the middle shifts
// menu numbers an operator may already have memorized.
var curatedCadenceOptions = []cadenceOption{
	{labelKey: "phase3_5_cadence_default", value: ""}, // 2h via DefaultHeartbeatInterval
	{labelKey: "phase3_5_cadence_1h", value: "1h"},
	{labelKey: "phase3_5_cadence_30m", value: "30m"},
	{labelKey: "phase3_5_cadence_10m", value: "10m"},
	{labelKey: "phase3_5_cadence_5m", value: "5m"},
	{labelKey: "phase3_5_cadence_2m", value: "2m"},
	{labelKey: "phase3_5_cadence_1m", value: "1m"},
}

// indexCustom is the menu position of the "Custom" entry. Computed
// rather than hard-coded so curated reorderings can't silently desync
// it from the curated slice.
func indexCustom() int { return len(curatedCadenceOptions) }

// Phase3Cadence asks the operator to pick a HeartBeat cadence for the
// mind-form being summoned. Runs after the prefab / scratch choice and
// before Phase 4 seal. Stores the chosen interval-string (or "" for
// "use system default") into s.HeartbeatInterval. Re-asks on invalid
// custom input.
func Phase3Cadence(ctx context.Context, s *Summoning, r render.Renderer) error {
	choices := make([]render.ChoiceOption, 0, len(curatedCadenceOptions)+1)
	for _, o := range curatedCadenceOptions {
		choices = append(choices, render.ChoiceOption{Label: stringFor(s.Lang, o.labelKey)})
	}
	choices = append(choices, render.ChoiceOption{Label: stringFor(s.Lang, "phase3_5_cadence_custom")})

	idx, err := r.PromptChoice(stringFor(s.Lang, "phase3_5_cadence_q"), choices)
	if err != nil {
		return err
	}
	if idx < 0 || idx > indexCustom() {
		return fmt.Errorf("phase3_5_cadence: choice %d out of range", idx)
	}
	if idx < indexCustom() {
		s.HeartbeatInterval = curatedCadenceOptions[idx].value
		return nil
	}

	// Custom: prompt until valid.
	for {
		raw, err := r.Prompt(stringFor(s.Lang, "phase3_5_cadence_custom_q"), render.PromptOpts{})
		if err != nil {
			return err
		}
		if err := config.ValidateHeartbeatInterval(raw); err == nil {
			s.HeartbeatInterval = raw
			return nil
		}
		r.Show(stringFor(s.Lang, "phase3_5_cadence_invalid"))
	}
}

// keep context import stable across edits (Phase3Cadence does not
// itself need ctx today, but the calling convention in run.go does).
var _ = context.Background
