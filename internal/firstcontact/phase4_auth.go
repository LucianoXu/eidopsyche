package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// installClaudeCredentials drives the auth-method menu through the
// wizard's Renderer and writes the resulting blob into the mind-form's
// docker volume. Runs after Orchestrate (so the volume exists) and
// before ContainerStart (so the first wake sees the credentials).
//
// Three on-disk install paths, mirroring `eidos forge login`:
//
//   - [token]    setup-token string → /eidos/claude/setup_token
//     (supervisor injects CLAUDE_CODE_OAUTH_TOKEN at every spawn)
//   - [creds]    credentials.json blob → /eidos/claude/.claude/.credentials.json
//     (claude reads it via $HOME/.claude at startup)
//   - [generate] `claude setup-token` here in an isolated HOME → credentials.json
//
// Option 3 needs a real TTY for the device-authorization flow; the
// other two are pure text input. r.WithRawTerminal hands the terminal
// back to the child for option 3 and re-acquires it afterwards.
func installClaudeCredentials(ctx context.Context, r render.Renderer, s *Summoning, image string) error {
	idx, err := r.PromptChoice(
		fmt.Sprintf(stringFor(s.Lang, "phase4_auth_q"), s.SummonedName),
		[]render.ChoiceOption{
			{Label: stringFor(s.Lang, "phase4_auth_token")},
			{Label: stringFor(s.Lang, "phase4_auth_creds")},
			{Label: stringFor(s.Lang, "phase4_auth_generate")},
		})
	if err != nil {
		return err
	}

	writer, err := claudeauth.NewForgectlVolumeWriter(ctx, s.Slug, image)
	if err != nil {
		return fmt.Errorf("volume writer: %w", err)
	}

	switch idx {
	case 0:
		return installViaSetupToken(r, s, writer)
	case 1:
		return installViaCredentials(r, s, writer)
	case 2:
		return installViaGenerate(r, s, writer)
	default:
		return fmt.Errorf("unexpected auth choice index %d", idx)
	}
}

func installViaSetupToken(r render.Renderer, s *Summoning, w claudeauth.VolumeWriter) error {
	for {
		tok, err := r.Prompt(stringFor(s.Lang, "phase4_auth_token_q"), render.PromptOpts{})
		if err != nil {
			return err
		}
		if tok == "" {
			r.Show(stringFor(s.Lang, "phase4_auth_token_empty"))
			continue
		}
		if err := claudeauth.WriteSetupTokenToVolumeUsing(w, tok); err != nil {
			return err
		}
		r.Show(stringFor(s.Lang, "phase4_auth_installed"))
		return nil
	}
}

func installViaCredentials(r render.Renderer, s *Summoning, w claudeauth.VolumeWriter) error {
	sub, err := r.PromptChoice(stringFor(s.Lang, "phase4_auth_creds_src_q"),
		[]render.ChoiceOption{
			{Label: stringFor(s.Lang, "phase4_auth_creds_src_file")},
			{Label: stringFor(s.Lang, "phase4_auth_creds_src_paste")},
		})
	if err != nil {
		return err
	}
	var blob []byte
	switch sub {
	case 0:
		for {
			path, perr := r.Prompt(stringFor(s.Lang, "phase4_auth_creds_path_q"), render.PromptOpts{})
			if perr != nil {
				return perr
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				r.Show(fmt.Sprintf(stringFor(s.Lang, "phase4_auth_creds_invalid"), path))
				continue
			}
			blob = body
			break
		}
	case 1:
		body, perr := r.EditMultiline(stringFor(s.Lang, "phase4_auth_creds_paste_q"), "")
		if perr != nil {
			if errors.Is(perr, io.EOF) {
				return errors.New("credentials paste cancelled")
			}
			return perr
		}
		blob = []byte(body)
	default:
		return fmt.Errorf("unexpected credentials source index %d", sub)
	}
	if err := claudeauth.WriteToVolumeUsing(w, blob); err != nil {
		return err
	}
	r.Show(stringFor(s.Lang, "phase4_auth_installed"))
	return nil
}

func installViaGenerate(r render.Renderer, s *Summoning, w claudeauth.VolumeWriter) error {
	r.Show(stringFor(s.Lang, "phase4_auth_generate_note"))
	var blob []byte
	err := r.WithRawTerminal(func() error {
		b, genErr := claudeauth.Generate(os.Stdin, os.Stdout, os.Stderr, "")
		if genErr != nil {
			return genErr
		}
		blob = b
		return nil
	})
	if err != nil {
		return err
	}
	if err := claudeauth.WriteToVolumeUsing(w, blob); err != nil {
		return err
	}
	r.Show(stringFor(s.Lang, "phase4_auth_installed"))
	return nil
}
