package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/config"
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
