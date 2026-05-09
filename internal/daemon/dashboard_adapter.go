package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// dashboardAdapter wraps *Daemon to satisfy dashboard.DashboardDeps.
// Defined in the daemon package to avoid an import cycle (dashboard
// cannot import daemon).
type dashboardAdapter struct{ d *Daemon }

// NewDashboardAdapter exposes the adapter so daemon.Run can pass it to
// dashboard.Run. Returned as the interface so callers cannot reach into
// the daemon directly.
func NewDashboardAdapter(d *Daemon) dashboard.DashboardDeps { return dashboardAdapter{d: d} }

// adapter-shim: OwnPubkey() has no error return on dashboard.DashboardDeps;
// a whoami Call failure (e.g. transient meta read error) still needs to
// produce a usable string. The Key.PublicHex field is the in-memory
// canonical pubkey loaded at daemon start, so reading it is a safe
// last-resort fallback.
func (a dashboardAdapter) OwnPubkey() string {
	var w whoamiResult
	if err := a.d.Call(context.Background(), "whoami", nil, &w); err == nil && w.Pubkey != "" {
		return w.Pubkey
	}
	if a.d.Key != nil {
		return a.d.Key.PublicHex
	}
	return ""
}

func (a dashboardAdapter) OwnLabel(ctx context.Context) (string, error) {
	var w whoamiResult
	if err := a.d.Call(ctx, "whoami", nil, &w); err != nil {
		return "", err
	}
	return w.Label, nil
}

// whoamiResult mirrors the whoami IPC method's projection so the
// adapter can decode without an inline anonymous struct.
type whoamiResult struct {
	Pubkey     string              `json:"pubkey"`
	Npub       string              `json:"npub"`
	Label      string              `json:"label"`
	HomeRelays []map[string]string `json:"home_relays"`
}

