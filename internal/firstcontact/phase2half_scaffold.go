package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// ScaffoldChoice is the chosen scaffold-source outcome of Phase 2.5.
type ScaffoldChoice int

const (
	// ScaffoldExit ends the wizard here without summoning anything.
	// Equivalent in effect to Phase2Exit; surfaced as a Phase 2.5
	// option so an operator who reached the menu by accident can bow
	// out without aborting the program.
	ScaffoldExit ScaffoldChoice = iota
	// ScaffoldScratch takes the existing claude-driven character flow.
	ScaffoldScratch
	// ScaffoldPrefab takes the prefab catalogue flow.
	ScaffoldPrefab
)

// Phase2Half asks the operator whether to shape the new mind-form
// from scratch (claude-driven) or pick a prefab. Run after Phase 2
// commits to summon, before Phase 3.
func Phase2Half(ctx context.Context, s *Summoning, r render.Renderer) (ScaffoldChoice, error) {
	choices := []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase2_5_scratch")},
		{Label: stringFor(s.Lang, "phase2_5_prefab")},
		{Label: stringFor(s.Lang, "phase2_5_back")},
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase2_5_q"), choices)
	if err != nil {
		return 0, err
	}
	switch idx {
	case 0:
		return ScaffoldScratch, nil
	case 1:
		return ScaffoldPrefab, nil
	case 2:
		return ScaffoldExit, nil
	default:
		return 0, fmt.Errorf("phase2.5: choice index %d out of range", idx)
	}
}

// keep context import stable across edits.
var _ = context.Background
