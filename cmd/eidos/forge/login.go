// Package forge provides the `eidos forge ...` cobra subcommand tree.
// This file owns `eidos forge login`: authenticating claude inside a
// mindform's docker volume.
//
// The login flow installs a per-mindform OAuth credentials blob into
// /eidos/claude/.claude/.credentials.json. Operators select a source
// via one of four flags, or use the interactive prompt when none is
// given:
//
//  1. --setup-token-stdin — read a raw sk-ant-oat01-... token from
//     stdin (one line) and wrap it into a credentials.json blob.
//     Stdin-only by design: passing the token via argv would leak it
//     into shell history, `ps`, and CI logs.
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

			var (
				blob []byte
				err  error
			)
			switch {
			case setupTokenStdin:
				// Read one line so the command doesn't appear to hang when
				// invoked from an interactive shell — newline ends input,
				// any trailing bytes are discarded. Piped callers without a
				// trailing newline hit io.EOF, which is fine: ReadString
				// returns the data it accumulated before EOF.
				raw, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil && readErr != io.EOF {
					return fmt.Errorf("read stdin: %w", readErr)
				}
				blob, err = claudeauth.WrapSetupToken(raw)
				if err != nil {
					return err
				}
			case tokenFile != "":
				blob, err = os.ReadFile(tokenFile)
				if err != nil {
					return fmt.Errorf("read %s: %w", tokenFile, err)
				}
			case paste:
				blob, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			case generate:
				blob, err = claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
				if err != nil {
					return err
				}
			default:
				blob, err = runInteractiveLogin(cmd, name)
				if err != nil {
					return err
				}
			}

			writer, err := installVolume(name, img)
			if err != nil {
				return err
			}
			if err := claudeauth.WriteToVolumeUsing(writer, blob); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ credentials installed into eidos-mindform-%s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	// Stdin-only by design: passing a bearer token via argv leaks it into
	// shell history, `ps`, and CI logs. Operators pipe the token in.
	cmd.Flags().BoolVar(&setupTokenStdin, "setup-token-stdin", false, "read a raw setup-token (sk-ant-oat01-...) from stdin and wrap it into a credentials blob")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to a pre-built .credentials.json file")
	cmd.Flags().BoolVar(&paste, "paste", false, "read a pre-built credentials.json blob from stdin")
	cmd.Flags().BoolVar(&generate, "generate", false, "drive `claude setup-token` in an isolated HOME")
	return cmd
}

// runInteractiveLogin walks the operator through the menu when no flag
// is given. Option ordering reflects expected operator frequency:
// raw setup-token first (the typical case after browser setup),
// pre-built credentials.json second (advanced/automation), interactive
// generate third (operator without a token yet).
func runInteractiveLogin(cmd *cobra.Command, name string) ([]byte, error) {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	fmt.Fprintf(out, "How will %s authenticate?\n"+
		"  [1] Paste a setup-token string (sk-ant-oat01-... from claude.ai/setup)\n"+
		"  [2] Use a pre-generated credentials.json blob\n"+
		"  [3] Generate a fresh setup-token now (interactive, browser needed)\n"+
		"> ", name)
	choice, err := in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(choice) {
	case "1":
		fmt.Fprintf(out, "Paste the setup-token (single line, enter to confirm):\n> ")
		tok, err := in.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		return claudeauth.WrapSetupToken(tok)
	case "2":
		fmt.Fprintf(out, "Source?\n  [1] Read from a file\n  [2] Paste contents (blank line ends)\n> ")
		sub, err := in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		switch strings.TrimSpace(sub) {
		case "1":
			fmt.Fprintf(out, "Path to credentials file:\n> ")
			path, err := in.ReadString('\n')
			if err != nil {
				return nil, err
			}
			return os.ReadFile(strings.TrimSpace(path))
		case "2":
			fmt.Fprintf(out, "Paste credentials (blank line ends):\n")
			var buf strings.Builder
			for {
				line, err := in.ReadString('\n')
				if err != nil && err != io.EOF {
					return nil, err
				}
				if strings.TrimSpace(line) == "" {
					break
				}
				buf.WriteString(line)
				if err == io.EOF {
					break
				}
			}
			return []byte(buf.String()), nil
		default:
			return nil, fmt.Errorf("credentials source: invalid choice %q", sub)
		}
	case "3":
		return claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
	default:
		return nil, fmt.Errorf("invalid choice %q", choice)
	}
}

// InstallLoginInteractive is the wizard-callable shim. Used by
// internal/firstcontact/phase4_seal.go.
func InstallLoginInteractive(name, image string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newLoginCmd()
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{name, "--image", image})
	return cmd.Execute()
}
