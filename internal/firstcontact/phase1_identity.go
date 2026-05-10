package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// Phase1Deps are the inputs Phase 1 cannot derive from Summoning.
type Phase1Deps struct {
	StateDir string

	// OperatorKeyPath, if non-empty, supplies the nsec for the import
	// branch from a file (the file is also accepted as 64-char hex).
	// Threaded from cmd/eidos/summon's --key-file flag.
	OperatorKeyPath string

	// OptionalCardPath, if non-empty, supplies the operator's own card
	// for the import branch — its label and home_relay become the
	// defaults in subsequent prompts.
	OptionalCardPath string
}

// ErrSelfHostExit kept for backward compatibility — historic callers
// (and possibly tests) reference this sentinel. The new identity stage
// no longer produces it; the self-host branch was dropped per spec.
var ErrSelfHostExit = errors.New("self-host relay chosen — wizard exiting cleanly")

// Phase1 asks the operator to choose: create a new identity, import an
// existing one, or skip. Mutates s in place. Skip writes no disk state
// and leaves OperatorPresent=false; create / import populate
// MasterLabel / MasterNpub / HomeRelay and set OperatorPresent=true.
func Phase1(ctx context.Context, s *Summoning, r render.Renderer, d Phase1Deps) error {
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase1_choose_q"), []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase1_choose_create")},
		{Label: stringFor(s.Lang, "phase1_choose_import")},
		{Label: stringFor(s.Lang, "phase1_choose_skip")},
	})
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		return phase1Create(s, r, d)
	case 1:
		return phase1Import(s, r, d)
	case 2:
		r.Show(stringFor(s.Lang, "phase1_skip_note"))
		return nil
	}
	return fmt.Errorf("phase1: unknown choice index %d", idx)
}

func phase1Create(s *Summoning, r render.Renderer, d Phase1Deps) error {
	label, err := r.Prompt(stringFor(s.Lang, "phase1_label_q"), render.PromptOpts{
		HelpText: stringFor(s.Lang, "phase1_label_help"),
	})
	if err != nil {
		return err
	}
	homeRelay, err := promptHomeRelayWithDefault(s, r, "")
	if err != nil {
		return err
	}
	npub, err := identity.Bootstrap(d.StateDir, label, homeRelay)
	if err != nil {
		return fmt.Errorf("bootstrap operator identity: %w", err)
	}
	s.MasterLabel = label
	s.MasterNpub = npub
	s.HomeRelay = homeRelay
	s.OperatorPresent = true
	return nil
}

