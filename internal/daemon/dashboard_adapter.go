package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
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

func (a dashboardAdapter) Send(ctx context.Context, toPubkey string, env envelope.Envelope) (string, error) {
	content, err := envelope.Encode(env)
	if err != nil {
		return "", err
	}
	wrapBob, _, err := nostr.Wrap(a.d.Key.PrivateHex, toPubkey, content)
	if err != nil {
		return "", err
	}
	wrapSelf, _, err := nostr.Wrap(a.d.Key.PrivateHex, a.d.Key.PublicHex, content)
	if err != nil {
		return "", err
	}
	a.d.recordSelfWrap(wrapSelf.ID)

	// Union: own_relays + recipient.Relays (when known) + fallbacks. Mirrors
	// the existing IPC sendMessage handler so dashboard sends behave the same.
	targets := map[string]struct{}{}
	if urls, err := a.d.ownRelayURLs(ctx); err == nil {
		for _, u := range urls {
			targets[u] = struct{}{}
		}
	}
	if c, err := a.d.Repo.Get(ctx, toPubkey); err == nil {
		for _, u := range c.Relays {
			targets[u] = struct{}{}
		}
	}
	for _, u := range a.d.Cfg.Publish.FallbackRelays {
		targets[u] = struct{}{}
	}
	urls := make([]string, 0, len(targets))
	for u := range targets {
		urls = append(urls, u)
	}

	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := a.d.Pool.Publish(publishCtx, urls, wrapBob)
	_ = a.d.Pool.Publish(publishCtx, urls, wrapSelf)

	accepted := []string{}
	for _, r := range res {
		if r.OK {
			accepted = append(accepted, r.Relay)
		}
	}
	if len(accepted) == 0 {
		return "", fmt.Errorf("no relay accepted publish on %d urls", len(urls))
	}
	now := time.Now().Unix()
	sent := inbox.Sent{
		EventID:     wrapBob.ID,
		SelfEventID: wrapSelf.ID,
		To:          toPubkey,
		Kind:        14,
		Content:     content,
		RumorAt:     now,
		SentAt:      now,
		AcceptedBy:  accepted,
		Final:       true,
	}
	if err := a.d.Box.AppendOutbox(sent); err != nil {
		// Match the IPC sendMessage handler's behaviour: persisting the
		// outbox row is part of the contract. If it fails, the publish
		// already succeeded but the local record is missing — surface
		// the error so the caller renders a failure rather than showing
		// a sent-bubble that vanishes on next reload.
		return "", fmt.Errorf("append outbox: %w", err)
	}
	// Intentionally do NOT emit dashboard.Event{Kind: "outbox.message"} here:
	// the dashboard's POST /thread/<pk>/send response already swaps the
	// rendered bubble into #thread-body. An SSE emit would race that swap
	// and the originating tab would render the same event twice with
	// identical data-event-id. Multi-tab outbox sync is out of scope for
	// v1; reload to see sends from a sibling tab.
	return wrapBob.ID, nil
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

// configPath is the canonical location of config.toml inside the gate
// state directory. Both ConfigSnapshot and ConfigSet route through this
// helper so they always read and write the same file.
func (a dashboardAdapter) configPath() string {
	return filepath.Join(a.d.StateDir, "config.toml")
}

func (a dashboardAdapter) ConfigSnapshot() (config.Config, error) {
	return config.Load(a.configPath())
}

func (a dashboardAdapter) ConfigSet(ctx context.Context, path, value string) error {
	key, ok := config.KeyByPath(path)
	if !ok {
		return fmt.Errorf("unknown config key: %s", path)
	}
	// Serialise read-modify-write so two concurrent rows-set calls
	// (e.g. operator clicking Set on two rows in quick succession, or
	// two open tabs writing different keys) cannot overwrite each
	// other's update. Validation runs inside the lock too so the
	// rejection of an invalid value reflects the on-disk state at
	// the moment of the attempt.
	a.d.configMu.Lock()
	defer a.d.configMu.Unlock()

	cfg, err := config.Load(a.configPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := key.Set(&cfg, value); err != nil {
		return err
	}
	if err := config.Save(a.configPath(), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "config.changed"})
	return nil
}

// ── phase 2: contacts ──────────────────────────────────────────────

func (a dashboardAdapter) GetContact(ctx context.Context, pubkey string) (*contacts.Contact, error) {
	return a.d.Repo.Get(ctx, pubkey)
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
		if c.Relay != "" && !contains(existing.Relays, c.Relay) {
			if err := a.d.Repo.AddRelay(ctx, pubHex, c.Relay); err != nil {
				return nil, fmt.Errorf("refresh relay: %w", err)
			}
		}
		a.d.emitDashEvent(dashboard.Event{Kind: "contact.relabeled"})
		saved, _ := a.d.Repo.Get(ctx, pubHex)
		return saved, nil
	}

	// New contact path.
	contact := contacts.Contact{
		Pubkey: pubHex,
		Label:  label,
		Tier:   contacts.TierFriend,
		Relays: []string{c.Relay},
	}
	if err := a.d.Repo.Add(ctx, contact); err != nil {
		return nil, err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "contact.added"})
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
	if err := a.d.Repo.SetTier(ctx, pubkey, tier); err != nil {
		return err
	}
	a.d.emitDashEvent(dashboard.Event{Kind: "contact.tier-changed"})
	return nil
}

func (a dashboardAdapter) ScanCard(ctx context.Context, cardURI string) (dashboard.ScanPreview, error) {
	c, err := card.Parse(strings.TrimSpace(cardURI))
	if err != nil {
		return dashboard.ScanPreview{}, fmt.Errorf("parse card: %w", err)
	}
	pubHex, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return dashboard.ScanPreview{}, fmt.Errorf("decode npub: %w", err)
	}
	already := false
	if existing, err := a.d.Repo.Get(ctx, pubHex); err == nil && existing != nil {
		already = true
	}
	return dashboard.ScanPreview{
		Pubkey:         pubHex,
		Npub:           c.Npub,
		Label:          c.Label,
		Relay:          c.Relay,
		AlreadyContact: already,
	}, nil
}
