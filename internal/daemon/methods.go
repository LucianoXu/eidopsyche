package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yingtexu/eidopsyche/internal/card"
	"github.com/yingtexu/eidopsyche/internal/contacts"
	"github.com/yingtexu/eidopsyche/internal/identity"
	"github.com/yingtexu/eidopsyche/internal/inbox"
	"github.com/yingtexu/eidopsyche/internal/ipc"
	"github.com/yingtexu/eidopsyche/internal/nostr"
)

func init() {
	register("whoami", whoami)
	register("card.export", cardExport)
	register("card.parse", cardParse)
	register("contact.add", contactAdd)
	register("contact.list", contactList)
	register("contact.remove", contactRemove)
	register("relay.list", relayList)
	register("relay.add", relayAdd)
	register("relay.remove", relayRemove)
	register("send", sendMessage)
	register("inbox.list", inboxList)
	register("inbox.tail", inboxTail)
	register("outbox.list", outboxList)
	register("version", versionMethod)
	register("subscribe.refresh", subscribeRefresh)
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

// contactRemove removes a contact by npub.
func contactRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Npub string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(p.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
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

// sendMessage NIP-17 gift-wraps a message and publishes it to the recipient's
// relays plus our own. It also publishes a self-copy for archive purposes.
func sendMessage(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk := p.To
	if strings.HasPrefix(p.To, "npub1") {
		hex, err := identity.DecodeNpub(p.To)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
		}
		pk = hex
	}
	c, err := d.Repo.Get(ctx, pk)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: p.To}
	}

	wrapBob, rumorID, err := nostr.Wrap(d.Key.PrivateHex, pk, p.Content)
	if err != nil {
		return nil, internalErr(err)
	}
	wrapSelf, _, err := nostr.Wrap(d.Key.PrivateHex, d.Key.PublicHex, p.Content)
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
		Content:     p.Content,
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
func inboxList(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
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
	if p.From != "" && strings.HasPrefix(p.From, "npub1") {
		hex, err := identity.DecodeNpub(p.From)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
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
func outboxList(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
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
	if p.To != "" && strings.HasPrefix(p.To, "npub1") {
		hex, err := identity.DecodeNpub(p.To)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
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
