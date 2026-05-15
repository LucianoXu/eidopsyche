// Package forge provides the `eidos forge ...` cobra subcommand tree.
// This file owns `eidos forge login`: authenticating claude inside a
// mindform's docker volume.
//
// Two install paths land in distinct files in the volume:
//
//   - Raw setup-token (--setup-token-stdin and interactive [1]) writes
//     /eidos/claude/setup_token and removes any stale credentials.json.
//     The supervisor reads this file at every claude spawn and injects
//     CLAUDE_CODE_OAUTH_TOKEN into claude's environment.
//
//   - Pre-built credentials.json (--token-file, --paste, --generate, and
//     interactive [2] / [3]) writes /eidos/claude/.claude/.credentials.json
//     and removes any stale setup-token file. Claude reads it at startup
//     via its $HOME/.claude default path.
//
// The two are mutually exclusive on disk so claude's LN5 guard never
// has to pick between them. Operators select a path via a flag, or use
// the interactive prompt when none is given:
//
//  1. --setup-token-stdin — read a raw sk-ant-oat01-... token from
//     stdin (one line). Stdin-only by design: argv would leak the
//     token into shell history, `ps`, and CI logs.
//  2. --token-file <path> — read a pre-built .credentials.json file
//     (output of an earlier `claude setup-token` run).
//  3. --paste             — read a pre-built credentials.json blob
//     from stdin (good for piping).
//  4. --generate          — drive `claude setup-token` here in an
//     isolated HOME so the operator's host claude is not touched.
//
// With no flag the command prompts interactively, exposing the same
// four paths through a menu.
//
// The old --from-host path (which copied the host's ~/.claude.json
// verbatim into the volume) is removed: shared OAuth tokens between
// host and container caused silent 401-on-exit-1 failures whenever
// either side refreshed.
package forge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// installVolume is the test seam — production wires it to a
// claudeauth.VolumeWriter backed by forgectl + docker.
var installVolume func(name, image string) (claudeauth.VolumeWriter, error)

func init() {
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) {
		return claudeauth.NewForgectlVolumeWriter(context.Background(), name, image)
	}
}

// loginInput discriminates the two on-disk install paths. Exactly one
// of the fields is set after a successful read from the operator.
type loginInput struct {
	setupToken string // non-empty -> env-var path (writes claude/setup_token)
	blob       []byte // non-nil   -> file path     (writes claude/.claude/.credentials.json)
}

func newLoginCmd() *cobra.Command {
	var image, tokenFile string
	var setupTokenStdin, paste, generate bool
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Authenticate Claude Code inside a mind-form's volume (isolated per-mindform setup-token)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			img := image
			if img == "" {
				img = DefaultImage()
			}

			input, err := readLoginInput(cmd, name, setupTokenStdin, tokenFile, paste, generate)
			if err != nil {
				return err
			}

			writer, err := installVolume(name, img)
			if err != nil {
				return err
			}
			if input.setupToken != "" {
				if err := claudeauth.WriteSetupTokenToVolumeUsing(writer, input.setupToken); err != nil {
					return err
				}
			} else {
				if err := claudeauth.WriteToVolumeUsing(writer, input.blob); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ credentials installed into eidos-mindform-%s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	// Stdin-only by design: passing a bearer token via argv leaks it into
	// shell history, `ps`, and CI logs. Operators pipe the token in.
	cmd.Flags().BoolVar(&setupTokenStdin, "setup-token-stdin", false, "read a raw setup-token (sk-ant-oat01-...) from stdin; supervisor injects CLAUDE_CODE_OAUTH_TOKEN at each claude spawn")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to a pre-built .credentials.json file")
	cmd.Flags().BoolVar(&paste, "paste", false, "read a pre-built credentials.json blob from stdin")
	cmd.Flags().BoolVar(&generate, "generate", false, "drive `claude setup-token` in an isolated HOME")
	return cmd
}

// readLoginInput dispatches to the right reader based on the flag
// constellation. Exactly one return field is populated on success.
func readLoginInput(cmd *cobra.Command, name string, setupTokenStdin bool, tokenFile string, paste, generate bool) (loginInput, error) {
	switch {
	case setupTokenStdin:
		// Read one line so the command doesn't appear to hang when
		// invoked from an interactive shell — newline ends input,
		// any trailing bytes are discarded. Piped callers without a
		// trailing newline hit io.EOF, which is fine: ReadString
		// returns the data it accumulated before EOF.
		raw, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && err != io.EOF {
			return loginInput{}, fmt.Errorf("read stdin: %w", err)
		}
		tok := strings.TrimSpace(raw)
		if tok == "" {
			return loginInput{}, fmt.Errorf("empty setup-token")
		}
		return loginInput{setupToken: tok}, nil
	case tokenFile != "":
		blob, err := os.ReadFile(tokenFile)
		if err != nil {
			return loginInput{}, fmt.Errorf("read %s: %w", tokenFile, err)
		}
		return loginInput{blob: blob}, nil
	case paste:
		blob, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return loginInput{}, fmt.Errorf("read stdin: %w", err)
		}
		return loginInput{blob: blob}, nil
	case generate:
		blob, err := claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
		if err != nil {
			return loginInput{}, err
		}
		return loginInput{blob: blob}, nil
	default:
		return runInteractiveLogin(cmd, name)
	}
}

