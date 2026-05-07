package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invite"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

func init() {
	register("whoami", whoami)
	register("set-label", setOwnLabel)
	register("card.export", cardExport)
	register("card.parse", cardParse)
	register("contact.add", contactAdd)
	register("contact.list", contactList)
	register("contact.remove", contactRemove)
	register("contact.set-label", contactSetLabel)
	register("relay.list", relayList)
	register("relay.add", relayAdd)
	register("relay.remove", relayRemove)
	register("send", sendMessage)
	register("inbox.list", inboxList)
	register("inbox.tail", inboxTail)
	register("outbox.list", outboxList)
	register("version", versionMethod)
	register("subscribe.refresh", subscribeRefresh)
	register("invite.create", inviteCreate)
	register("invite.list", inviteList)
	register("invite.revoke", inviteRevoke)
	register("invite.redeem", inviteRedeem)
}

// whoami returns our public identity plus configured home relays.
func whoami(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url, role FROM own_relays`)
	if err != nil {
		return nil, internalErr(err)
	}
	defer rows.Close()
	var relays []map[string]string
	for rows.Next() {
		var u, r string
		if err := rows.Scan(&u, &r); err != nil {
			return nil, internalErr(err)
		}
		relays = append(relays, map[string]string{"url": u, "role": r})
	}
	label, _ := d.DB.GetMeta(ctx, "label")
	return map[string]any{
		"pubkey":      d.Key.PublicHex,
		"npub":        d.Key.Npub,
		"label":       label,
		"home_relays": relays,
	}, nil
}

// setOwnLabel updates the label that this entity advertises (whoami / card).
// Empty labels are rejected — every identity must show a non-empty name.
func setOwnLabel(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Label string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "label must not be empty"}
	}
	if err := d.DB.SetMeta(ctx, "label", label); err != nil {
		return nil, internalErr(err)
	}
	return map[string]string{"label": label}, nil
}

// cardExport serialises our identity into a MindGate card URI.
func cardExport(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	label, _ := d.DB.GetMeta(ctx, "label")
	var homeRelay string
	if err := d.DB.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&homeRelay); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "no home relay configured"}
	}
	c := card.Card{Npub: d.Key.Npub, Relay: homeRelay, Label: label}
	uri, err := c.URI()
	if err != nil {
		return nil, internalErr(err)
	}
	return map[string]string{"uri": uri}, nil
}

// cardParse decodes a MindGate card URI and returns its fields.
func cardParse(_ context.Context, _ *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ URI string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	c, err := card.Parse(p.URI)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}
	return map[string]string{
		"npub":   c.Npub,
		"pubkey": pk,
		"relay":  c.Relay,
		"label":  c.Label,
	}, nil
}

// contactAdd adds a new contact from an npub + optional relays/label/tier.
func contactAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Npub   string   `json:"npub"`
		Relays []string `json:"relays"`
		Label  string   `json:"label"`
		Tier   string   `json:"tier"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(p.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}
	c := contacts.Contact{Pubkey: pk, Label: p.Label, Tier: contacts.Tier(p.Tier), Relays: p.Relays}
	if err := d.Repo.Add(ctx, c); err != nil {
		if errors.Is(err, contacts.ErrExists) {
			return nil, &ipc.Error{Code: ipc.ErrContactExists, Message: pk}
		}
		return nil, internalErr(err)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// contactList returns all contacts in the repo.
func contactList(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	all, err := d.Repo.List(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	out := make([]map[string]any, 0, len(all))
	for _, c := range all {
		npub, _ := identity.EncodeNpub(c.Pubkey)
		out = append(out, map[string]any{
			"npub":   npub,
			"pubkey": c.Pubkey,
			"label":  c.Label,
			"tier":   string(c.Tier),
			"relays": c.Relays,
		})
	}
	return out, nil
}

// contactRemove removes a contact by npub, hex pubkey, or label.
func contactRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Npub string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Npub)
	if ipcErr != nil {
		return nil, ipcErr
	}
	if err := d.Repo.Remove(ctx, pk); err != nil {
		if errors.Is(err, contacts.ErrNotFound) {
			return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
		}
		return nil, internalErr(err)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// contactSetLabel renames an existing contact. Target accepts npub, hex
// pubkey, or current label (subject to the usual ambiguity rules).
func contactSetLabel(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Target string
		Label  string
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "label must not be empty"}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Target)
	if ipcErr != nil {
		return nil, ipcErr
	}
	if err := d.Repo.SetLabel(ctx, pk, label); err != nil {
		if errors.Is(err, contacts.ErrNotFound) {
			return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
		}
		return nil, internalErr(err)
	}
	return map[string]string{"pubkey": pk, "label": label}, nil
}

// relayList returns own relays ordered by role then URL.
func relayList(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url, role FROM own_relays ORDER BY role, relay_url`)
	if err != nil {
		return nil, internalErr(err)
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var u, r string
		if err := rows.Scan(&u, &r); err != nil {
			return nil, internalErr(err)
		}
		out = append(out, map[string]string{"url": u, "role": r})
	}
	return out, nil
}

// relayAdd inserts a relay URL with the given role (home|fallback).
func relayAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		URL  string `json:"url"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if p.Role != "home" && p.Role != "fallback" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "role must be home or fallback"}
	}
	if _, err := d.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
		p.URL, p.Role, time.Now().Unix()); err != nil {
		return nil, internalErr(err)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// relayRemove deletes a relay URL from own_relays.
func relayRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ URL string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if _, err := d.DB.ExecContext(ctx, `DELETE FROM own_relays WHERE relay_url=?`, p.URL); err != nil {
		return nil, internalErr(err)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// sendMessage NIP-17 gift-wraps an envelope-v1 payload and publishes it to
// the recipient's relays plus our own. It also publishes a self-copy for
// archive purposes.
func sendMessage(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		To       string             `json:"to"`
		Envelope *envelope.Envelope `json:"envelope"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if p.Envelope == nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "envelope is required"}
	}
	content, err := envelope.Encode(*p.Envelope)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}

	pk, ipcErr := resolveTarget(ctx, d, p.To)
	if ipcErr != nil {
		return nil, ipcErr
	}
	c, err := d.Repo.Get(ctx, pk)
	if err != nil {
		// Self-loopback: sending to own pubkey is allowed even when not in
		// contacts. The dispatcher's authority rule (sender==self) handles
		// commands; chat-to-self lands in the operator's own inbox via the
		// self-copy publish.
		if pk != d.Key.PublicHex {
			return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: p.To}
		}
		c = &contacts.Contact{Pubkey: pk}
	}

	wrapBob, rumorID, err := nostr.Wrap(d.Key.PrivateHex, pk, content)
	if err != nil {
		return nil, internalErr(err)
	}
	wrapSelf, _, err := nostr.Wrap(d.Key.PrivateHex, d.Key.PublicHex, content)
	if err != nil {
		return nil, internalErr(err)
	}
	d.recordSelfWrap(wrapSelf.ID)

	now := time.Now().Unix()
	pre := inbox.Sent{
		EventID:     wrapBob.ID,
		SelfEventID: wrapSelf.ID,
		InnerID:     rumorID,
		To:          pk,
		Kind:        14,
		Content:     content,
		RumorAt:     now,
		SentAt:      now,
		AcceptedBy:  nil,
	}
	if err := d.Box.AppendOutbox(pre); err != nil {
		return nil, internalErr(err)
	}

	targets := map[string]struct{}{}
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url FROM own_relays`)
	if err != nil {
		return nil, internalErr(err)
	}
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		targets[u] = struct{}{}
	}
	rows.Close()
	for _, u := range c.Relays {
		targets[u] = struct{}{}
	}
	for _, u := range d.Cfg.Publish.FallbackRelays {
		targets[u] = struct{}{}
	}
	urls := make([]string, 0, len(targets))
	for u := range targets {
		urls = append(urls, u)
	}

	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resBob := d.Pool.Publish(publishCtx, urls, wrapBob)
	resSelf := d.Pool.Publish(publishCtx, urls, wrapSelf)
	_ = resSelf // self-copy publish is best-effort

	accepted := []string{}
	for _, r := range resBob {
		if r.OK {
			accepted = append(accepted, r.Relay)
		}
	}
	if len(accepted) == 0 {
		return nil, &ipc.Error{Code: ipc.ErrNoRelaysReachable,
			Message: fmt.Sprintf("publish failed on all %d relays", len(urls))}
	}
	final := pre
	final.AcceptedBy = accepted
	final.Final = true
	_ = d.Box.AppendOutbox(final)

	return map[string]any{
		"event_id":    wrapBob.ID,
		"accepted_by": accepted,
	}, nil
}

