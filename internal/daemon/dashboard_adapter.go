package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
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

	urls, err := a.d.ownRelayURLs(ctx)
	if err != nil {
		return "", err
	}
	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := a.d.Pool.Publish(publishCtx, urls, wrapBob)
	_ = a.d.Pool.Publish(publishCtx, urls, wrapSelf)

	for _, r := range res {
		if r.OK {
			now := time.Now().Unix()
			sent := inbox.Sent{
				EventID:     wrapBob.ID,
				SelfEventID: wrapSelf.ID,
				To:          toPubkey,
				Kind:        14,
				Content:     content,
				RumorAt:     now,
				SentAt:      now,
				AcceptedBy:  []string{r.Relay},
				Final:       true,
			}
			_ = a.d.Box.AppendOutbox(sent)
			sc := sent
			a.d.emitDashEvent(dashboard.Event{Kind: "outbox.message", Sent: &sc})
			return wrapBob.ID, nil
		}
	}
	return "", fmt.Errorf("no relay accepted publish on %d urls", len(urls))
}

func (a dashboardAdapter) SubscribeEvents() (<-chan dashboard.Event, func()) {
	return a.d.subscribeDashboard()
}
