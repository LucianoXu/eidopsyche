// Package dashboard implements the local web dashboard for MindGate.
// See docs/superpowers/specs/2026-05-07-dashboard-design.md.
package dashboard

import (
	"context"
	"errors"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
)

// DashboardDeps is the narrow surface the HTTP handlers consume from the
// daemon. The interface is satisfied by an adapter on *daemon.Daemon
// (see internal/daemon/dashboard_adapter.go); handler tests use a stub.
type DashboardDeps interface {
	OwnPubkey() string
	OwnLabel(ctx context.Context) (string, error)

	ListInbox(since *time.Time, from string, limit int) ([]inbox.Message, error)
	ListOutbox(since *time.Time, to string, limit int) ([]inbox.Sent, error)
	ListContacts(ctx context.Context) ([]*contacts.Contact, error)
	ListRelayHealth() []RelayState

	// Send wraps text in a v1 chat envelope and publishes via the same
	// path as the existing IPC `send` method. Returns the wrap event_id
	// on success, or an error if no relay accepts (caller renders the
	// error in the compose pane).
	Send(ctx context.Context, toPubkey string, env envelope.Envelope) (eventID string, err error)

	// SubscribeEvents returns a channel of Event values for the SSE hub
	// to fan out. cancel must be called to release the slot.
	SubscribeEvents() (ch <-chan Event, cancel func())

	// ── phase 1: identity + config ────────────────────────────────────
	//
	// SetOwnLabel writes the new label to the daemon's meta store. An
	// empty (post-trim) label is rejected to keep every identity's card
	// non-empty. On success, the daemon emits identity.label-changed so
	// other open dashboards refresh.
	SetOwnLabel(ctx context.Context, label string) error

	// OwnCardURI returns the mindgate://npub@ws…/?label=… string the
	// operator hands to peers. Implementation reads the identity meta
	// row and the home relay row, identical to the IPC card.export
	// handler.
	OwnCardURI(ctx context.Context) (string, error)

	// ConfigSnapshot returns the parsed contents of config.toml at the
	// time of the call. Combined with config.KeyList() the dashboard
	// renders a row per scalar key.
	ConfigSnapshot() (config.Config, error)

	// ConfigSet validates value against the config.Key registry, writes
	// it to config.toml, and emits config.changed. Returns a wrapped
	// validation error suitable for inline rendering when the value is
	// rejected. A successful Set does NOT take effect on the running
	// daemon — restart is still required to pick up changes; the
	// dashboard surfaces this in its Restart-required note.
	ConfigSet(ctx context.Context, path, value string) error

	// ── phase 2: contacts ─────────────────────────────────────────────
	//
	// GetContact looks up a contact by hex pubkey. Returns nil + a
	// not-found error if the pubkey is not in the contacts list.
	GetContact(ctx context.Context, pubkey string) (*contacts.Contact, error)

	// AddContact parses a mindgate:// card URI and persists the
	// resulting contact (label / tier / relays). labelOverride, when
	// non-empty, supersedes the label embedded in the card. Returns
	// the persisted contact on success. Emits contact.added.
	AddContact(ctx context.Context, cardURI, labelOverride string) (*contacts.Contact, error)

	// RemoveContact deletes the contact by pubkey. The dashboard
	// gates this with the typed-confirm modal; the adapter does not
	// re-validate the phrase. Emits contact.removed.
	RemoveContact(ctx context.Context, pubkey string) error

	// SetContactLabel renames a contact. Empty (post-trim) is
	// rejected. Emits contact.relabeled.
	SetContactLabel(ctx context.Context, pubkey, label string) error

	// SetContactTier moves a contact between trust tiers (master /
	// friend / acquaintance / blocked). Emits contact.tier-changed.
	SetContactTier(ctx context.Context, pubkey string, tier contacts.Tier) error

	// ScanCard parses a mindgate:// card URI without persisting
	// anything. Returns a preview the dashboard renders so the
	// operator can confirm what they'd add. AlreadyContact is true
	// when the card's pubkey is already in the contacts list.
	ScanCard(ctx context.Context, cardURI string) (ScanPreview, error)

	// ── phase 3: invites ─────────────────────────────────────────────
	//
	// ListInvites returns invites filtered by status. Pass "" for all.
	// The list is ordered by created_at DESC.
	ListInvites(ctx context.Context, status string) ([]*invitedb.Invite, error)

	// CreateInvite signs a new invite token and persists the row.
	// Returns the persisted invite (status='active', uses=0) plus the
	// shareable mindgate-invite URI. Emits invite.created.
	CreateInvite(ctx context.Context, opts InviteCreateOpts) (*invitedb.Invite, string, error)

	// RevokeInvite marks the invite as revoked, looked up by ID
	// prefix (8+ hex chars). Returns the full ID on success. Emits
	// invite.revoked. The dashboard gates this with the typed-confirm
	// modal; the adapter does not re-validate the phrase.
	RevokeInvite(ctx context.Context, idPrefix string) (string, error)

	// RedeemInvite verifies the invite token, adds the issuer as a
	// contact, and publishes a kind:25001 redemption gift wrap.
	// Emits invite.redeemed AND contact.added.
	RedeemInvite(ctx context.Context, token string) (RedeemResult, error)

	// ── phase 4: own relays ──────────────────────────────────────────
	//
	// ListOwnRelays returns the rows of own_relays ordered by role
	// (home > fallback) then URL. Combined with ListRelayHealth() the
	// dashboard renders a per-row health pill.
	ListOwnRelays(ctx context.Context) ([]OwnRelay, error)

	// AddOwnRelay inserts a relay URL with the given role
	// ("home" or "fallback"). Returns a wrapped validation error
	// suitable for inline rendering when the URL or role is rejected
	// or the URL already exists. Emits relay.added on success.
	AddOwnRelay(ctx context.Context, rawURL, role string) error

	// RemoveOwnRelay deletes a relay row by exact URL. The dashboard
	// gates this with a typed-confirm modal when the role is "home";
	// the adapter does not re-validate the phrase. Refuses when
	// removing the row would leave zero home relays. Emits relay.removed.
	RemoveOwnRelay(ctx context.Context, rawURL string) error

	// ── phase 5: service control ────────────────────────────────────
	//
	// Status returns a snapshot of daemon-process metadata for the
	// Service tab. Reads only — no side effects.
	Status() ServiceStatus

	// LifecycleRun spawns `os.Args[0] <args...>` and returns the
	// job id immediately. Stdout is streamed via SSE
	// (`lifecycle.line:<jobID>` / `lifecycle.done:<jobID>`). At most
	// one job in flight at a time; concurrent calls return ErrLifecycleBusy.
	// No ctx parameter on purpose: the request context is canceled the
	// moment the handler returns the job id, but the subprocess must
	// outlive that — the daemon owns a long-lived lifecycle context
	// internally.
	LifecycleRun(args []string) (jobID string, err error)
}

