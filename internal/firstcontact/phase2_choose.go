package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/identity"
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
		c, err := card.Read(d.MasterCardPath)
		if err != nil {
			return 0, fmt.Errorf("load card %q: %w", d.MasterCardPath, err)
		}
		applyCardToSummoning(s, c)
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
	// menu and ask for the card directly.
	if len(opts) == 1 && opts[0].action == Phase2SummonCard {
		r.Show(stringFor(s.Lang, "phase2_card_required"))
		c, err := promptCard(s, r)
		if err != nil {
			return 0, err
		}
		applyCardToSummoning(s, c)
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
		c, err := promptCard(s, r)
		if err != nil {
			return 0, err
		}
		applyCardToSummoning(s, c)
	}
	return chosen, nil
}

// promptCard asks the operator how they want to provide the master's
// card (file path or pasted contents) and returns the parsed card.
func promptCard(s *Summoning, r render.Renderer) (card.Card, error) {
	choices := []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase2_card_source_file")},
		{Label: stringFor(s.Lang, "phase2_card_source_paste")},
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase2_card_source_q"), choices)
	if err != nil {
		return card.Card{}, err
	}
	switch idx {
	case 0:
		return promptCardFromFile(s, r)
	case 1:
		return promptCardFromPaste(s, r)
	default:
		return card.Card{}, fmt.Errorf("phase2: card-source index %d out of range", idx)
	}
}

// promptCardFromFile asks for a path and loops until the file parses
// as a valid v1 card. Empty input re-prompts.
func promptCardFromFile(s *Summoning, r render.Renderer) (card.Card, error) {
	for {
		path, err := r.Prompt(stringFor(s.Lang, "phase2_card_path_q"), render.PromptOpts{})
		if err != nil {
			return card.Card{}, err
		}
		if path == "" {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), "empty path"))
			continue
		}
		c, err := card.Read(path)
		if err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), err))
			continue
		}
		return c, nil
	}
}

// promptCardFromPaste asks the operator to paste either a mindgate://
// URI or a full TOML card body, and loops until parsing succeeds.
func promptCardFromPaste(s *Summoning, r render.Renderer) (card.Card, error) {
	for {
		text, err := r.Prompt(stringFor(s.Lang, "phase2_card_paste_q"), render.PromptOpts{Multiline: true})
		if err != nil {
			return card.Card{}, err
		}
		c, err := parseCardContent(text)
		if err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase2_card_invalid"), err))
			continue
		}
		return c, nil
	}
}

// parseCardContent decodes a pasted card. A leading `mindgate://`
// signals the URI form (single line, only Label/Npub/Relay carried);
// anything else is treated as a TOML v1 card body.
//
// URI inputs go through the same Label/Npub/Relay invariant checks as
// Card.Validate (modulo the fields the URI form does not carry); a
// bech32 npub that fails to decode, a missing label, or a relay that
// is not a ws:// or wss:// URL with a host are all rejected up front
// rather than discovered later.
func parseCardContent(text string) (card.Card, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return card.Card{}, errors.New("empty input")
	}
	if strings.HasPrefix(trimmed, "mindgate://") {
		// URI form: take just the first line; ignore any trailing
		// noise the user may have pasted along with it.
		uri := trimmed
		if i := strings.IndexAny(uri, "\r\n"); i >= 0 {
			uri = uri[:i]
		}
		c, err := card.Parse(uri)
		if err != nil {
			return card.Card{}, err
		}
		if c.Label == "" {
			return card.Card{}, errors.New("URI is missing the label query parameter")
		}
		if _, err := identity.DecodeNpub(c.Npub); err != nil {
			return card.Card{}, fmt.Errorf("npub: %w", err)
		}
		if c.Relay == "" {
			return card.Card{}, errors.New("URI is missing the home relay")
		}
		u, err := url.Parse(c.Relay)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return card.Card{}, fmt.Errorf("home_relay: must be ws:// or wss:// URL with host (got %q)", c.Relay)
		}
		return c, nil
	}
	return card.Decode(strings.NewReader(trimmed))
}

// applyCardToSummoning copies the master fields from c into s. This is
// the canonical "card becomes the new mind-form's master" step — used
// by the --master-card flag, the file-path interactive branch, and the
// paste-content interactive branch alike.
func applyCardToSummoning(s *Summoning, c card.Card) {
	s.MasterLabel = c.Label
	s.MasterNpub = c.Npub
	s.HomeRelay = c.Relay
}

// keep context import stable when other phase files in this package
// shrink it; pure compile-time guard.
var _ = context.Background
