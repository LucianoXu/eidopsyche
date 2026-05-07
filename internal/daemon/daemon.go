package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/yingtexu/eidopsyche/internal/config"
	"github.com/yingtexu/eidopsyche/internal/contacts"
	"github.com/yingtexu/eidopsyche/internal/identity"
	"github.com/yingtexu/eidopsyche/internal/inbox"
	"github.com/yingtexu/eidopsyche/internal/invitedb"
	"github.com/yingtexu/eidopsyche/internal/ipc"
	"github.com/yingtexu/eidopsyche/internal/nostr"
	"github.com/yingtexu/eidopsyche/internal/store"
)

// Daemon holds all runtime state for a running MindGate instance.
type Daemon struct {
	StateDir string
	Cfg      config.Config
	Key      *identity.Keypair
	DB       *store.DB
	Repo     *contacts.Repo
	Invites  *invitedb.Repo
	Box      *inbox.Store
	Pool     *nostr.Pool
	Log      *slog.Logger

	kick        chan struct{}
	mu          sync.Mutex
	subs        []*ipc.Conn
	dedupe      map[string]struct{}
	selfWrapIDs map[string]struct{}
}

// Start loads state from stateDir and initialises the daemon without yet
// running the event loop.
func Start(stateDir string) (*Daemon, error) {
	cfgPath := filepath.Join(stateDir, "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	keyPath := filepath.Join(stateDir, "key")
	k, err := identity.LoadKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("load key: %w", err)
	}
	db, err := store.Open(filepath.Join(stateDir, "state.db"), false)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		return nil, err
	}
	d := &Daemon{
		StateDir:    stateDir,
		Cfg:         cfg,
		Key:         k,
		DB:          db,
		Repo:        contacts.New(db),
		Invites:     invitedb.New(db),
		Box:         inbox.New(stateDir),
		Pool:        nostr.NewPool(),
		Log:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		kick:        make(chan struct{}, 1),
		dedupe:      make(map[string]struct{}, 1024),
		selfWrapIDs: make(map[string]struct{}, 1024),
	}
	return d, nil
}

// Stop closes the relay pool and database.
func (d *Daemon) Stop(ctx context.Context) {
	d.Pool.Close()
	d.DB.Close()
}

// Run acquires a state-dir lock, starts the IPC server, launches the Nostr
// subscriber goroutine, and serves until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	lockPath := filepath.Join(d.StateDir, "state.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another daemon is running: %w", err)
	}
	defer lockFile.Close()
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	fmt.Fprintf(lockFile, "%d\n", os.Getpid())

	socket := filepath.Join(d.StateDir, d.Cfg.Daemon.Socket)
	srv := ipc.NewServer(socket, &handler{d: d})
	if err := srv.Listen(); err != nil {
		return err
	}
	d.Log.Info("ipc listening", "socket", socket)

	go d.runSubscriber(ctx)

	return srv.Serve(ctx)
}

