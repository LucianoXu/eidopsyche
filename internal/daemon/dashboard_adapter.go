package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
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

func (a dashboardAdapter) OwnPubkey() string { return a.d.Key.PublicHex }

func (a dashboardAdapter) OwnLabel(ctx context.Context) (string, error) {
	var v string
	row := a.d.DB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "label")
	if err := row.Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}

func (a dashboardAdapter) ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error) {
	return a.d.Box.ListInbox(since, from, limit)
}

func (a dashboardAdapter) ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error) {
	return a.d.Box.ListOutbox(since, to, limit)
}

func (a dashboardAdapter) ListContacts(ctx context.Context) ([]*contacts.Contact, error) {
	return a.d.Repo.List(ctx)
}

func (a dashboardAdapter) ListRelayHealth() []dashboard.RelayState {
	if a.d.relayHealth == nil {
		return nil
	}
	snap := a.d.relayHealth.snapshot()
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
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label must not be empty")
	}
	if err := a.d.DB.SetMeta(ctx, "label", label); err != nil {
		return err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "identity.label-changed"})
	return nil
}

func (a dashboardAdapter) OwnCardURI(ctx context.Context) (string, error) {
	label, _ := a.d.DB.GetMeta(ctx, "label")
	var homeRelay string
	if err := a.d.DB.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&homeRelay); err != nil {
		return "", fmt.Errorf("no home relay configured: %w", err)
	}
	c := card.Card{Npub: a.d.Key.Npub, Relay: homeRelay, Label: label}
	uri, err := c.URI()
	if err != nil {
		return "", err
	}
	return uri, nil
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

// AddContact admits a contact described by a mindgate:// card. If the
// contact's pubkey is already known, the call upserts: label is
// updated to whatever the operator typed (or the card's embedded
// label, in that order), and the card's relay hint is appended to
// contact_relays if not already present. The tier is preserved on
// upsert so a refresh doesn't accidentally widen trust.
func (a dashboardAdapter) AddContact(ctx context.Context, cardURI, labelOverride string) (*contacts.Contact, error) {
	c, err := card.Parse(strings.TrimSpace(cardURI))
	if err != nil {
		return nil, fmt.Errorf("parse card: %w", err)
	}
	pubHex, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return nil, fmt.Errorf("decode npub: %w", err)
	}
	label := strings.TrimSpace(labelOverride)
	if label == "" {
		label = strings.TrimSpace(c.Label)
	}
	if label == "" {
		return nil, fmt.Errorf("card has no label and none was provided")
	}

	// Refresh path: contact already exists — update label, ensure the
	// card's relay hint is present, leave tier alone.
	if existing, err := a.d.Repo.Get(ctx, pubHex); err == nil && existing != nil {
		if err := a.d.Repo.SetLabel(ctx, pubHex, label); err != nil {
			return nil, fmt.Errorf("refresh label: %w", err)
		}
		relayAdded := false
		if c.Relay != "" && !contains(existing.Relays, c.Relay) {
			if err := a.d.Repo.AddRelay(ctx, pubHex, c.Relay); err != nil {
				return nil, fmt.Errorf("refresh relay: %w", err)
			}
			relayAdded = true
		}
		a.d.emitDashEvent(dashboard.Event{Kind: "contact.relabeled"})
		// Kick the subscriber so the new relay enters the subscription
		// set immediately — otherwise the daemon stays bound to the
		// old set until restart and may miss inbound from the
		// just-refreshed peer.
		if relayAdded {
			a.d.Refresh()
		}
		saved, _ := a.d.Repo.Get(ctx, pubHex)
		return saved, nil
	}

	// New contact path. Only persist a relay hint if the card carried
	// one; an empty string would later end up in subscriptionURLs and
	// poison relay health with dial failures.
	contact := contacts.Contact{
		Pubkey: pubHex,
		Label:  label,
		Tier:   contacts.TierFriend,
	}
	if c.Relay != "" {
		contact.Relays = []string{c.Relay}
	}
	if err := a.d.Repo.Add(ctx, contact); err != nil {
		return nil, err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "contact.added"})
	// Same reason as the refresh branch: a fresh contact's relay
	// joins our subscription set; kick so we pick it up now.
	if c.Relay != "" {
		a.d.Refresh()
	}
	saved, _ := a.d.Repo.Get(ctx, pubHex)
	return saved, nil
}

