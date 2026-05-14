package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forge"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func init() {
	register("forge.upgrade", forgeUpgrade)
}

// forgeUpgrade is the IPC handler for the forge.upgrade method.
// Validates params, invokes internal/forge.Upgrade, and maps typed
// errors to IPC codes.
//
// CLAUDE.md "Single Call Path": the CLI (cmd/eidos/forge/upgrade.go)
// is a thin wrapper that calls this method; future dashboard / MCP
// surfaces dispatch through internal/daemon.Call to the same handler.
// forge.create remains a bootstrap exception today — migration tracked
// under unified-call-path.
//
// TODO: A per-mindform serialization mutex would prevent two concurrent
// upgrade operations racing on the same container. Not in scope for this
// task: internal/forge.Upgrade is naturally idempotent (stop→remove→create→start
// is re-entrant at the Docker level), and concurrent upgrades for the
// same mind-form are an edge case with no real user impact at this stage.
// Add a keyed mutex (sync.Map[string]*sync.Mutex) here when the need arises.
func forgeUpgrade(ctx context.Context, _ *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p ipc.ForgeUpgradeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	if p.Name == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "name is required"}
	}
	if err := forgectl.ValidateName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "name: " + err.Error()}
	}
	if p.Grace < 0 {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "grace must be non-negative"}
	}

	var idleTimeout time.Duration
	if p.IdleTimeout != "" {
		dur, err := time.ParseDuration(p.IdleTimeout)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "idle_timeout: " + err.Error()}
		}
		idleTimeout = dur
	}

	client, err := forgectl.New()
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "forgectl: " + err.Error()}
	}

	opts := forge.UpgradeOpts{
		Name:        p.Name,
		Image:       p.Image,
		WaitIdle:    p.WaitIdle,
		IdleTimeout: idleTimeout,
		Grace:       p.Grace,
		DryRun:      p.DryRun,
	}

	res, err := forge.Upgrade(ctx, client, opts)
	if err != nil {
		return nil, mapUpgradeError(err)
	}

	return ipc.ForgeUpgradeResult{
		Name:          res.Name,
		OldImage:      res.OldImage,
		NewImage:      res.NewImage,
		OldEidos:      res.OldEidos,
		NewEidos:      res.NewEidos,
		OldClaudeCode: res.OldClaudeCode,
		NewClaudeCode: res.NewClaudeCode,
		Skipped:       res.Skipped,
		SkippedReason: res.SkippedReason,
		DryRun:        res.DryRun,
	}, nil
}

func mapUpgradeError(err error) *ipc.Error {
	switch {
	case errors.Is(err, forge.ErrUpgradeNotFound):
		return &ipc.Error{Code: ipc.ErrForgeNotFound, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeImagePull):
		return &ipc.Error{Code: ipc.ErrForgeImagePull, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeImageInspect):
		return &ipc.Error{Code: ipc.ErrForgeImageInspect, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerInspect):
		return &ipc.Error{Code: ipc.ErrForgeContainerInspect, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeIdleTimeout):
		return &ipc.Error{Code: ipc.ErrForgeIdleTimeout, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerStop):
		return &ipc.Error{Code: ipc.ErrForgeContainerStop, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerRemove):
		return &ipc.Error{Code: ipc.ErrForgeContainerRemove, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerCreate):
		return &ipc.Error{Code: ipc.ErrForgeContainerCreate, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerStart):
		return &ipc.Error{Code: ipc.ErrForgeContainerStart, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeHealthTimeout):
		return &ipc.Error{Code: ipc.ErrForgeHealthTimeout, Message: err.Error()}
	default:
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
}