// ServiceStatus is the snapshot read by /settings/service. Pulled from
// the daemon at request time; no caching.
type ServiceStatus struct {
	Version       string
	Commit        string
	BuildDate     string
	StartedAt     time.Time
	StateDir      string
	DashboardURL  string
	IPCSocket     string
	ActiveJobID   string    // empty when no lifecycle job is in flight
	ActiveJobArgs []string  // job's argv, e.g. ["gate","reconnect"]
	ActiveJobAt   time.Time // when the in-flight job started
}

// ErrLifecycleBusy is the dashboard-side sentinel returned by
// LifecycleRun when another job is already in flight. The handler maps
// it to HTTP 409 Conflict.
var ErrLifecycleBusy = errors.New("lifecycle: another job is already in flight")

// OwnRelay is the dashboard-local view of an own_relays row.
type OwnRelay struct {
	URL     string
	Role    string
	AddedAt int64 // Unix timestamp; 0 if unknown
}

// ErrRelay* are the typed errors AddOwnRelay and RemoveOwnRelay return.
// Handlers map them to HTTP status codes via errors.Is, avoiding the
// brittle err.Error() substring-matching the first cut used.
var (
	ErrRelayInvalidURL   = errors.New("relay: invalid URL")
	ErrRelayInvalidRole  = errors.New("relay: invalid role")
	ErrRelayDuplicate    = errors.New("relay: already on file")
	ErrRelayNotFound     = errors.New("relay: not on file")
	ErrRelayHomeRequired = errors.New("relay: at least one home relay must remain")
)

// InviteCreateOpts is the dashboard-local form of the create-invite
// request body. Negative ExpiresSeconds means "no expiry"; zero means
// "use the daemon's default (7d)". Unlimited overrides MaxUses.
type InviteCreateOpts struct {
	SingleUse      bool
	Unlimited      bool
	MaxUses        int
	ExpiresSeconds int64
	IssuerLabel    string
	RedeemerLabel  string
}

// RedeemResult is the dashboard-local view of an invite redemption.
type RedeemResult struct {
	IssuerNpub  string
	IssuerRelay string
	AcceptedBy  []string
}

// ScanPreview is the read-only result of a card-scan dry-run.
type ScanPreview struct {
	Pubkey         string
	Npub           string
	Label          string
	Relay          string
	AlreadyContact bool
}

// Event is the SSE-bound broadcast type. Kind discriminates the union;
// only the matching pointer field is populated.
//
// HTML, when non-empty, replaces all rendering in renderEvent and is
// emitted directly as the SSE data: payload. The lifecycle stream
// (Phase 5) sets this so each child stdout line lands in the page
// without round-tripping through the templates — the renderer can't
// know the line text in advance, and the cost of rendering a one-line
// fragment via html/template would be wasteful.
type Event struct {
	Kind    string
	HTML    string
	Message *inbox.Message
	Sent    *inbox.Sent
	Contact *contacts.Contact
	Relay   *RelayState
}

// RelayState mirrors daemon.RelayHealth for dashboard consumption. Kept
// in the dashboard package to avoid importing daemon (would create a
// cycle: dashboard → daemon → dashboard).
type RelayState struct {
	URL         string `json:"url"`
	Role        string `json:"role"`
	State       string `json:"state"`
	LastError   string `json:"last_error,omitempty"`
	LastEventAt int64  `json:"last_event_at,omitempty"`
}
