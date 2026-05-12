package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/promptcapture"
	"github.com/spf13/cobra"
)

type promptDumpHostInput struct {
	Client  forgectl.Client
	Name    string
	Prompt  string
	OutPath string
	Bare    bool
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

func runPromptDumpHost(ctx context.Context, in promptDumpHostInput) error {
	if in.Stdout == nil {
		in.Stdout = os.Stdout
	}
	if in.Stderr == nil {
		in.Stderr = os.Stderr
	}
	if err := forgectl.ValidateName(in.Name); err != nil {
		return err
	}
	cont := forgectl.ContainerName(in.Name)
	state, err := in.Client.ContainerInspectState(ctx, cont)
	if err != nil {
		return err
	}
	if state == "absent" {
		return fmt.Errorf("mind-form %q not found", in.Name)
	}
	if state != "running" {
		return fmt.Errorf("mind-form %q not running (state: %s)", in.Name, state)
	}

	args := []string{
		"eidos", "forge", "prompt-dump",
		"--mindform-name", in.Name,
	}
	if in.Prompt != "" {
		args = append(args, "--prompt", in.Prompt)
	}
	if in.Bare {
		args = append(args, "--bare")
	}
	if in.Verbose {
		args = append(args, "-v")
	}

	res, err := in.Client.ContainerExec(ctx, cont, args)
	if err != nil {
		return fmt.Errorf("docker exec: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("in-container prompt-dump exited %d: %s",
			res.ExitCode, string(res.Stderr))
	}

	if in.OutPath == "" {
		// Stream the envelope JSON to host stdout verbatim — no
		// re-serialization needed.
		_, err := in.Stdout.Write(res.Stdout)
		return err
	}

	// -o set: parse and dual-write on the host.
	var env map[string]any
	if err := json.Unmarshal(res.Stdout, &env); err != nil {
		return fmt.Errorf("parse envelope from container: %w (stdout=%q)", err, string(res.Stdout))
	}
	wrote, err := promptcapture.WriteOutputs(env, in.OutPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(in.Stderr, "prompt-dump: wrote %s\n", strings.Join(wrote, ", "))
	return nil
}

func newPromptDumpHostCmd() *cobra.Command {
	var (
		prompt  string
		bare    bool
		verbose bool
		outPath string
	)
	cmd := &cobra.Command{
		Use:   "prompt-dump <name>",
		Short: "Capture the /v1/messages request envelope this mind-form would send right now",
		Long: `Capture the /v1/messages request envelope this mind-form's claude would
send right now: the default system prompt, tool catalogue, the layered
identity.md, the ontology's CLAUDE.md, the configured model, and the
first-message ambient context block.

This is a one-shot snapshot of a fresh session — it does not affect the
running agent-loop and does not include the rolling conversation history
of in-flight turns. For the response side (assistant thinking + tool
calls), use ` + "`eidos forge watch`" + `.

The capture runs inside the mind-form's container, using the mind-form's
own identity.md, config.toml, ontology CLAUDE.md, and claude binary.

Examples:
  eidos forge prompt-dump alice                # JSON envelope to stdout
  eidos forge prompt-dump alice -o snap        # writes snap.json + snap.md
  eidos forge prompt-dump alice -o snap.md     # Markdown only
  eidos forge prompt-dump alice --bare         # skip --append-system-prompt
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			return runPromptDumpHost(cmd.Context(), promptDumpHostInput{
				Client:  c,
				Name:    args[0],
				Prompt:  prompt,
				OutPath: outPath,
				Bare:    bare,
				Verbose: verbose,
				Stdout:  cmd.OutOrStdout(),
				Stderr:  cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "ping", "stub prompt sent to claude")
	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"write envelope to this path; .json/.md selects format, else both")
	cmd.Flags().BoolVar(&bare, "bare", false, "omit --append-system-prompt")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false,
		"surface in-container stderr (proxy + claude) to host stderr")
	return cmd
}
