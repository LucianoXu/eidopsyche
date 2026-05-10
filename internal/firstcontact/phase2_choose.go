package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// Phase2Action is the chosen mindform-stage outcome.
type Phase2Action int

const (
	// Phase2Exit ends the wizard here without summoning anything.
	Phase2Exit Phase2Action = iota
	// Phase2SummonLocal proceeds to summon a mind-form using the
	// host's local identity as master.
	Phase2SummonLocal
	// Phase2SummonCard proceeds to summon a mind-form using a card's
	// holder as master. Phase 2 has already loaded the card and
	// overwritten s.MasterLabel / s.MasterNpub / s.HomeRelay.
	Phase2SummonCard
)

// Phase2Deps are the inputs Phase 2 cannot derive from Summoning.
type Phase2Deps struct {
	EntryMode      EntryMode
	MasterCardPath string // empty unless --master-card was given
}

// Phase2 asks the operator what to do at the mind-form stage. The
// option set is computed from EntryMode and s.OperatorPresent (spec
// § 4.2). When MasterCardPath is non-empty, Phase 2 skips the menu
// entirely and uses the card directly (spec § 4.4.1: flag wins, no
// warning). EntryGateInit short-circuits to Phase2Exit.
//
// On Phase2SummonCard, Phase 2 loads the card and overwrites
// s.MasterLabel / s.MasterNpub / s.HomeRelay (per spec § 4.4.2:
// mind-form's home_relay = master's home_relay).
func Phase2(ctx context.Context, s *Summoning, r render.Renderer, d Phase2Deps) (Phase2Action, error) {
	// gate init never summons.
	if d.EntryMode == EntryGateInit {
		return Phase2Exit, nil
	}

	// --master-card flag wins, no warning.
	if d.MasterCardPath != "" {
		if err := loadCardIntoSummoning(s, d.MasterCardPath); err != nil {
			return 0, err
		}
		return Phase2SummonCard, nil
	}

	type opt struct {
		label  string
		action Phase2Action
	}
	var opts []opt
	if d.EntryMode != EntrySummon {
		opts = append(opts, opt{stringFor(s.Lang, "phase2_action_exit"), Phase2Exit})
	}
	if s.OperatorPresent {
		opts = append(opts, opt{stringFor(s.Lang, "phase2_action_local"), Phase2SummonLocal})
	}
	opts = append(opts, opt{stringFor(s.Lang, "phase2_action_card"), Phase2SummonCard})

	// Single-option degenerate case: `eidos summon` with no local
	// identity and no flag — only "用名片召唤" is offered, so skip the
	// menu and ask for the path directly.
	if len(opts) == 1 && opts[0].action == Phase2SummonCard {
		r.Show(stringFor(s.Lang, "phase2_card_required"))
		path, err := promptCardPath(s, r)
		if err != nil {
			return 0, err
		}
		if err := loadCardIntoSummoning(s, path); err != nil {
			return 0, err
		}
		return Phase2SummonCard, nil
	}

	choices := make([]render.ChoiceOption, 0, len(opts))
	for _, o := range opts {
		choices = append(choices, render.ChoiceOption{Label: o.label})
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase2_action_q"), choices)
	if err != nil {
		return 0, err
	}
	if idx < 0 || idx >= len(opts) {
		return 0, fmt.Errorf("phase2: choice index %d out of range", idx)
	}
	chosen := opts[idx].action

	if chosen == Phase2SummonCard {
		path, err := promptCardPath(s, r)
		if err != nil {
			return 0, err
		}
		if err := loadCardIntoSummoning(s, path); err != nil {
			return 0, err
		}
	}
	return chosen, nil
}

// promptCardPath asks for a card path and loops until the file parses.
// Empty input re-prompts.
func promptCardPath(s *Summoning, r render.Renderer) (string, error) {
	for {
		path, err := r.Prompt(stringFor(s.Lang, "phase2_card_path_q"), render.PromptOpts{})
		if err != nil {
			return "", err
		}
		if path == "" {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), "empty path"))
			continue
		}
		if _, err := card.Read(path); err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), err))
			continue
		}
		return path, nil
	}
}

// loadCardIntoSummoning reads a v1 card and overwrites the master fields
// on s. This is the canonical "card becomes the new mind-form's master"
// path — used by both the --master-card flag and the interactive
// "use a card" menu choice.
func loadCardIntoSummoning(s *Summoning, path string) error {
	c, err := card.Read(path)
	if err != nil {
		return fmt.Errorf("load card %q: %w", path, err)
	}
	s.MasterLabel = c.Label
	s.MasterNpub = c.Npub
	s.HomeRelay = c.Relay
	return nil
}

// keep context import stable when other phase files in this package
// shrink it; pure compile-time guard.
var _ = context.Background