// Refresh asks the subscriber to recompute its relay set and reattach.
// Non-blocking; if a refresh is already pending the call is a no-op.
func (d *Daemon) Refresh() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// runSubscriber subscribes to gift-wrap events addressed to our pubkey on all
// known relays and dispatches them through handleIncoming. It retries with
// exponential backoff on failure and can be kicked to refresh early via d.kick.
func (d *Daemon) runSubscriber(parent context.Context) {
	const (
		baseBackoff = time.Second
		maxBackoff  = 60 * time.Second
	)
	backoff := baseBackoff

	for parent.Err() == nil {
		subCtx, cancel := context.WithCancel(parent)

		urls, err := d.subscriptionURLs(subCtx)
		if err != nil {
			d.Log.Warn("subscription urls", "err", err, "backoff", backoff)
			cancel()
			if !waitForRetry(parent, d.kick, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		if len(urls) == 0 {
			d.Log.Warn("no relays in subscription set; will retry", "backoff", backoff)
			cancel()
			if !waitForRetry(parent, d.kick, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		since := gnostr.Timestamp(time.Now().Add(-48 * time.Hour).Unix())
		filter := gnostr.Filter{
			Kinds: []int{1059},
			Tags:  gnostr.TagMap{"p": []string{d.Key.PublicHex}},
			Since: &since,
		}
		ch, err := d.Pool.Subscribe(subCtx, urls, filter)
		if err != nil {
			d.Log.Warn("subscribe failed; will retry", "err", err, "backoff", backoff)
			cancel()
			if !waitForRetry(parent, d.kick, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		d.Log.Info("subscribed", "relays", urls)
		backoff = baseBackoff

		kicked := false
	drain:
		for {
			select {
			case <-parent.Done():
				cancel()
				return
			case <-d.kick:
				d.Log.Info("subscriber refresh requested")
				kicked = true
				break drain
			case ev, ok := <-ch:
				if !ok {
					d.Log.Warn("subscription channel closed; reconnecting")
					break drain
				}
				d.handleIncoming(parent, ev)
			}
		}
		cancel()
		if kicked {
			// fast restart on explicit refresh
			backoff = 100 * time.Millisecond
		}
	}
}

// waitForRetry sleeps for d, returning true to continue or false if ctx is done.
// A kick signal short-circuits the sleep and returns true.
func waitForRetry(ctx context.Context, kick <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-kick:
		return true
	case <-t.C:
		return true
	}
}

// nextBackoff doubles b up to max.
func nextBackoff(b, max time.Duration) time.Duration {
	next := b * 2
	if next > max {
		next = max
	}
	return next
}

// subscriptionURLs returns the union of own relays, contact relays, and extra
// relays from config.
func (d *Daemon) subscriptionURLs(ctx context.Context) ([]string, error) {
	uniq := map[string]struct{}{}
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url FROM own_relays`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return nil, err
		}
		uniq[u] = struct{}{}
	}
	rows.Close()
	contactRelays, err := d.Repo.AllRelaysUnion(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range contactRelays {
		uniq[u] = struct{}{}
	}
	for _, u := range d.Cfg.Subscribe.ExtraRelays {
		uniq[u] = struct{}{}
	}
	out := make([]string, 0, len(uniq))
	for u := range uniq {
		out = append(out, u)
	}
	return out, nil
}

// handleIncoming processes a single inbound gift-wrap event: deduplication,
// unwrap, and dispatch by inner rumor kind.
func (d *Daemon) handleIncoming(ctx context.Context, ev *gnostr.Event) {
	d.mu.Lock()
	if _, dup := d.dedupe[ev.ID]; dup {
		d.mu.Unlock()
		return
	}
	d.dedupe[ev.ID] = struct{}{}
	if _, self := d.selfWrapIDs[ev.ID]; self {
		d.mu.Unlock()
		d.Log.Debug("self-copy echo dropped (in-memory match)", "event_id", ev.ID)
		return
	}
	d.mu.Unlock()

	rumor, err := nostr.Unwrap(d.Key.PrivateHex, ev)
	if err != nil {
		d.Log.Warn("unwrap failed", "event_id", ev.ID, "err", err)
		return
	}
	if rumor.PubKey == d.Key.PublicHex {
		d.Log.Debug("self-copy echo dropped (sender=self)", "event_id", ev.ID)
		return
	}

	switch rumor.Kind {
	case 14:
		d.handleChatMessage(ctx, ev, rumor)
	case 25001:
		d.handleInviteRedemption(ctx, ev, rumor)
	default:
		d.Log.Warn("ignored unknown rumor kind", "kind", rumor.Kind, "from", rumor.PubKey)
	}
}

// handleChatMessage is the existing chat message path: contact filter, inbox
// append, broadcast.
func (d *Daemon) handleChatMessage(ctx context.Context, ev *gnostr.Event, rumor *gnostr.Event) {
	c, err := d.Repo.Get(ctx, rumor.PubKey)
	if err != nil || c.Tier == contacts.TierBlocked {
		d.Log.Debug("dropped non-contact / blocked", "from", rumor.PubKey)
		return
	}
	msg := inbox.Message{
		EventID:    ev.ID,
		InnerID:    rumor.ID,
		From:       rumor.PubKey,
		Kind:       rumor.Kind,
		Content:    rumor.Content,
		RumorAt:    int64(rumor.CreatedAt),
		ReceivedAt: time.Now().Unix(),
	}
	if err := d.Box.AppendInbox(msg); err != nil {
		d.Log.Error("append inbox", "err", err)
		return
	}
	d.broadcastInbox(msg)
}

// handleInviteRedemption processes an incoming kind:25001 invite-redemption rumor.
func (d *Daemon) handleInviteRedemption(ctx context.Context, _ *gnostr.Event, rumor *gnostr.Event) {
	var body struct {
		V                 int    `json:"v"`
		InviteID          string `json:"invite_id"`
		RedeemerRelay     string `json:"redeemer_relay"`
		RedeemerLabelHint string `json:"redeemer_label_hint"`
	}
	if err := json.Unmarshal([]byte(rumor.Content), &body); err != nil {
		d.Log.Warn("invite redemption: malformed content", "from", rumor.PubKey, "err", err)
		return
	}
	if body.InviteID == "" {
		d.Log.Warn("invite redemption: missing invite_id", "from", rumor.PubKey)
		return
	}

	inv, err := d.Invites.Get(ctx, body.InviteID)
	if err != nil {
		d.Log.Info("invite redemption: invite not found", "invite_id", body.InviteID)
		return
	}

	if inv.Status == invitedb.StatusRevoked {
		d.Log.Info("invite redemption: invite is revoked", "invite_id", body.InviteID)
		return
	}

	now := time.Now()
	if !inv.ExpiresAt.IsZero() && now.After(inv.ExpiresAt) {
		_ = d.Invites.MarkExpired(ctx, body.InviteID)
		d.Log.Info("invite redemption: invite has expired", "invite_id", body.InviteID)
		return
	}
	if inv.MaxUses != 0 && inv.Uses >= inv.MaxUses {
		_ = d.Invites.MarkExpired(ctx, body.InviteID)
		d.Log.Info("invite redemption: invite exhausted", "invite_id", body.InviteID)
		return
	}

	updated, err := d.Invites.RecordRedemption(ctx, body.InviteID, rumor.PubKey)
	if err != nil {
		if errors.Is(err, invitedb.ErrAlreadyRedeemed) {
			d.Log.Info("invite redemption: already redeemed by this key", "from", rumor.PubKey)
			return
		}
		d.Log.Error("invite redemption: record failed", "err", err)
		return
	}

	// Determine label for the new contact.
	label := inv.RedeemerLabel
	if label == "" {
		label = body.RedeemerLabelHint
	}
	if label == "" {
		shortID := body.InviteID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		label = fmt.Sprintf("invitee-%s", shortID)
	}

	// Add the redeemer as a contact (idempotent: ErrExists is OK).
	var relays []string
	if body.RedeemerRelay != "" {
		relays = []string{body.RedeemerRelay}
	}
	addErr := d.Repo.Add(ctx, contacts.Contact{
		Pubkey: rumor.PubKey,
		Label:  label,
		Tier:   contacts.TierFriend,
		Relays: relays,
	})
	if addErr != nil && !errors.Is(addErr, contacts.ErrExists) {
		d.Log.Error("invite redemption: add contact failed", "err", addErr)
		return
	}

	// If uses now equal max_uses (and unlimited is not in effect), expire the invite.
	if updated.MaxUses != 0 && updated.Uses >= updated.MaxUses {
		_ = d.Invites.MarkExpired(ctx, body.InviteID)
	}

	d.Refresh()

	// Push contact.added event to inbox.tail subscribers.
	npub, _ := identity.EncodeNpub(rumor.PubKey)
	d.broadcastContactAdded(map[string]any{
		"npub":      npub,
		"pubkey":    rumor.PubKey,
		"label":     label,
		"source":    "invite",
		"invite_id": body.InviteID,
	})
}

// broadcastContactAdded pushes a contact.added push event to all live IPC subscribers.
func (d *Daemon) broadcastContactAdded(data any) {
	d.mu.Lock()
	subs := make([]*ipc.Conn, len(d.subs))
	copy(subs, d.subs)
	d.mu.Unlock()
	for _, c := range subs {
		_ = c.PushEvent("contact.added", data)
	}
}

// broadcastInbox pushes an inbox message to all live IPC subscribers.
func (d *Daemon) broadcastInbox(m inbox.Message) {
	d.mu.Lock()
	subs := make([]*ipc.Conn, len(d.subs))
	copy(subs, d.subs)
	d.mu.Unlock()
	for _, c := range subs {
		_ = c.PushEvent("inbox.message", m)
	}
}

// addSubscriber registers conn as an inbox push target and auto-removes it when
// the connection closes.
func (d *Daemon) addSubscriber(c *ipc.Conn) {
	d.mu.Lock()
	d.subs = append(d.subs, c)
	d.mu.Unlock()
	go func() {
		<-c.Closed()
		d.mu.Lock()
		for i, x := range d.subs {
			if x == c {
				d.subs = append(d.subs[:i], d.subs[i+1:]...)
				break
			}
		}
		d.mu.Unlock()
	}()
}

// recordSelfWrap remembers a wrap event ID that we published as the self-copy
// so that if the relay echoes it back to our subscription we drop it silently.
// The map is capped at 1024 entries; excess entries are evicted by dropping an
// arbitrary entry.
func (d *Daemon) recordSelfWrap(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.selfWrapIDs) >= 1024 {
		for k := range d.selfWrapIDs {
			delete(d.selfWrapIDs, k)
			break
		}
	}
	d.selfWrapIDs[id] = struct{}{}
}