// runInteractiveLogin walks the operator through the menu when no flag
// is given. Option ordering reflects expected operator frequency:
// raw setup-token first (the typical case after browser setup),
// pre-built credentials.json second (advanced/automation), interactive
// generate third (operator without a token yet).
func runInteractiveLogin(cmd *cobra.Command, name string) (loginInput, error) {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	fmt.Fprintf(out, "How will %s authenticate?\n"+
		"  [1] Paste a setup-token string (sk-ant-oat01-... from claude.ai/setup)\n"+
		"  [2] Use a pre-generated credentials.json blob\n"+
		"  [3] Generate a fresh setup-token now (interactive, browser needed)\n"+
		"> ", name)
	choice, err := in.ReadString('\n')
	if err != nil {
		return loginInput{}, err
	}
	switch strings.TrimSpace(choice) {
	case "1":
		fmt.Fprintf(out, "Paste the setup-token (single line, enter to confirm):\n> ")
		raw, err := in.ReadString('\n')
		if err != nil && err != io.EOF {
			return loginInput{}, err
		}
		tok := strings.TrimSpace(raw)
		if tok == "" {
			return loginInput{}, fmt.Errorf("empty setup-token")
		}
		return loginInput{setupToken: tok}, nil
	case "2":
		fmt.Fprintf(out, "Source?\n  [1] Read from a file\n  [2] Paste contents (blank line ends)\n> ")
		sub, err := in.ReadString('\n')
		if err != nil {
			return loginInput{}, err
		}
		switch strings.TrimSpace(sub) {
		case "1":
			fmt.Fprintf(out, "Path to credentials file:\n> ")
			path, err := in.ReadString('\n')
			if err != nil {
				return loginInput{}, err
			}
			trimmed := strings.TrimSpace(path)
			blob, err := os.ReadFile(trimmed)
			if err != nil {
				return loginInput{}, fmt.Errorf("read %s: %w", trimmed, err)
			}
			return loginInput{blob: blob}, nil
		case "2":
			fmt.Fprintf(out, "Paste credentials (blank line ends):\n")
			var buf strings.Builder
			for {
				line, err := in.ReadString('\n')
				if err != nil && err != io.EOF {
					return loginInput{}, err
				}
				if strings.TrimSpace(line) == "" {
					break
				}
				buf.WriteString(line)
				if err == io.EOF {
					break
				}
			}
			return loginInput{blob: []byte(buf.String())}, nil
		default:
			return loginInput{}, fmt.Errorf("credentials source: invalid choice %q", sub)
		}
	case "3":
		blob, err := claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
		if err != nil {
			return loginInput{}, err
		}
		return loginInput{blob: blob}, nil
	default:
		return loginInput{}, fmt.Errorf("invalid choice %q", choice)
	}
}
