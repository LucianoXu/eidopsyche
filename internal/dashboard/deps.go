// Package dashboard implements the local web dashboard for MindGate.
// See docs/superpowers/specs/2026-05-07-dashboard-design.md.
package dashboard

import (
	"context"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
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
}

// Event is the SSE-bound broadcast type. Kind discriminates the union;
// only the matching pointer field is populated.
type Event struct {
	Kind    string
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
