package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/invite"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

// InviteCreateOptions is the typed input shared by the IPC and dashboard
// surfaces. Negative ExpiresSeconds means "no expiry"; zero means default
// (7d). Unlimited overrides MaxUses; SingleUse forces MaxUses=1.
type InviteCreateOptions struct {
	SingleUse      bool
	Unlimited      bool
	MaxUses        int
	ExpiresSeconds int64
	IssuerLabel    string
	RedeemerLabel  string
}

// InviteCreateResult is the typed result of InviteCreate. ExpiresAt is a
// Unix timestamp (0 means no expiry) — kept as int64 to match the IPC
// wire shape so tests can assert against either surface uniformly.
type InviteCreateResult struct {
	ID        string
	URI       string
	ExpiresAt int64
	MaxUses   int
}

// InviteRedeemResult is the typed result of InviteRedeem.
type InviteRedeemResult struct {
	IssuerNpub  string
	IssuerRelay string
	AcceptedBy  []string
}

// errInviteNotFound, errInvitePrefixAmbiguous, errInviteInvalidToken,
// errInviteExpired, errInvalidNpub, errNoRelaysReachable map onto the
// existing IPC error codes. Callers from the IPC dispatch path
// translate them; callers from the dashboard adapter surface them
// directly to the operator. Kept distinct from the invitedb sentinel
// errors so the dashboard side can render a clean message without
// pulling in invitedb's vocabulary.
var (
	errInviteInvalidToken = errors.New("invite: invalid token")
	errInviteExpired      = errors.New("invite: expired")
	errNoRelaysReachable  = errors.New("invite: no relay accepted publish")
)

// InviteCreate signs and persists a new invite, returning the URI for
// the operator to share. Emits dashboard.invite.created on success so
// every open dashboard refreshes its list.
func (d *Daemon) InviteCreate(ctx context.Context, opts InviteCreateOptions) (*InviteCreateResult, error) {
	maxUses := opts.MaxUses
	switch {
	case opts.Unlimited:
		maxUses = 0
	case opts.SingleUse:
		maxUses = 1
	case maxUses == 0:
		maxUses = 1
	}

	var expiresAt int64
	switch {
	case opts.ExpiresSeconds < 0:
		expiresAt = 0
	case opts.ExpiresSeconds == 0:
		expiresAt = time.Now().Add(7 * 24 * time.Hour).Unix()
	default:
		expiresAt = time.Now().Add(time.Duration(opts.ExpiresSeconds) * time.Second).Unix()
	}

	issuerLabel := opts.IssuerLabel
	if issuerLabel == "" {
		issuerLabel, _ = d.DB.GetMeta(ctx, "label")
	}

	var homeRelay string
	_ = d.DB.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&homeRelay)

	id, err := invite.RandomID()
	if err != nil {
		return nil, err
	}

	payload := invite.Payload{
		V:                 1,
		IssuerNpub:        d.Key.Npub,
		IssuerRelay:       homeRelay,
		IssuerLabelHint:   issuerLabel,
		RedeemerLabelHint: opts.RedeemerLabel,
		ID:                id,
		ExpiresAt:         expiresAt,
		MaxUses:           maxUses,
	}
	if err := payload.Sign(d.Key.PrivateHex); err != nil {
		return nil, fmt.Errorf("sign invite: %w", err)
	}

	uri, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode invite: %w", err)
	}

	row := invitedb.Invite{
		ID:            id,
		CreatedAt:     time.Now(),
		MaxUses:       maxUses,
		IssuerLabel:   issuerLabel,
		RedeemerLabel: opts.RedeemerLabel,
	}
	if expiresAt != 0 {
		row.ExpiresAt = time.Unix(expiresAt, 0)
	}
	if err := d.Invites.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("persist invite: %w", err)
	}

	d.emitDashEvent(dashboard.Event{Kind: "invite.created"})

	return &InviteCreateResult{
		ID:        id,
		URI:       uri,
		ExpiresAt: expiresAt,
		MaxUses:   maxUses,
	}, nil
}