func phase1Import(s *Summoning, r render.Renderer, d Phase1Deps) error {
	hexKey, err := readImportKey(s, r, d)
	if err != nil {
		return err
	}
	var defaultLabel, defaultRelay string
	if c, ok := readImportCard(s, r, d); ok {
		defaultLabel = c.Label
		defaultRelay = c.Relay
	}
	labelOpts := render.PromptOpts{HelpText: stringFor(s.Lang, "phase1_label_help")}
	if defaultLabel != "" {
		labelOpts.AllowEmpty = true
		labelOpts.HelpText = fmt.Sprintf("default: %s", defaultLabel)
	}
	label, err := r.Prompt(stringFor(s.Lang, "phase1_label_q"), labelOpts)
	if err != nil {
		return err
	}
	if label == "" {
		label = defaultLabel
	}
	homeRelay, err := promptHomeRelayWithDefault(s, r, defaultRelay)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(d.StateDir, "key")
	if err := os.MkdirAll(d.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(hexKey+"\n"), 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	npub, err := identity.BootstrapWithExistingKey(d.StateDir, label, homeRelay)
	if err != nil {
		return fmt.Errorf("bootstrap with existing key: %w", err)
	}
	s.MasterLabel = label
	s.MasterNpub = npub
	s.HomeRelay = homeRelay
	s.OperatorPresent = true
	return nil
}

// readImportKey returns a canonical lowercase 64-char hex private key.
// Source preference: (1) Deps.OperatorKeyPath, (2) interactive paste.
func readImportKey(s *Summoning, r render.Renderer, d Phase1Deps) (string, error) {
	if d.OperatorKeyPath != "" {
		body, err := os.ReadFile(d.OperatorKeyPath)
		if err != nil {
			return "", fmt.Errorf("read --key-file: %w", err)
		}
		return parsePrivateKey(strings.TrimSpace(string(body)))
	}
	for {
		input, err := r.Prompt(stringFor(s.Lang, "phase1_import_nsec_q"), render.PromptOpts{})
		if err != nil {
			return "", err
		}
		hex, err := parsePrivateKey(strings.TrimSpace(input))
		if err == nil {
			return hex, nil
		}
		r.Show(stringFor(s.Lang, "phase1_import_nsec_invalid"))
	}
}

var hex64Phase1 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parsePrivateKey accepts an nsec1... NIP-19 string OR a 64-char hex
// (case-insensitive). Returns canonical lowercase hex.
func parsePrivateKey(input string) (string, error) {
	if strings.HasPrefix(input, "nsec1") {
		hex, err := identity.DecodeNsec(input)
		if err != nil {
			return "", err
		}
		return hex, nil
	}
	lower := strings.ToLower(input)
	if hex64Phase1.MatchString(lower) {
		return lower, nil
	}
	return "", errors.New("input is neither nsec1... nor 64-char hex")
}

// readImportCard tries to load a card file — from Deps or interactive
// prompt. Returns (card, true) on success, (zero, false) when skipped
// or unreadable. Reading a card is optional in the import branch.
func readImportCard(s *Summoning, r render.Renderer, d Phase1Deps) (card.Card, bool) {
	if d.OptionalCardPath != "" {
		c, err := card.Read(d.OptionalCardPath)
		if err != nil {
			r.Show(fmt.Sprintf(stringFor(s.Lang, "phase1_import_card_invalid"), err))
			return card.Card{}, false
		}
		return c, true
	}
	path, err := r.Prompt(stringFor(s.Lang, "phase1_import_card_q"), render.PromptOpts{AllowEmpty: true})
	if err != nil || path == "" {
		return card.Card{}, false
	}
	c, err := card.Read(path)
	if err != nil {
		r.Show(fmt.Sprintf(stringFor(s.Lang, "phase1_import_card_invalid"), err))
		return card.Card{}, false
	}
	return c, true
}

// promptHomeRelayWithDefault asks the operator to pick a home relay.
// When dflt is non-empty, it is offered as the first choice ("use the
// value from your card"). The other two choices are unchanged: public
// station and custom URL. Returns the chosen URL.
func promptHomeRelayWithDefault(s *Summoning, r render.Renderer, dflt string) (string, error) {
	choices := []render.ChoiceOption{}
	if dflt != "" {
		choices = append(choices, render.ChoiceOption{
			Label: fmt.Sprintf(stringFor(s.Lang, "phase1_relay_card_default"), dflt),
		})
	}
	choices = append(choices,
		render.ChoiceOption{Label: stringFor(s.Lang, "phase1_relay_public"), Hint: PublicHomeRelay},
		render.ChoiceOption{Label: stringFor(s.Lang, "phase1_relay_custom")},
	)
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase1_relay_q"), choices)
	if err != nil {
		return "", err
	}
	if dflt != "" {
		switch idx {
		case 0:
			return dflt, nil
		case 1:
			return PublicHomeRelay, nil
		case 2:
			return promptCustomRelay(s, r)
		}
	} else {
		switch idx {
		case 0:
			return PublicHomeRelay, nil
		case 1:
			return promptCustomRelay(s, r)
		}
	}
	return "", fmt.Errorf("phase1 relay: unknown choice index %d", idx)
}

func promptCustomRelay(s *Summoning, r render.Renderer) (string, error) {
	for {
		candidate, err := r.Prompt(stringFor(s.Lang, "phase1_relay_custom_q"), render.PromptOpts{})
		if err != nil {
			return "", err
		}
		if u, perr := url.Parse(candidate); perr == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Host != "" {
			return candidate, nil
		}
		r.Show(stringFor(s.Lang, "phase1_relay_custom_invalid"))
	}
}

// Compile-time guard so context import stays — used by other phase
// files in this package; keeps imports stable across reorganization.
var _ = context.Background
