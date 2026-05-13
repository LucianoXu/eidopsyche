package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/promptcapture"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
	"github.com/spf13/cobra"
)

// Canonical in-container paths (must stay in sync with
// cmd/eidos/supervisor/agent_loop.go's agentLoop* constants).
const (
	promptDumpOntologyRoot   = "/eidos/ontology"
	promptDumpGateConfigPath = "/eidos/gate/config.toml"
	promptDumpClaudeDir      = "/eidos/ontology/.claude"
	promptDumpClaudeBin      = "/usr/local/bin/claude"
	promptDumpIdentityRel    = "self/identity.md"
)

// promptDumpInContainerInput is the typed input for runPromptDumpInContainer.
// All paths are explicit so tests can point at a fake filesystem.
type promptDumpInContainerInput struct {
	ClaudeBin      string
	OntologyRoot   string
	GateConfigPath string
	ClaudeDir      string // optional; empty is fine
	MindformName   string // from --mindform-name; "" if not provided
	Prompt         string
	Bare           bool
	Verbose        bool
	Stdout         io.Writer
	Stderr         io.Writer
}

// runPromptDumpInContainer reads identity.md + config.toml from the
// supplied paths, invokes promptcapture.Run, and writes the envelope
// JSON to input.Stdout.
func runPromptDumpInContainer(ctx context.Context, in promptDumpInContainerInput) error {
	if in.Stdout == nil {
		in.Stdout = os.Stdout
	}
	if in.Stderr == nil {
		in.Stderr = os.Stderr
	}

	identityPath := filepath.Join(in.OntologyRoot, promptDumpIdentityRel)
	identityRel := promptDumpIdentityRel
	if _, err := os.Stat(identityPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat identity: %w", err)
		}
		fmt.Fprintf(in.Stderr, "prompt-dump: identity not found at %s; proceeding bare\n", identityPath)
		identityRel = ""
	}

	var model string
	if cfg, err := config.Load(in.GateConfigPath); err == nil {
		model = cfg.MindForm.Model
	}

	// Assemble the full system prompt the agent-loop would send, so the
	// dump mirrors production. --bare omits the flag entirely (claude's
	// default preamble surfaces).
	systemPrompt := ""
	if !in.Bare {
		facts, _ := prompts.FromOntology(in.OntologyRoot)
		facts.Model = model
		facts.OntologyDir = in.OntologyRoot
		built, bErr := prompts.Build(ctx, facts, in.OntologyRoot)
		if bErr != nil {
			return fmt.Errorf("build system prompt: %w", bErr)
		}
		systemPrompt = built
	} else {
		identityRel = ""
	}

	env, err := promptcapture.Run(ctx, promptcapture.Opts{
		ClaudeBin:    in.ClaudeBin,
		Cwd:          in.OntologyRoot,
		SystemPrompt: systemPrompt,
		Model:        model,
		Prompt:       in.Prompt,
		ClaudeDir:    in.ClaudeDir,
		Verbose:      in.Verbose,
		CapturedFrom: promptcapture.CapturedFrom{
			Mindform:     in.MindformName,
			OntologyRoot: in.OntologyRoot,
			Model:        model,
			IdentityPath: identityRel,
			Bare:         in.Bare,
		},
		NoPOSTAfter: 10 * time.Second,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(in.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

func newPromptDumpInContainerCmd() *cobra.Command {
	var (
		prompt       string
		bare         bool
		mindformName string
		verbose      bool
	)
	cmd := &cobra.Command{
		Use:    "prompt-dump",
		Short:  "(in-container) Capture this mind-form's /v1/messages envelope",
		Hidden: true, // operators discover the host wrapper, not the worker
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPromptDumpInContainer(cmd.Context(), promptDumpInContainerInput{
				ClaudeBin:      promptDumpClaudeBin,
				OntologyRoot:   promptDumpOntologyRoot,
				GateConfigPath: promptDumpGateConfigPath,
				ClaudeDir:      promptDumpClaudeDir,
				MindformName:   mindformName,
				Prompt:         prompt,
				Bare:           bare,
				Verbose:        verbose,
				Stdout:         cmd.OutOrStdout(),
				Stderr:         cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "ping", "stub prompt sent to claude")
	cmd.Flags().BoolVar(&bare, "bare", false, "omit --system-prompt (use claude's default preamble)")
	cmd.Flags().StringVar(&mindformName, "mindform-name", "",
		"recorded in captured_from.mindform; host wrapper supplies this")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "log proxy traffic + claude stderr")
	return cmd
}
