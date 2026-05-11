package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

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

// cardScan parses a mindgate:// card URI and reports whether the
// embedded npub is already in the contacts list. Strict superset of
// card.parse — that older method stays for callers that don't need the
// AlreadyContact field; new callers should prefer card.scan.
func cardScan(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ URI string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	c, err := card.Parse(strings.TrimSpace(p.URI))
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrCardInvalid, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}
	already := false
	if existing, err := d.Repo.Get(ctx, pk); err == nil && existing != nil {
		already = true
	}
	return dashboard.ScanPreview{
		Pubkey:         pk,
		Npub:           c.Npub,
		Label:          c.Label,
		Relay:          c.Relay,
		AlreadyContact: already,
	}, nil
}