// contains reports whether s contains x. Used for the relay-hint
// dedup in AddContact's refresh path.
func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func (a dashboardAdapter) RemoveContact(ctx context.Context, pubkey string) error {
	if err := a.d.Repo.Remove(ctx, pubkey); err != nil {
		return err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "contact.removed"})
	// Symmetrical with AddContact: the removed contact's relay hints
	// drop out of the union, so kick the subscriber to recompute and
	// release any connection that's no longer in the set. Otherwise
	// the daemon stays bound to the old set (and the relay-health
	// panel keeps reporting the now-orphaned URL) until restart.
	a.d.Refresh()
	return nil
}

func (a dashboardAdapter) SetContactLabel(ctx context.Context, pubkey, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label must not be empty")
	}
	if err := a.d.Repo.SetLabel(ctx, pubkey, label); err != nil {
		return err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "contact.relabeled"})
	return nil
}

func (a dashboardAdapter) SetContactTier(ctx context.Context, pubkey string, tier contacts.Tier) error {
	return a.d.Call(ctx, "contact.set-tier", ContactSetTierParams{Target: pubkey, Tier: string(tier)}, nil)
}

// ── phase 3: invites ───────────────────────────────────────────────

func (a dashboardAdapter) ListInvites(ctx context.Context, status string) ([]*invitedb.Invite, error) {
	return a.d.InviteList(ctx, status)
}

func (a dashboardAdapter) CreateInvite(ctx context.Context, opts dashboard.InviteCreateOpts) (*invitedb.Invite, string, error) {
	res, err := a.d.InviteCreate(ctx, InviteCreateOptions{
		SingleUse:      opts.SingleUse,
		Unlimited:      opts.Unlimited,
		MaxUses:        opts.MaxUses,
		ExpiresSeconds: opts.ExpiresSeconds,
		IssuerLabel:    opts.IssuerLabel,
		RedeemerLabel:  opts.RedeemerLabel,
	})
	if err != nil {
		return nil, "", err
	}
	// Re-read so the row reflects the post-insert canonical state
	// (status='active', uses=0). This avoids the renderer needing to
	// reconstruct the timestamps from InviteCreateResult.
	inv, err := a.d.Invites.Get(ctx, res.ID)
	if err != nil {
		return nil, "", fmt.Errorf("read back invite: %w", err)
	}
	return inv, res.URI, nil
}

func (a dashboardAdapter) RevokeInvite(ctx context.Context, idPrefix string) (string, error) {
	return a.d.InviteRevoke(ctx, idPrefix)
}

func (a dashboardAdapter) RedeemInvite(ctx context.Context, token string) (dashboard.RedeemResult, error) {
	res, err := a.d.InviteRedeem(ctx, token)
	if err != nil {
		return dashboard.RedeemResult{}, err
	}
	return dashboard.RedeemResult{
		IssuerNpub:  res.IssuerNpub,
		IssuerRelay: res.IssuerRelay,
		AcceptedBy:  res.AcceptedBy,
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
	rows, err := a.d.ListOwnRelays(ctx)
	if err != nil {
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

// AddOwnRelay forwards to the daemon helper and translates the daemon's
// internal sentinels into dashboard-facing ones so the handler doesn't
// have to grep error strings to map onto HTTP status codes.
func (a dashboardAdapter) AddOwnRelay(ctx context.Context, rawURL, role string) error {
	err := a.d.AddOwnRelay(ctx, rawURL, role)
	switch {
	case errors.Is(err, errOwnRelayInvalidURL):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayInvalidURL, err)
	case errors.Is(err, errOwnRelayInvalidRole):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayInvalidRole, err)
	case errors.Is(err, errOwnRelayDuplicate):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayDuplicate, err)
	}
	return err
}

func (a dashboardAdapter) RemoveOwnRelay(ctx context.Context, rawURL string) error {
	err := a.d.RemoveOwnRelay(ctx, rawURL)
	switch {
	case errors.Is(err, errOwnRelayInvalidURL):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayInvalidURL, err)
	case errors.Is(err, errOwnRelayNotFound):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayNotFound, err)
	case errors.Is(err, errOwnRelayHomeRequired):
		return fmt.Errorf("%w: %v", dashboard.ErrRelayHomeRequired, err)
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
