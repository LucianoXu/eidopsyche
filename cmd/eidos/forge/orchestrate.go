package forge

import (
	"context"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
	iforge "github.com/LucianoXu/eidopsyche/internal/forge"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

// CreateOpts re-exports the orchestration option struct so the
// existing call sites in this package (create.go) and external
// callers (firstcontact) compile unchanged. The body now lives in
// internal/forge.
type CreateOpts = iforge.CreateOpts

// MindFormImageRepo re-exports the canonical mind-form image
// repository constant.
const MindFormImageRepo = iforge.MindFormImageRepo

// DefaultImage re-exports internal/forge.DefaultImage. The function
// (rather than a const) preserves the existing test seam that
// overrides version.Version at runtime.
func DefaultImage() string { return iforge.DefaultImage() }

// Orchestrate re-exports internal/forge.Orchestrate. cfg threads the
// host gate config through so the create-container step can mount
// configured workspaces alongside /eidos. cfg=nil mounts only /eidos
// (the freshly-summoned mind-form has no workspace config yet — the
// First Contact wizard takes this branch).
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts, cfg *config.Config) error {
	return iforge.Orchestrate(ctx, c, name, o, cfg)
}

// runCreate2 is the cobra-level entry: creates a real Docker client,
// orchestrates, and prints post-create UX.
func runCreate2(cmd *cobra.Command, name string, o CreateOpts) error {
	c, err := forgectl.New()
	if err != nil {
		return err
	}
	stateDir, err := config.ResolveStateDir("")
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(stateDir, "config.toml"))
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if err := Orchestrate(ctx, c, name, o, &cfg); err != nil {
		return err
	}
	cmd.Printf("created mind-form %q (label %q)\n", name, o.Label)
	cmd.Printf("  volume: %s\n", forgectl.VolumeName(name))
	cmd.Printf("  master: %s\n", o.Owner)
	cmd.Printf("  relay : %s\n", o.Relay)
	cmd.Printf("\nNext: print its card with `eidos forge status %s`, start it with `eidos forge start %s`.\n", name, name)
	if !o.NoLogin {
		cmd.Printf("\nRun `eidos forge login %s` now to log Claude Code into this mind-form.\n", name)
	}
	return nil
}
