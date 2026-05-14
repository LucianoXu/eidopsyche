package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// UpgradeOpts carries the parameters for Upgrade. Fields map 1:1 to
// the JSON params of the forge.upgrade IPC method (see
// internal/ipc/protocol.go).
type UpgradeOpts struct {
	Name        string
	Image       string // empty → DefaultImage()
	WaitIdle    bool
	IdleTimeout time.Duration // zero → 10 minutes
	Grace       int           // zero → 10 seconds
	DryRun      bool

	// Workspaces is the list of bind mounts to re-apply when the new
	// container is created in step 6. Without this, upgrade would silently
	// drop any workspaces the operator had configured, forcing them to
	// run `forge restart` afterwards just to recover state that
	// conceptually never changed. The daemon handler populates this
	// from the host gate config; CLI tests / direct callers can leave it
	// nil to get the /eidos-only mount list (matching the pre-workspaces
	// behaviour).
	Workspaces []config.WorkspaceMount
}

// UpgradeResult is the structured output of Upgrade. Fields mirror
// ForgeUpgradeResult in internal/ipc/protocol.go.
type UpgradeResult struct {
	Name          string
	OldImage      string
	NewImage      string
	OldEidos      string
	NewEidos      string
	OldClaudeCode string
	NewClaudeCode string
	Skipped       bool
	SkippedReason string
	DryRun        bool
}

// Typed errors that the daemon handler maps to IPC error codes.
var (
	ErrUpgradeNotFound         = errors.New("forge upgrade: mind-form not found")
	ErrUpgradeImagePull        = errors.New("forge upgrade: image pull failed")
	ErrUpgradeImageInspect     = errors.New("forge upgrade: image inspect failed")
	ErrUpgradeContainerInspect = errors.New("forge upgrade: container inspect failed")
	ErrUpgradeIdleTimeout      = errors.New("forge upgrade: agentloop did not become idle within timeout")
	ErrUpgradeContainerStop    = errors.New("forge upgrade: container stop failed")
	ErrUpgradeContainerRemove  = errors.New("forge upgrade: container remove failed")
	ErrUpgradeContainerCreate  = errors.New("forge upgrade: container create failed")
	ErrUpgradeContainerStart   = errors.New("forge upgrade: container start failed")
	ErrUpgradeHealthTimeout    = errors.New("forge upgrade: container did not become healthy within 30s")
)

// HealthProbeTimeout caps how long Upgrade waits for the new
// container's IPC socket to come up before declaring health failure.
const HealthProbeTimeout = 30 * time.Second

