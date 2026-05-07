// Package dashboard implements the local web dashboard for MindGate.
// See docs/superpowers/specs/2026-05-07-dashboard-design.md.
package dashboard

import (
	"context"
	"time"

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

	// Send wraps text in a v1 chat envelope and publishes via the same
	// path as the existing IPC `send` method. Returns the wrap event_id
	// on success, or an error if no relay accepts (caller renders the
	// error in the compose pane).
	Send(ctx context.Context, toPubkey string, env envelope.Envelope) (eventID string, err error)

	// SubscribeEvents returns a channel of Event values for the SSE hub
	// to fan out. cancel must be called to release the slot.
	SubscribeEvents() (ch <-chan Event, cancel func())
}

// Event is the SSE-bound broadcast type. Kind discriminates the union;
// only the matching pointer field is populated.
type Event struct {
	Kind    string
	Message *inbox.Message
	Sent    *inbox.Sent
	Contact *contacts.Contact
}