func (a dashboardAdapter) ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error) {
	params := inboxListParams{From: from, Limit: limit}
	if since != nil {
		s := since.Unix()
		params.Since = &s
	}
	var out []inbox.Message
	if err := a.d.Call(context.Background(), "inbox.list", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a dashboardAdapter) ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error) {
	params := outboxListParams{To: to, Limit: limit}
	if since != nil {
		s := since.Unix()
		params.Since = &s
	}
	var out []inbox.Sent
	if err := a.d.Call(context.Background(), "outbox.list", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// inboxListParams / outboxListParams are typed mirrors of the inline
// anonymous structs in inboxList / outboxList. The IPC handlers accept
// the same field set; named types let the adapter avoid map[string]any.
type inboxListParams struct {
	Since *int64 `json:"since,omitempty"`
	From  string `json:"from,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type outboxListParams struct {
	Since *int64 `json:"since,omitempty"`
	To    string `json:"to,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (a dashboardAdapter) ListContacts(ctx context.Context) ([]*contacts.Contact, error) {
	var out []*contacts.Contact
	if err := a.d.Call(ctx, "contact.list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a dashboardAdapter) ListRelayHealth() []dashboard.RelayState {
	var snap []RelayHealth
	if err := a.d.Call(context.Background(), "relays.health", nil, &snap); err != nil {
		return nil
	}
	out := make([]dashboard.RelayState, 0, len(snap))
	for _, h := range snap {
		out = append(out, dashboard.RelayState{
			URL:         h.URL,
			Role:        h.Role,
			State:       h.State,
			LastError:   h.LastError,
			LastEventAt: h.LastEventAt,
		})
	}
	return out
}

// Send routes the dashboard's send-message request through the same
// IPC method handler the CLI uses. See SPEC.md "调用路径统一" and
// docs/superpowers/specs/2026-05-09-unified-call-path-design.md.
//
// All side effects (resolveTarget for npub→hex, contact-existence check
// with self-loopback exemption, two-phase outbox persistence,
// ipc.ErrNoRelaysReachable on full publish failure) live in the handler.
// The dashboard handler converts the returned *ipc.Error into a 502 + toast.
func (a dashboardAdapter) Send(ctx context.Context, toPubkey string, env envelope.Envelope) (string, error) {
	var result SendResult
	if err := a.d.Call(ctx, "send", SendParams{To: toPubkey, Envelope: &env}, &result); err != nil {
		return "", err
	}
	return result.EventID, nil
}

func (a dashboardAdapter) SubscribeEvents() (<-chan dashboard.Event, func()) {
	return a.d.subscribeDashboard()
}

// ── phase 1: identity + config ─────────────────────────────────────

func (a dashboardAdapter) SetOwnLabel(ctx context.Context, label string) error {
	return a.d.Call(ctx, "set-label", map[string]string{"label": label}, nil)
}

func (a dashboardAdapter) OwnCardURI(ctx context.Context) (string, error) {
	var out struct {
		URI string `json:"uri"`
	}
	if err := a.d.Call(ctx, "card.export", nil, &out); err != nil {
		return "", err
	}
	return out.URI, nil
}

// ConfigSnapshot routes through the IPC config.get handler so the
// adapter and CLI surface read the on-disk file by the exact same code
// path. See SPEC.md "调用路径统一".
func (a dashboardAdapter) ConfigSnapshot() (config.Config, error) {
	var cfg config.Config
	if err := a.d.Call(context.Background(), "config.get", nil, &cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// ConfigSet routes through the IPC config.set handler. The lock that
// serialises read-modify-write of config.toml lives on the handler now;
// before unification the lock guarded only the dashboard writer while
// the CLI wrote the file directly without taking it.
func (a dashboardAdapter) ConfigSet(ctx context.Context, path, value string) error {
	return a.d.Call(ctx, "config.set", ConfigSetParams{Path: path, Value: value}, nil)
}

// ── phase 2: contacts ──────────────────────────────────────────────

func (a dashboardAdapter) GetContact(ctx context.Context, pubkey string) (*contacts.Contact, error) {
	var c contacts.Contact
	if err := a.d.Call(ctx, "contact.get", map[string]string{"target": pubkey}, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// AddContact admits a contact described by a mindgate:// card via the
// IPC contact.add-from-card method. Upsert semantics live in the
// handler so the dashboard and any future surface share them.
func (a dashboardAdapter) AddContact(ctx context.Context, cardURI, labelOverride string) (*contacts.Contact, error) {
	var c contacts.Contact
	if err := a.d.Call(ctx, "contact.add-from-card",
		ContactAddFromCardParams{URI: cardURI, LabelOverride: labelOverride}, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (a dashboardAdapter) RemoveContact(ctx context.Context, pubkey string) error {
	return a.d.Call(ctx, "contact.remove", map[string]string{"npub": pubkey}, nil)
}

func (a dashboardAdapter) SetContactLabel(ctx context.Context, pubkey, label string) error {
	return a.d.Call(ctx, "contact.set-label", map[string]string{
		"target": pubkey,
		"label":  label,
	}, nil)
}

func (a dashboardAdapter) SetContactTier(ctx context.Context, pubkey string, tier contacts.Tier) error {
	return a.d.Call(ctx, "contact.set-tier", ContactSetTierParams{Target: pubkey, Tier: string(tier)}, nil)
}

// ── phase 3: invites ───────────────────────────────────────────────

func (a dashboardAdapter) ListInvites(ctx context.Context, status string) ([]*invitedb.Invite, error) {
	var out []*invitedb.Invite
	if err := a.d.Call(ctx, "invite.list", map[string]string{"status": status}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a dashboardAdapter) CreateInvite(ctx context.Context, opts dashboard.InviteCreateOpts) (*invitedb.Invite, string, error) {
	var out InviteCreateMethodResult
	err := a.d.Call(ctx, "invite.create", map[string]any{
		"single_use":      opts.SingleUse,
		"unlimited":       opts.Unlimited,
		"max_uses":        opts.MaxUses,
		"expires_seconds": opts.ExpiresSeconds,
		"issuer_label":    opts.IssuerLabel,
		"redeemer_label":  opts.RedeemerLabel,
	}, &out)
	if err != nil {
		return nil, "", err
	}
	return out.Invite, out.URI, nil
}

func (a dashboardAdapter) RevokeInvite(ctx context.Context, idPrefix string) (string, error) {
	var out struct {
		FullID string `json:"full_id"`
	}
	if err := a.d.Call(ctx, "invite.revoke", map[string]string{"id_prefix": idPrefix}, &out); err != nil {
		return "", translateInviteError(err)
	}
	return out.FullID, nil
}

func (a dashboardAdapter) RedeemInvite(ctx context.Context, token string) (dashboard.RedeemResult, error) {
	var out struct {
		IssuerNpub  string   `json:"issuer_npub"`
		IssuerRelay string   `json:"issuer_relay"`
		AcceptedBy  []string `json:"accepted_by"`
	}
	if err := a.d.Call(ctx, "invite.redeem", map[string]string{"token": token}, &out); err != nil {
		return dashboard.RedeemResult{}, err
	}
	return dashboard.RedeemResult{
		IssuerNpub:  out.IssuerNpub,
		IssuerRelay: out.IssuerRelay,
		AcceptedBy:  out.AcceptedBy,
	}, nil
}

func (a dashboardAdapter) ScanCard(ctx context.Context, cardURI string) (dashboard.ScanPreview, error) {
	var preview dashboard.ScanPreview
	if err := a.d.Call(ctx, "card.scan", map[string]string{"uri": cardURI}, &preview); err != nil {
		return dashboard.ScanPreview{}, err
	}
	return preview, nil
}

// ── phase 4: own relays ────────────────────────────────────────────

func (a dashboardAdapter) ListOwnRelays(ctx context.Context) ([]dashboard.OwnRelay, error) {
	var rows []OwnRelayRow
	if err := a.d.Call(ctx, "relay.list", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]dashboard.OwnRelay, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboard.OwnRelay{
			URL:     r.URL,
			Role:    r.Role,
			AddedAt: r.AddedAt,
		})
	}
	return out, nil
}

func (a dashboardAdapter) AddOwnRelay(ctx context.Context, rawURL, role string) error {
	err := a.d.Call(ctx, "relay.add", map[string]string{"url": rawURL, "role": role}, nil)
	return translateRelayError(err)
}

func (a dashboardAdapter) RemoveOwnRelay(ctx context.Context, rawURL string) error {
	err := a.d.Call(ctx, "relay.remove", map[string]string{"url": rawURL}, nil)
	return translateRelayError(err)
}

// translateInviteError maps the IPC handler's typed invite codes back
// onto the invitedb sentinels the dashboard handler errors.Is-checks.
// Without this, post-Phase-5 the handler's switch would fall through
// to the generic 502 path because the adapter returns an *ipc.Error
// rather than the wrapped invitedb sentinel that pre-Phase-5
// d.InviteRevoke / d.InviteRedeem returned directly.
func translateInviteError(err error) error {
	if err == nil {
		return nil
	}
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		return err
	}
	switch ipcErr.Code {
	case ipc.ErrInviteInvalidToken:
		return fmt.Errorf("%w: %s", invitedb.ErrNotFound, ipcErr.Message)
	case ipc.ErrInvitePrefixAmbiguous:
		return fmt.Errorf("%w: %s", invitedb.ErrPrefixAmbiguous, ipcErr.Message)
	}
	return err
}

// translateRelayError maps the IPC handler's typed codes back onto the
// dashboard sentinel set. The handler today returns INVALID_PARAMS for
// the whole sentinel range with the original message preserved; the
// adapter pattern-matches on the message so the dashboard's HTTP layer
// keeps its existing errors.Is checks. If a future refactor introduces
// dedicated IPC codes for each sentinel (RELAY_INVALID_URL etc.), this
// function should be updated to switch on Code instead of substring.
func translateRelayError(err error) error {
	if err == nil {
		return nil
	}
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		return err
	}
	msg := ipcErr.Message
	switch {
	case msg == errOwnRelayInvalidURL.Error():
		return fmt.Errorf("%w: %s", dashboard.ErrRelayInvalidURL, msg)
	case msg == errOwnRelayInvalidRole.Error():
		return fmt.Errorf("%w: %s", dashboard.ErrRelayInvalidRole, msg)
	case msg == errOwnRelayDuplicate.Error():
		return fmt.Errorf("%w: %s", dashboard.ErrRelayDuplicate, msg)
	case msg == errOwnRelayNotFound.Error():
		return fmt.Errorf("%w: %s", dashboard.ErrRelayNotFound, msg)
	case msg == errOwnRelayHomeRequired.Error():
		return fmt.Errorf("%w: %s", dashboard.ErrRelayHomeRequired, msg)
	}
	return err
}

// ── phase 5: service control ───────────────────────────────────────

func (a dashboardAdapter) Status() dashboard.ServiceStatus {
	var st dashboard.ServiceStatus
	// Status() has no error return on the dashboard interface, so a
	// JSON / handler failure surfaces as a zero-value snapshot rather
	// than crashing the request. Daemons in any callable state can
	// always satisfy service.status (it reads only in-memory fields),
	// so this fallback is essentially defensive.
	_ = a.d.Call(context.Background(), "service.status", nil, &st)
	return st
}

func (a dashboardAdapter) LifecycleRun(args []string) (string, error) {
	var out struct {
		JobID string `json:"job_id"`
	}
	err := a.d.Call(context.Background(), "lifecycle.run", LifecycleRunParams{Args: args}, &out)
	if err != nil {
		var ipcErr *ipc.Error
		if errors.As(err, &ipcErr) && ipcErr.Code == ipc.ErrLifecycleBusy {
			return "", dashboard.ErrLifecycleBusy
		}
		return "", err
	}
	return out.JobID, nil
}
