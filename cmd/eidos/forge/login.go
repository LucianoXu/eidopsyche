// Package forge provides the `eidos forge ...` cobra subcommand tree.
// This file owns `eidos forge login`: authenticating claude inside a
// mindform's docker volume.
//
// The login flow installs a per-mindform OAuth credentials blob into
// /eidos/claude/.claude/.credentials.json. Operators provide the blob
// in one of three ways:
//
//  1. --token-file <path>  — read a .credentials.json file produced
//     by an earlier `claude setup-token` run anywhere convenient.
//  2. --paste               — read the blob from stdin (good for piping).
//  3. --generate            — drive `claude setup-token` here in an
//     isolated HOME so the operator's host claude is not touched.
//  4. No flag → interactive prompt offering paths 1+2 (paste/file) and 3.
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
	// Set the default production implementation. Using init() keeps the
	// var declaration clean while allowing tests to override per-call.
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) {
		return claudeauth.NewForgectlVolumeWriter(context.Background(), name, image)
	}
}

func newLoginCmd() *cobra.Command {
	var image, tokenFile string
	var paste, generate bool
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
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to a .credentials.json file to install")
	cmd.Flags().BoolVar(&paste, "paste", false, "read the credentials JSON from stdin")
	cmd.Flags().BoolVar(&generate, "generate", false, "drive `claude setup-token` in an isolated HOME")
	return cmd
}

// runInteractiveLogin walks the operator through the two-path prompt
// when no flag is given. Returns the credentials blob.
func runInteractiveLogin(cmd *cobra.Command, name string) ([]byte, error) {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	fmt.Fprintf(out, "How will %s authenticate?\n  [1] Paste a setup-token I've already generated\n  [2] Generate a fresh setup-token now (browser will be needed)\n> ", name)
	choice, err := in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(choice) {
	case "1":
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
			return nil, fmt.Errorf("phase1: invalid choice %q", sub)
		}
	case "2":
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
