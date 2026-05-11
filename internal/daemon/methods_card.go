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
