package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// Phase1Deps are the inputs Phase1 cannot derive from Summoning.
type Phase1Deps struct {
	StateDir string
}

// ErrSelfHostExit is returned when the operator chose self-host —
// the wizard prints standalone instructions and exits cleanly. This
// is not a failure from the operator's perspective; the cmd-level
// caller treats it as exit-code 0.
var ErrSelfHostExit = errors.New("self-host relay chosen — wizard exiting cleanly")

// Phase1 collects the operator's label and home relay, then calls
// identity.Bootstrap to persist them. Mutates s in place.
//
// On the self-host branch, returns ErrSelfHostExit after printing
// standalone instructions for `eidos relay init`. The wizard does NOT
// embed the relay-init flag surface — keeping the two surfaces in
// sync would be a maintenance burden out of proportion to the saved
// keystrokes.
func Phase1(ctx context.Context, s *Summoning, r render.Renderer, d Phase1Deps) error {
	label, err := r.Prompt(stringFor(s.Lang, "phase1_label_q"), render.PromptOpts{
		HelpText: stringFor(s.Lang, "phase1_label_help"),
	})
	if err != nil {
		return err
	}
	s.OperatorLabel = label

	idx, err := r.PromptChoice(stringFor(s.Lang, "phase1_relay_q"), []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase1_relay_public"), Hint: PublicHomeRelay},
		{Label: stringFor(s.Lang, "phase1_relay_selfhost")},
		{Label: stringFor(s.Lang, "phase1_relay_custom")},
	})
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		s.HomeRelay = PublicHomeRelay
	case 1:
		r.Show(stringFor(s.Lang, "phase1_selfhost_instructions"))
		return ErrSelfHostExit
	case 2:
		for {
			candidate, err := r.Prompt(stringFor(s.Lang, "phase1_relay_custom_q"), render.PromptOpts{})
			if err != nil {
				return err
			}
			if u, perr := url.Parse(candidate); perr == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Host != "" {
				s.HomeRelay = candidate
				break
			}
			r.Show(stringFor(s.Lang, "phase1_relay_custom_invalid"))
		}
	}

	npub, err := identity.Bootstrap(d.StateDir, s.OperatorLabel, s.HomeRelay)
	if err != nil {
		return fmt.Errorf("bootstrap operator identity: %w", err)
	}
	s.OperatorNpub = npub
	return nil
}