// inboxList returns inbox messages filtered by since/from/limit.
func inboxList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Since *int64 `json:"since"`
		From  string `json:"from"`
		Limit int    `json:"limit"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	var sincePtr *time.Time
	if p.Since != nil {
		t := time.Unix(*p.Since, 0)
		sincePtr = &t
	}
	if p.From != "" {
		hex, ipcErr := resolveTarget(ctx, d, p.From)
		if ipcErr != nil {
			return nil, ipcErr
		}
		p.From = hex
	}
	out, err := d.Box.ListInbox(sincePtr, p.From, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	return out, nil
}

// inboxTail subscribes the connection to live inbox push events.
func inboxTail(ctx context.Context, d *Daemon, conn *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	d.addSubscriber(conn)
	return map[string]bool{"subscribed": true}, nil
}

// outboxList returns sent messages filtered by since/to/limit.
func outboxList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Since *int64 `json:"since"`
		To    string `json:"to"`
		Limit int    `json:"limit"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	var sincePtr *time.Time
	if p.Since != nil {
		t := time.Unix(*p.Since, 0)
		sincePtr = &t
	}
	if p.To != "" {
		hex, ipcErr := resolveTarget(ctx, d, p.To)
		if ipcErr != nil {
			return nil, ipcErr
		}
		p.To = hex
	}
	out, err := d.Box.ListOutbox(sincePtr, p.To, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	return out, nil
}