// Upgrade swaps the mind-form's container to a new image while
// preserving the volume.
//
// Steps:
//  1. inspect current container's image ref + ID + LABELs.
//  2. ensure new image is local (pull if missing), read its LABELs + ID.
//     DryRun returns here.
//  3. wait-idle (opt-in).
//  4. stop (if running).
//  5. remove (if not absent).
//  6. create with new image, same volume.
//  7. start.
//  8. health-verify (30s budget).
//
// Step 3 aborts cleanly on timeout (no container mutation yet).
// Failures after step 5 leave the container absent but the volume
// intact — the operator re-runs Upgrade, which the idempotent re-entry
// in step 6 handles.
func Upgrade(ctx context.Context, c forgectl.Client, opts UpgradeOpts) (UpgradeResult, error) {
	if opts.Name == "" {
		return UpgradeResult{}, fmt.Errorf("Upgrade: empty name")
	}
	if opts.Image == "" {
		opts.Image = DefaultImage()
	}
	if opts.Grace == 0 {
		opts.Grace = 10
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 10 * time.Minute
	}

	cont := forgectl.ContainerName(opts.Name)

	// Preflight: refuse to upgrade a mind-form that doesn't exist at
	// all (no container AND no volume). The container may legitimately
	// be absent (idempotent re-entry after SIGINT'd previous run); the
	// volume must exist for upgrade to have anywhere to mount.
	volExists, err := c.VolumeExists(ctx, forgectl.VolumeName(opts.Name))
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("%w: volume check: %v", ErrUpgradeImageInspect, err)
	}
	if !volExists {
		return UpgradeResult{}, fmt.Errorf("%w: %s", ErrUpgradeNotFound, opts.Name)
	}

	res := UpgradeResult{
		Name:     opts.Name,
		NewImage: opts.Image,
		DryRun:   opts.DryRun,
	}

	// Step 1: inspect current container's image ref + ID + LABELs.
	oldImageID, oldRef, err := c.ContainerInspectImage(ctx, cont)
	if err != nil {
		return res, fmt.Errorf("%w: inspect current container: %v", ErrUpgradeImageInspect, err)
	}
	res.OldImage = oldRef
	if oldRef != "" {
		labels, lerr := c.ImageInspectLabels(ctx, oldRef)
		if lerr != nil {
			// Tolerate — old image may have been pruned from the local store.
			// Leave version fields empty; oldImageID is still valid from
			// container metadata, so the Skipped check below still works.
		} else {
			v := forgectl.VersionsFromLabels(labels)
			res.OldEidos = v.Eidos
			res.OldClaudeCode = v.ClaudeCode
		}
	}

	// Step 2: ensure new image is local, read its LABELs + ID.
	existsLocal, err := c.ImageExists(ctx, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeImageInspect, err)
	}
	if !existsLocal {
		if err := c.ImagePull(ctx, opts.Image, io.Discard); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeImagePull, err)
		}
	}
	newLabels, err := c.ImageInspectLabels(ctx, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: new image labels: %v", ErrUpgradeImageInspect, err)
	}
	v := forgectl.VersionsFromLabels(newLabels)
	res.NewEidos = v.Eidos
	res.NewClaudeCode = v.ClaudeCode

	newImageID, err := c.ImageInspectID(ctx, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: new image ID: %v", ErrUpgradeImageInspect, err)
	}

	// Skipped check: image IDs match — already running target image.
	if oldImageID != "" && newImageID == oldImageID {
		res.Skipped = true
		res.SkippedReason = fmt.Sprintf("already at %s", opts.Image)
		return res, nil
	}

	if opts.DryRun {
		return res, nil
	}

	// Step 3 (optional): wait-idle before any mutation.
	if opts.WaitIdle {
		if err := waitIdle(ctx, c, cont, opts.IdleTimeout); err != nil {
			return res, err
		}
	}

	// Step 4: stop if running.
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return res, fmt.Errorf("%w: inspect container state: %v", ErrUpgradeContainerInspect, err)
	}
	if state == "running" {
		if err := c.ContainerStop(ctx, cont, opts.Grace); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeContainerStop, err)
		}
	}

	// Step 5: remove if not absent (idempotent re-entry when container
	// was already removed by a previous interrupted run).
	if state != "absent" {
		if err := c.ContainerRemove(ctx, cont); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeContainerRemove, err)
		}
	}

	// Step 6: create with new image, preserving the /eidos volume and
	// any configured workspace bind mounts. Workspaces conceptually
	// belong to the mind-form across upgrades — re-applying them here
	// avoids surprising the operator with a "pending restart" diff
	// immediately after upgrade.
	mounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName(opts.Name), Target: "/eidos"},
	}
	for _, w := range opts.Workspaces {
		mounts = append(mounts, forgectl.Mount{
			Type:     forgectl.MountBind,
			Source:   w.HostPath,
			Target:   "/workspace/" + w.Name,
			ReadOnly: w.EffectiveMode() == "ro",
		})
	}
	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:   cont,
		Image:  opts.Image,
		Mounts: mounts,
	}); err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeContainerCreate, err)
	}

	// Step 7: start.
	if err := c.ContainerStart(ctx, cont); err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeContainerStart, err)
	}

	// Step 8: health-verify.
	if err := waitHealthy(ctx, c, cont, HealthProbeTimeout); err != nil {
		return res, err
	}

	return res, nil
}

// waitIdle polls the in-container runtime-state until the agentloop
// reports an idle phase, or until timeout. Returns ErrUpgradeIdleTimeout
// on timeout.
func waitIdle(ctx context.Context, c forgectl.Client, cont string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
		if err == nil && res.ExitCode == 0 && isIdlePhase(res.Stdout) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrUpgradeIdleTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// isIdlePhase returns true when the runtime-state JSON shows an
// agentloop phase that is safe to interrupt. Conservative: only
// "idle" / "sleeping" count as idle; "awake" / "in_turn" do not.
//
// Decodes a minimal struct rather than importing cmd/eidos/forge's
// full RuntimeState (which would be an upward import). The phase
// key is stable; if it ever changes, update here.
func isIdlePhase(jsonStdout []byte) bool {
	var rs struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(jsonStdout, &rs); err != nil {
		return false
	}
	return rs.Phase == "idle" || rs.Phase == "sleeping"
}

// waitHealthy polls the new container's gate IPC until it responds,
// or until timeout. Reuses the runtime-state probe.
func waitHealthy(ctx context.Context, c forgectl.Client, cont string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
		if err == nil && res.ExitCode == 0 && len(res.Stdout) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrUpgradeHealthTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
