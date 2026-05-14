package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func init() {
	register("forge.workspace.add", forgeWorkspaceAdd)
}

type forgeWorkspaceAddParams struct {
	MindForm  string `json:"mindform"`
	Name      string `json:"name"`
	HostPath  string `json:"host_path"`
	Mode      string `json:"mode,omitempty"`
	NoWarnUID bool   `json:"no_warn_uid,omitempty"`
}

type forgeWorkspaceAddResult struct {
	PendingRestart bool     `json:"pending_restart"`
	Warnings       []string `json:"warnings,omitempty"`
}

func forgeWorkspaceAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceAddParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if err := forgectl.ValidateName(p.MindForm); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "mindform: " + err.Error()}
	}
	if err := config.ValidateWorkspaceName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if err := config.ValidateWorkspaceMode(p.Mode); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if !d.MindFormKnown(p.MindForm) {
		return nil, &ipc.Error{Code: ipc.ErrForgeNotFound, Message: fmt.Sprintf("mind-form %q not found", p.MindForm)}
	}
	if err := config.ValidateWorkspaceHostPath(p.HostPath); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}

	var warnings []string
	if w := config.DangerousHostPath(p.HostPath); w != "" {
		warnings = append(warnings, w)
	}
	effMode := p.Mode
	if effMode == "" {
		effMode = "rw"
	}
	if effMode == "rw" && !p.NoWarnUID {
		if uid, err := config.HostPathOwnerUID(p.HostPath); err == nil && uid != config.MindFormContainerUID {
			warnings = append(warnings, config.UIDMismatchWarning(p.HostPath, uid))
		}
	}

	var dupErr *ipc.Error
	merr := d.Mutate(ctx, "forge."+p.MindForm+".workspaces", config.HostCtx,
		func() (any, any, error) {
			cfg, err := config.Load(d.configPath())
			if err != nil {
				return nil, nil, err
			}
			if cfg.Forge == nil {
				cfg.Forge = map[string]config.ForgeMindForm{}
			}
			mf := cfg.Forge[p.MindForm]
			oldWs := append([]config.WorkspaceMount(nil), mf.Workspaces...)
			for _, w := range mf.Workspaces {
				if w.Name == p.Name {
					dupErr = &ipc.Error{Code: ipc.ErrWorkspaceExists,
						Message: fmt.Sprintf("workspace %q already exists on mind-form %q", p.Name, p.MindForm)}
					return nil, nil, dupErr
				}
			}
			mf.Workspaces = append(mf.Workspaces, config.WorkspaceMount{
				Name: p.Name, HostPath: p.HostPath, Mode: p.Mode,
			})
			cfg.Forge[p.MindForm] = mf
			if err := config.Save(d.configPath(), cfg); err != nil {
				return nil, nil, err
			}
			return oldWs, mf.Workspaces, nil
		})
	if dupErr != nil {
		return nil, dupErr
	}
	if merr != nil {
		return nil, asIPCError(merr)
	}
	return forgeWorkspaceAddResult{PendingRestart: true, Warnings: warnings}, nil
}

func init() {
	register("forge.workspace.remove", forgeWorkspaceRemove)
}

type forgeWorkspaceRemoveParams struct {
	MindForm string `json:"mindform"`
	Name     string `json:"name"`
}

func forgeWorkspaceRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceRemoveParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if err := forgectl.ValidateName(p.MindForm); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "mindform: " + err.Error()}
	}
	if err := config.ValidateWorkspaceName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	// No MindFormKnown gate here — removing workspace config is always
	// safe, and we must allow operators to clean up orphaned entries
	// left behind after `forge purge` (which today only removes the
	// container + volume, not the host gate config). Without this
	// carve-out a recycled name silently inherits stale mounts.

	var notFoundErr *ipc.Error
	merr := d.Mutate(ctx, "forge."+p.MindForm+".workspaces", config.HostCtx,
		func() (any, any, error) {
			cfg, err := config.Load(d.configPath())
			if err != nil {
				return nil, nil, err
			}
			mf := cfg.Forge[p.MindForm]
			oldWs := append([]config.WorkspaceMount(nil), mf.Workspaces...)
			filtered := mf.Workspaces[:0]
			found := false
			for _, w := range mf.Workspaces {
				if w.Name == p.Name {
					found = true
					continue
				}
				filtered = append(filtered, w)
			}
			if !found {
				notFoundErr = &ipc.Error{Code: ipc.ErrWorkspaceNotFound,
					Message: fmt.Sprintf("no workspace %q on mind-form %q", p.Name, p.MindForm)}
				return nil, nil, notFoundErr
			}
			mf.Workspaces = filtered
			if len(mf.Workspaces) == 0 {
				delete(cfg.Forge, p.MindForm)
			} else {
				cfg.Forge[p.MindForm] = mf
			}
			if err := config.Save(d.configPath(), cfg); err != nil {
				return nil, nil, err
			}
			return oldWs, mf.Workspaces, nil
		})
	if notFoundErr != nil {
		return nil, notFoundErr
	}
	if merr != nil {
		return nil, asIPCError(merr)
	}
	return forgeWorkspaceAddResult{PendingRestart: true}, nil
}

func init() {
	register("forge.workspace.list", forgeWorkspaceList)
}

type forgeWorkspaceListParams struct {
	MindForm string `json:"mindform"`
}

type forgeWorkspaceListEntry struct {
	Name     string `json:"name"`
	HostPath string `json:"host_path"`
	Mode     string `json:"mode"`
}

type forgeWorkspaceListResult struct {
	Desired        []forgeWorkspaceListEntry `json:"desired"`
	Actual         []forgeWorkspaceListEntry `json:"actual"`
	PendingRestart bool                      `json:"pending_restart"`
}

func forgeWorkspaceList(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p forgeWorkspaceListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidRequest, Message: "decode params: " + err.Error()}
	}
	if err := forgectl.ValidateName(p.MindForm); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "mindform: " + err.Error()}
	}
	// No MindFormKnown gate here — list must work on orphaned config
	// (post-purge) so the operator can see and clean up stale entries.
	// Both `desired` (config) and `actual` (docker inspect) are
	// independently best-effort below.

	desired := []forgeWorkspaceListEntry{}
	cfg, err := config.Load(d.configPath())
	if err == nil {
		for _, w := range cfg.Forge[p.MindForm].Workspaces {
			desired = append(desired, forgeWorkspaceListEntry{
				Name: w.Name, HostPath: w.HostPath, Mode: w.EffectiveMode(),
			})
		}
	}

	actual := []forgeWorkspaceListEntry{}
	if d.forgectlClient != nil {
		mounts, err := d.forgectlClient.ContainerInspectMounts(ctx, forgectl.ContainerName(p.MindForm))
		if err == nil {
			const prefix = "/workspace/"
			for _, m := range mounts {
				if m.Type != forgectl.MountBind {
					continue
				}
				if !strings.HasPrefix(m.Target, prefix) {
					continue
				}
				name := strings.TrimPrefix(m.Target, prefix)
				if name == "" || strings.Contains(name, "/") {
					continue
				}
				mode := "rw"
				if m.ReadOnly {
					mode = "ro"
				}
				actual = append(actual, forgeWorkspaceListEntry{
					Name: name, HostPath: m.Source, Mode: mode,
				})
			}
		}
	}

	return forgeWorkspaceListResult{
		Desired:        desired,
		Actual:         actual,
		PendingRestart: !workspaceListsEqual(desired, actual),
	}, nil
}

func workspaceListsEqual(a, b []forgeWorkspaceListEntry) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]forgeWorkspaceListEntry{}
	for _, e := range b {
		idx[e.Name] = e
	}
	for _, e := range a {
		other, ok := idx[e.Name]
		if !ok || other != e {
			return false
		}
	}
	return true
}
