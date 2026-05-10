package forge

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
	"github.com/spf13/cobra"
)

// DefaultImage is the container image tag for new mind-forms when --image
// is not given. Set at build time via -ldflags or left as a sensible
// default; eidos forge create warns when host version != image tag.
var DefaultImage = "ghcr.io/lucianoxu/eidopsyche-mindform:dev"

// Orchestrate is the testable seam for `eidos forge create` and the
// First Contact wizard's phase 3. The wizard imports it directly as a
// sanctioned bootstrap exception (see CLAUDE.md "Single Call Path"):
// container creation does not flow through the daemon's methodTable
// today, and adding an IPC method just to wrap docker is out of scope
// for this milestone.
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error {
	vol := forgectl.VolumeName(name)
	cont := forgectl.ContainerName(name)
	if exists, err := c.VolumeExists(ctx, vol); err != nil {
		return fmt.Errorf("check volume: %w", err)
	} else if exists {
		return fmt.Errorf("volume %s already exists", vol)
	}
	if exists, err := c.ContainerExists(ctx, cont); err != nil {
		return fmt.Errorf("check container: %w", err)
	} else if exists {
		return fmt.Errorf("container %s already exists", cont)
	}

	image := o.Image
	if image == "" {
		image = DefaultImage
	}
	// Skip the pull when the image is already present locally. This is the
	// common case for dev / staging where `make image` produced a tag the
	// registry doesn't yet host (the worktree branch isn't published, the
	// CI hasn't pushed, etc.). When the image is missing we still attempt
	// the pull and surface its error.
	exists, err := c.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", image, err)
	}
	if !exists {
		if err := c.ImagePull(ctx, image, os.Stderr); err != nil {
			return fmt.Errorf("pull %s: %w", image, err)
		}
	}

	if err := c.VolumeCreate(ctx, vol); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}

	// Build the template tar to pipe into init-volume. PrefabID, if
	// set, switches the source to prefab/<id>/.
	pipeR, pipeW := io.Pipe()
	go func() {
		defer pipeW.Close()
		params := ontology.Params{
			Label:        o.Label,
			OwnerNpub:    o.Owner,
			OwnerLabel:   o.OwnerLabel,
			MindFormNpub: o.MindFormNpub,
			HomeRelay:    o.Relay,
			CreatedDate:  time.Now().UTC().Format("2006-01-02"),
			JournalEntry: o.JournalEntry,
		}
		var err error
		if o.PrefabID != "" {
			err = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
		} else {
			err = ontology.TarStream(pipeW, params)
		}
		if err != nil {
			_ = pipeW.CloseWithError(err)
		}
	}()

	env := []string{
		"EIDOS_IN_CONTAINER=1",
		"EIDOS_FORGE_NAME=" + name,
		"EIDOS_FORGE_LABEL=" + o.Label,
		"EIDOS_FORGE_OWNER=" + o.Owner,
		"EIDOS_FORGE_RELAY=" + o.Relay,
		"EIDOS_FORGE_MODEL=" + o.Model,
	}
	if o.KeyHex != "" {
		env = append(env, "EIDOS_FORGE_KEY_HEX="+o.KeyHex)
	}
	res, err := c.RunInit(ctx, forgectl.RunInitOpts{
		Image: image,
		Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
		// init-volume runs as root so it can extract the template tar,
		// clone the bundle, and git-init the parent ontology with full
		// privileges. Its last step chowns /eidos to 1000:1000; the
		// persistent container then starts as the image's USER (eidos).
		User:  "0:0",
		Env:   env,
		Cmd:   []string{"eidos", "forge", "init-volume"},
		Stdin: pipeR,
	})
	if err != nil {
		// On failure, roll back volume so the user can retry cleanly.
		_ = c.VolumeRemove(ctx, vol)
		return fmt.Errorf("init-volume: %w (stderr: %s)", err, string(res.Stderr))
	}

	// Create the persistent container in stopped state. `forge start` is
	// then a thin docker-start. Using the image's default entrypoint
	// (tini → entrypoint.sh → eidos supervisor run); no Cmd / Entrypoint
	// override needed.
	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:  cont,
		Image: image,
		Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
	}); err != nil {
		_ = c.VolumeRemove(ctx, vol)
		return fmt.Errorf("create container: %w", err)
	}
	return nil
}

// runCreate2 is the cobra-level entry: creates a real Docker client,
// orchestrates, and prints post-create UX.
func runCreate2(cmd *cobra.Command, name string, o CreateOpts) error {
	c, err := forgectl.New()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if err := Orchestrate(ctx, c, name, o); err != nil {
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
