package forge

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

// CreateOpts carries the parameters for Orchestrate. Field names are
// stable contracts with cmd/eidos/forge/create.go and the First
// Contact wizard.
//
// The wizard-only fields KeyHex and SummoningBook are not exposed as
// CLI flags — they make no sense for scripted use.
type CreateOpts struct {
	Owner   string
	Relay   string
	Label   string
	NoLogin bool
	Image   string
	Model   string

	// HeartbeatInterval is the per-mind-form HeartBeat cadence written
	// into /eidos/gate/config.toml at init-volume time. Empty leaves
	// the [heartbeat] block unset, so the supervisor falls back to
	// config.DefaultHeartbeatInterval. Must be in the supported set
	// (validated via config.ValidateHeartbeatInterval).
	HeartbeatInterval string

	KeyHex        string // wizard-only: pre-generated MindForm private hex
	SummoningBook string // wizard-only: rendered summoning-book markdown
	RoleResearch  string // wizard-only: rendered self/role-research.md (scratch path)

	// PrefabID, when non-empty, makes Orchestrate stream the
	// prefab/<id>/ tree into the volume instead of the canonical
	// template. The wizard's Phase 3 prefab branch sets this; the
	// scratch path leaves it empty.
	PrefabID string

	// OwnerLabel is the master's human-readable label, surfaced to
	// prefab .tpl files (e.g. summoning-book templates). Empty on
	// scratch path; the scratch template/ does not reference it.
	OwnerLabel string

	// MindFormNpub is the new mind-form's npub, surfaced to prefab
	// .tpl files. The wizard knows it after key generation; CLI
	// `eidos forge create` (which has no key context) leaves it
	// empty — prefab path is wizard-only.
	MindFormNpub string
}

// Orchestrate is the testable seam for `eidos forge create` and the
// First Contact wizard's phase 3. Mid-flight failures (init-volume,
// container-create) reverse-undo any completed steps so the operator
// can retry cleanly without manual `docker volume rm` / `docker rm`
// housekeeping.
//
// CLAUDE.md "Single Call Path": container creation is a sanctioned
// bootstrap exception that does not flow through the daemon's
// methodTable. forge.upgrade (a sibling flow in this package) does
// flow through IPC; the asymmetry is acknowledged and tolerated
// until the broader unified-call-path migration.
//
// cfg threads the host gate config through so the create-container
// step can mount configured workspaces alongside /eidos. cfg=nil
// mounts only /eidos (the First Contact wizard takes this branch —
// a freshly-summoned mind-form has no workspace config yet).
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts, cfg *config.Config) error {
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
		image = DefaultImage()
	}

	steps := []Step{
		{
			Name: "ensure-image",
			// Skip the pull when the image is already present locally
			// (common in dev / staging where `make image` produced a
			// tag the registry doesn't host yet). When the image is
			// missing we still attempt the pull and surface its error.
			Do: func(ctx context.Context) error {
				exists, err := c.ImageExists(ctx, image)
				if err != nil {
					return fmt.Errorf("inspect image %s: %w", image, err)
				}
				if exists {
					return nil
				}
				if err := c.ImagePull(ctx, image, os.Stderr); err != nil {
					return fmt.Errorf("pull %s: %w", image, err)
				}
				return nil
			},
			// Undo intentionally nil: images are shared infrastructure.
		},
		{
			Name: "create-volume",
			Do: func(ctx context.Context) error {
				if err := c.VolumeCreate(ctx, vol); err != nil {
					return fmt.Errorf("create volume: %w", err)
				}
				return nil
			},
			Undo: func(ctx context.Context) error { return c.VolumeRemove(ctx, vol) },
		},
		{
			Name: "init-volume",
			Do: func(ctx context.Context) error {
				// Build the template tar to pipe into init-volume.
				// PrefabID, if set, switches the source to prefab/<id>/.
				pipeR, pipeW := io.Pipe()
				go func() {
					defer pipeW.Close()
					params := ontology.Params{
						Label:         o.Label,
						OwnerNpub:     o.Owner,
						OwnerLabel:    o.OwnerLabel,
						MindFormNpub:  o.MindFormNpub,
						HomeRelay:     o.Relay,
						CreatedDate:   time.Now().UTC().Format("2006-01-02"),
						SummoningBook: o.SummoningBook,
						RoleResearch:  o.RoleResearch,
					}
					var perr error
					if o.PrefabID != "" {
						perr = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
					} else {
						perr = ontology.TarStream(pipeW, params)
					}
					if perr != nil {
						_ = pipeW.CloseWithError(perr)
					}
				}()

				env := []string{
					"EIDOS_IN_CONTAINER=1",
					"EIDOS_FORGE_NAME=" + name,
					"EIDOS_FORGE_LABEL=" + o.Label,
					"EIDOS_FORGE_OWNER=" + o.Owner,
					"EIDOS_FORGE_RELAY=" + o.Relay,
					"EIDOS_FORGE_MODEL=" + o.Model,
					"EIDOS_FORGE_HEARTBEAT_INTERVAL=" + o.HeartbeatInterval,
				}
				if o.KeyHex != "" {
					env = append(env, "EIDOS_FORGE_KEY_HEX="+o.KeyHex)
				}
				res, err := c.RunInit(ctx, forgectl.RunInitOpts{
					Image: image,
					Mount: forgectl.Mount{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
					// init-volume runs as root so it can extract the
					// template tar, clone the bundle, and git-init the
					// parent ontology with full privileges. Its last
					// step chowns /eidos to 1000:1000; the persistent
					// container then starts as the image's USER (eidos).
					User:  "0:0",
					Env:   env,
					Cmd:   []string{"eidos", "forge", "init-volume"},
					Stdin: pipeR,
				})
				if err != nil {
					return fmt.Errorf("init-volume: %w (stderr: %s)", err, string(res.Stderr))
				}
				return nil
			},
			// init-volume writes into the volume; create-volume's Undo
			// (VolumeRemove) is what cleans up on failure here.
		},
		{
			Name: "create-container",
			// Create the persistent container in stopped state.
			// `forge start` is then a thin docker-start. Using the
			// image's default entrypoint (tini → entrypoint.sh →
			// eidos supervisor run); no Cmd / Entrypoint override.
			//
			// Mounts list is volume-only at create time. forge purge
			// removes the container + volume but leaves the host gate
			// config's [forge.<name>].workspaces block intact, so a
			// recycled name could silently inherit stale bind mounts
			// from a previous (now-deleted) mind-form. The safe
			// default is "fresh mind-forms start with /eidos only";
			// operators add workspaces explicitly via
			// `eidos forge workspace add` + `eidos forge restart`.
			// The cfg parameter is preserved on the signature for
			// future use (e.g., once forge purge cleans the config it
			// would be safe to honour cfg.Forge[name] here).
			Do: func(ctx context.Context) error {
				_ = cfg // reserved for a future safe create-time use; see comment above.
				if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
					Name:  cont,
					Image: image,
					Mounts: []forgectl.Mount{
						{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
					},
				}); err != nil {
					return fmt.Errorf("create container: %w", err)
				}
				return nil
			},
			Undo: func(ctx context.Context) error { return c.ContainerRemove(ctx, cont) },
		},
	}
	return RunSteps(ctx, steps)
}