// InviteList returns invites filtered by status. Pass "" for all.
func (d *Daemon) InviteList(ctx context.Context, status string) ([]*invitedb.Invite, error) {
	return d.Invites.List(ctx, status)
}

// InviteRevoke marks an invite as revoked, looked up by ID prefix.
// Returns the full ID on success. Emits dashboard.invite.revoked.
func (d *Daemon) InviteRevoke(ctx context.Context, idPrefix string) (string, error) {
	if idPrefix == "" {
		return "", fmt.Errorf("id_prefix required")
	}
	inv, err := d.Invites.FindByPrefix(ctx, idPrefix)
	if err != nil {
		return "", err
	}
	if err := d.Invites.MarkRevoked(ctx, inv.ID); err != nil {
		return "", err
	}
	d.emitDashEvent(dashboard.Event{Kind: "invite.revoked"})
	return inv.ID, nil
}

// InviteRedeem verifies an invite token, adds the issuer as a contact,
// and publishes a kind:25001 redemption gift wrap. Emits invite.redeemed
// AND contact.added so every open dashboard refreshes.
func (d *Daemon) InviteRedeem(ctx context.Context, token string) (*InviteRedeemResult, error) {
	payload, err := invite.Decode(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInviteInvalidToken, err)
	}
	if err := payload.Verify(); err != nil {
		return nil, fmt.Errorf("%w: %v", errInviteInvalidToken, err)
	}
	if payload.ExpiresAt != 0 && time.Now().Unix() >= payload.ExpiresAt {
		return nil, errInviteExpired
	}

	issuerHex, err := identity.DecodeNpub(payload.IssuerNpub)
	if err != nil {
		return nil, fmt.Errorf("decode issuer npub: %w", err)
	}

	contact := contacts.Contact{
		Pubkey: issuerHex,
		Label:  payload.IssuerLabelHint,
		Tier:   contacts.TierFriend,
		Relays: []string{payload.IssuerRelay},
	}
	if addErr := d.Repo.Add(ctx, contact); addErr != nil && !errors.Is(addErr, contacts.ErrExists) {
		return nil, fmt.Errorf("add issuer contact: %w", addErr)
	}

	var ownHomeRelay string
	_ = d.DB.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&ownHomeRelay)

	rumorContent := map[string]any{
		"v":                   1,
		"invite_id":           payload.ID,
		"redeemer_relay":      ownHomeRelay,
		"redeemer_label_hint": payload.RedeemerLabelHint,
	}
	rb, _ := json.Marshal(rumorContent)

	wrap, _, err := nostr.WrapKind(d.Key.PrivateHex, issuerHex, string(rb), 25001)
	if err != nil {
		return nil, fmt.Errorf("wrap redemption: %w", err)
	}
	selfWrap, _, _ := nostr.WrapKind(d.Key.PrivateHex, d.Key.PublicHex, string(rb), 25001)
	d.recordSelfWrap(selfWrap.ID)

	targets := map[string]struct{}{payload.IssuerRelay: {}}
	rows, _ := d.DB.QueryContext(ctx, `SELECT relay_url FROM own_relays`)
	if rows != nil {
		for rows.Next() {
			var u string
			_ = rows.Scan(&u)
			targets[u] = struct{}{}
		}
		rows.Close()
	}
	for _, u := range d.Cfg.Publish.FallbackRelays {
		targets[u] = struct{}{}
	}
	urls := make([]string, 0, len(targets))
	for u := range targets {
		urls = append(urls, u)
	}

	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := d.Pool.Publish(pubCtx, urls, wrap)
	_ = d.Pool.Publish(pubCtx, urls, selfWrap)

	accepted := []string{}
	for _, r := range res {
		if r.OK {
			accepted = append(accepted, r.Relay)
		}
	}
	if len(accepted) == 0 {
		return nil, errNoRelaysReachable
	}

	d.Refresh()
	d.emitDashEvent(dashboard.Event{Kind: "invite.redeemed"})
	d.emitDashEvent(dashboard.Event{Kind: "contact.added"})

	return &InviteRedeemResult{
		IssuerNpub:  payload.IssuerNpub,
		IssuerRelay: payload.IssuerRelay,
		AcceptedBy:  accepted,
	}, nil
}
