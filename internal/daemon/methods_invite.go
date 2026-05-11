package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// InviteCreateMethodResult bundles the persisted invite row with the
// shareable URI so callers (CLI, dashboard) get both in one round trip
// without needing a follow-up invite.list to re-fetch the canonical row.
type InviteCreateMethodResult struct {
	Invite *invitedb.Invite `json:"invite"`
	URI    string           `json:"uri"`
}

// inviteCreate creates a new invite token and returns the canonical
// invite row + the shareable URI. The dashboard adapter's previous
// post-create re-read is no longer needed.
func inviteCreate(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		SingleUse      bool   `json:"single_use"`
		MaxUses        int    `json:"max_uses"`
		ExpiresSeconds int64  `json:"expires_seconds"`
		IssuerLabel    string `json:"issuer_label"`
		RedeemerLabel  string `json:"redeemer_label"`
		Unlimited      bool   `json:"unlimited"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	var result InviteCreateMethodResult
	merr := d.Mutate(ctx, "invites", config.BothCtx,
		func() (any, any, error) {
			res, err := d.InviteCreate(ctx, InviteCreateOptions{
				SingleUse:      p.SingleUse,
				Unlimited:      p.Unlimited,
				MaxUses:        p.MaxUses,
				ExpiresSeconds: p.ExpiresSeconds,
				IssuerLabel:    p.IssuerLabel,
				RedeemerLabel:  p.RedeemerLabel,
			})
			if err != nil {
				return nil, nil, err
			}
			inv, err := d.Invites.Get(ctx, res.ID)
			if err != nil {
				return nil, nil, err
			}
			result = InviteCreateMethodResult{Invite: inv, URI: res.URI}
			return nil, inv, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	return result, nil
}

// inviteList returns invites filtered by status. The result is the
// typed *invitedb.Invite slice; callers needing Unix-second timestamps
// derive them from the time.Time fields. Pre-Phase-5 the handler
// returned a flattened map[string]any — switching to the typed shape
// removes the divergence with the dashboard adapter's projection.
func inviteList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Status string `json:"status"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	invites, err := d.InviteList(ctx, p.Status)
	if err != nil {
		return nil, internalErr(err)
	}
	return invites, nil
}

// inviteRevoke revokes an invite by id prefix. Routes through
// daemon.Mutate; emits state.changed for the invite.
func inviteRevoke(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		IDPrefix string `json:"id_prefix"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	var fullID string
	merr := d.Mutate(ctx, "invites."+p.IDPrefix, config.BothCtx,
		func() (any, any, error) {
			id, err := d.InviteRevoke(ctx, p.IDPrefix)
			if err != nil {
				switch {
				case errors.Is(err, errInviteIDPrefixRequired):
					return nil, nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
				case errors.Is(err, invitedb.ErrNotFound):
					return nil, nil, &ipc.Error{Code: ipc.ErrInviteInvalidToken, Message: "invite not found"}
				case errors.Is(err, invitedb.ErrPrefixAmbiguous):
					return nil, nil, &ipc.Error{Code: ipc.ErrInvitePrefixAmbiguous, Message: "prefix matches multiple invites"}
				}
				return nil, nil, err
			}
			fullID = id
			return id, nil, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	return map[string]any{
		"ok":      true,
		"full_id": fullID,
	}, nil
}

// inviteRedeem redeems an invite token: verifies it, adds the issuer as a
// contact, publishes a kind:25001 gift wrap to the issuer's relay.
func inviteRedeem(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	res, err := d.InviteRedeem(ctx, p.Token)
	if err != nil {
		switch {
		case errors.Is(err, errInviteInvalidToken):
			return nil, &ipc.Error{Code: ipc.ErrInviteInvalidToken, Message: err.Error()}
		case errors.Is(err, errInviteExpired):
			return nil, &ipc.Error{Code: ipc.ErrInviteExpired, Message: "invite has expired"}
		case errors.Is(err, errNoRelaysReachable):
			return nil, &ipc.Error{Code: ipc.ErrNoRelaysReachable, Message: "issuer relay unreachable"}
		}
		// identity.DecodeNpub failures arrive wrapped in fmt.Errorf — map
		// them onto the legacy "invalid npub" code so existing callers
		// (and the dashboard error renderer) keep their semantics.
		if strings.Contains(err.Error(), "decode issuer npub") {
			return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
		}
		return nil, internalErr(err)
	}
	return map[string]any{
		"issuer_npub":  res.IssuerNpub,
		"issuer_relay": res.IssuerRelay,
		"accepted_by":  res.AcceptedBy,
	}, nil
}