// versionMethod returns the daemon and schema versions.
func versionMethod(_ context.Context, _ *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	return map[string]any{
		"daemon_version": "0.0.1",
		"schema_version": 1,
	}, nil
}

// subscribeRefresh asks the daemon to recompute its relay set and reattach.
func subscribeRefresh(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// internalErr wraps a Go error into an IPC internal error response.
func internalErr(err error) *ipc.Error {
	return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
}

// resolveTarget converts a user-supplied target string (npub / hex / label)
// to a hex pubkey, returning an *ipc.Error suitable for direct propagation.
func resolveTarget(ctx context.Context, d *Daemon, input string) (string, *ipc.Error) {
	if input == "" {
		return "", &ipc.Error{Code: ipc.ErrInvalidParams, Message: "empty target"}
	}
	if strings.HasPrefix(input, "npub1") {
		hex, err := identity.DecodeNpub(input)
		if err != nil {
			return "", &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
		}
		return hex, nil
	}
	if isHex64(input) {
		return input, nil
	}
	c, err := d.Repo.GetByLabel(ctx, input)
	if err != nil {
		var amb *contacts.AmbiguousLabelError
		if errors.As(err, &amb) {
			npubs := make([]string, 0, len(amb.Pubkeys))
			for _, pk := range amb.Pubkeys {
				if np, e := identity.EncodeNpub(pk); e == nil {
					npubs = append(npubs, np)
				} else {
					npubs = append(npubs, pk)
				}
			}
			return "", &ipc.Error{
				Code:    ipc.ErrLabelAmbiguous,
				Message: fmt.Sprintf("multiple contacts share label %q: %s; use npub or hex instead", input, strings.Join(npubs, ", ")),
			}
		}
		if errors.Is(err, contacts.ErrNotFound) {
			return "", &ipc.Error{
				Code:    ipc.ErrContactNotFound,
				Message: fmt.Sprintf("no contact with label %q (also tried as npub/hex)", input),
			}
		}
		return "", internalErr(err)
	}
	return c.Pubkey, nil
}

// inviteCreate creates a new invite token.
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

	maxUses := p.MaxUses
	if p.Unlimited {
		maxUses = 0
	} else if maxUses == 0 && !p.Unlimited {
		// Default to single-use when no explicit max_uses or unlimited.
		maxUses = 1
	}
	if p.SingleUse {
		maxUses = 1
	}

	var expiresAt int64
	switch {
	case p.ExpiresSeconds < 0:
		expiresAt = 0 // no expiry
	case p.ExpiresSeconds == 0:
		expiresAt = time.Now().Add(7 * 24 * time.Hour).Unix() // default 7d
	default:
		expiresAt = time.Now().Add(time.Duration(p.ExpiresSeconds) * time.Second).Unix()
	}

	issuerLabel := p.IssuerLabel
	if issuerLabel == "" {
		issuerLabel, _ = d.DB.GetMeta(ctx, "label")
	}

	var homeRelay string
	_ = d.DB.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&homeRelay)

	id, err := invite.RandomID()
	if err != nil {
		return nil, internalErr(err)
	}

	payload := invite.Payload{
		V:                 1,
		IssuerNpub:        d.Key.Npub,
		IssuerRelay:       homeRelay,
		IssuerLabelHint:   issuerLabel,
		RedeemerLabelHint: p.RedeemerLabel,
		ID:                id,
		ExpiresAt:         expiresAt,
		MaxUses:           maxUses,
	}
	if err := payload.Sign(d.Key.PrivateHex); err != nil {
		return nil, internalErr(err)
	}

	uri, err := payload.Encode()
	if err != nil {
		return nil, internalErr(err)
	}

	inv := invitedb.Invite{
		ID:            id,
		CreatedAt:     time.Now(),
		MaxUses:       maxUses,
		IssuerLabel:   issuerLabel,
		RedeemerLabel: p.RedeemerLabel,
	}
	if expiresAt != 0 {
		inv.ExpiresAt = time.Unix(expiresAt, 0)
	}
	if err := d.Invites.Insert(ctx, inv); err != nil {
		return nil, internalErr(err)
	}

	return map[string]any{
		"id":         id,
		"uri":        uri,
		"expires_at": expiresAt,
		"max_uses":   maxUses,
	}, nil
}

// inviteList returns invites filtered by status.
func inviteList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Status string `json:"status"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	invites, err := d.Invites.List(ctx, p.Status)
	if err != nil {
		return nil, internalErr(err)
	}
	out := make([]map[string]any, 0, len(invites))
	for _, inv := range invites {
		var expiresAt int64
		if !inv.ExpiresAt.IsZero() {
			expiresAt = inv.ExpiresAt.Unix()
		}
		out = append(out, map[string]any{
			"id":             inv.ID,
			"created_at":     inv.CreatedAt.Unix(),
			"expires_at":     expiresAt,
			"max_uses":       inv.MaxUses,
			"uses":           inv.Uses,
			"status":         string(inv.Status),
			"issuer_label":   inv.IssuerLabel,
			"redeemer_label": inv.RedeemerLabel,
		})
	}
	return out, nil
}

// inviteRevoke revokes an invite by id prefix.
func inviteRevoke(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		IDPrefix string `json:"id_prefix"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if p.IDPrefix == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "id_prefix required"}
	}
	inv, err := d.Invites.FindByPrefix(ctx, p.IDPrefix)
	if err != nil {
		if errors.Is(err, invitedb.ErrNotFound) {
			return nil, &ipc.Error{Code: ipc.ErrInviteInvalidToken, Message: "invite not found"}
		}
		if errors.Is(err, invitedb.ErrPrefixAmbiguous) {
			return nil, &ipc.Error{Code: ipc.ErrInvitePrefixAmbiguous, Message: "prefix matches multiple invites"}
		}
		return nil, internalErr(err)
	}
	if err := d.Invites.MarkRevoked(ctx, inv.ID); err != nil {
		return nil, internalErr(err)
	}
	return map[string]any{
		"ok":      true,
		"full_id": inv.ID,
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
	payload, err := invite.Decode(p.Token)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInviteInvalidToken, Message: err.Error()}
	}
	if err := payload.Verify(); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInviteInvalidToken, Message: err.Error()}
	}
	if payload.ExpiresAt != 0 && time.Now().Unix() >= payload.ExpiresAt {
		return nil, &ipc.Error{Code: ipc.ErrInviteExpired, Message: "invite has expired"}
	}

	issuerHex, err := identity.DecodeNpub(payload.IssuerNpub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}

	// Add issuer as contact (idempotent).
	contact := contacts.Contact{
		Pubkey: issuerHex,
		Label:  payload.IssuerLabelHint,
		Tier:   contacts.TierFriend,
		Relays: []string{payload.IssuerRelay},
	}
	if addErr := d.Repo.Add(ctx, contact); addErr != nil && !errors.Is(addErr, contacts.ErrExists) {
		return nil, internalErr(addErr)
	}

	// Get our own home relay for the redemption message.
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
		return nil, internalErr(err)
	}

	// Self-copy (dedupe suppression on echo).
	selfWrap, _, _ := nostr.WrapKind(d.Key.PrivateHex, d.Key.PublicHex, string(rb), 25001)
	d.recordSelfWrap(selfWrap.ID)

	// Collect publish targets: issuer relay + own relays + fallbacks.
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
		return nil, &ipc.Error{Code: ipc.ErrNoRelaysReachable, Message: "issuer relay unreachable"}
	}

	d.Refresh()

	return map[string]any{
		"issuer_npub":  payload.IssuerNpub,
		"issuer_relay": payload.IssuerRelay,
		"accepted_by":  accepted,
	}, nil
}

// isHex64 returns true if s is exactly 64 lowercase hexadecimal characters.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
