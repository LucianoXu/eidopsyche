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
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
	"github.com/LucianoXu/eidopsyche/internal/store"
	"github.com/LucianoXu/eidopsyche/internal/version"
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

	startedAt time.Time

	kick        chan struct{}
	mu          sync.Mutex
	subs        []*ipc.Conn
	dashSubs    []*dashSub
	dedupe      map[string]struct{}
	selfWrapIDs map[string]struct{}
	relayHealth *relayHealthStore

	// configMu serialises read-modify-write of config.toml so two
	// concurrent dashboard ConfigSet calls cannot lose updates by
	// loading the same snapshot, mutating different keys, and saving
	// over each other. Acquired by dashboardAdapter.ConfigSet only;
	// the daemon does not hot-reload its in-memory config from the
	// file (changes take effect on restart) so other paths don't
	// need to take it.
	configMu sync.Mutex

	// lifecycle: at most one subprocess (eidos gate <subcmd> /
	// eidos self-update) in flight at a time. lifeCtx is daemon-owned
	// so the child outlives the HTTP request that spawned it; the
	// dashboard handler returns the job id immediately and the line
	// pump runs in its own goroutine.
	lifeMu      sync.Mutex
	lifeCtx     context.Context
	lifeCancel  context.CancelFunc
	lifeSpawner LifecycleSpawner
	activeLife  *lifecycleJob

	// testSendChatReply, if non-nil, replaces sendChatReply during tests
	// to avoid actual NIP-17 publish over the network.
	testSendChatReply func(ctx context.Context, toPubkey string, text string) error
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
		Pool:        nostr.NewPoolWithSigner(keypairSigner{k: k}),
		Log:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		startedAt:   time.Now(),
		kick:        make(chan struct{}, 1),
		dedupe:      make(map[string]struct{}, 1024),
		selfWrapIDs: make(map[string]struct{}, 1024),
		relayHealth: newRelayHealthStore(),
	}
	// Wire Pool's per-URL state hook so transitions surface in d.relayHealth
	// AND in the dashboard SSE hub. The hook runs from inside the Pool's
	// per-URL Subscribe pumps; emitting the dashboard event here keeps the
	// dashboard panel live without daemon's runSubscriber needing to know.
	d.Pool.SetStateHook(func(url, state, lastErr string) {
		h := d.relayHealth.setState(url, state, lastErr)
		d.emitRelayState(h)
	})
	d.Pool.SetEventHook(func(url string) {
		d.relayHealth.markEvent(url)
	})
	return d, nil
}

// emitRelayState fans the just-updated RelayHealth out to dashboard SSE
// subscribers. Daemon owns the dashSubs slice (see dashboard_event.go);
// this helper keeps the conversion in one place.
func (d *Daemon) emitRelayState(h RelayHealth) {
	state := dashboard.RelayState{
		URL:         h.URL,
		Role:        h.Role,
		State:       h.State,
		LastError:   h.LastError,
		LastEventAt: h.LastEventAt,
	}
	d.emitDashEvent(dashboard.Event{Kind: "relay.state", Relay: &state})
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
	if err := acquireExclusiveLock(lockFile); err != nil {
		return fmt.Errorf("another daemon is running: %w", err)
	}
	defer lockFile.Close()
	defer releaseLock(lockFile) //nolint:errcheck

	fmt.Fprintf(lockFile, "%d\n", os.Getpid())

	socket := filepath.Join(d.StateDir, d.Cfg.Daemon.Socket)
	srv := ipc.NewServer(socket, &handler{d: d})
	if err := srv.Listen(); err != nil {
		return err
	}
	d.Log.Info("ipc listening", "socket", socket)

	d.installLifecycle(nil) // nil → use platform-default lifecycleSpawn
	defer d.shutdownLifecycle()

	go d.runSubscriber(ctx)
	go func() {
		_ = dashboard.Run(ctx, NewDashboardAdapter(d), d.Cfg.Dashboard, d.Log)
	}()

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
// relays from config. Side effect: refreshes per-URL Role in d.relayHealth
// and prunes URLs no longer in the set so the snapshot stays in sync with
// the live subscription target list.
func (d *Daemon) subscriptionURLs(ctx context.Context) ([]string, error) {
	roles := map[string]string{} // first-wins; "home" / "fallback" beats "contact" beats "extra"
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url, role FROM own_relays`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u, r string
		if err := rows.Scan(&u, &r); err != nil {
			rows.Close()
			return nil, err
		}
		roles[u] = r
	}
	rows.Close()
	contactRelays, err := d.Repo.AllRelaysUnion(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range contactRelays {
		if _, taken := roles[u]; !taken {
			roles[u] = "contact"
		}
	}
	for _, u := range d.Cfg.Subscribe.ExtraRelays {
		if _, taken := roles[u]; !taken {
			roles[u] = "extra"
		}
	}
	out := make([]string, 0, len(roles))
	keep := make(map[string]struct{}, len(roles))
	for u, role := range roles {
		out = append(out, u)
		keep[u] = struct{}{}
		if d.relayHealth != nil {
			d.relayHealth.setRole(u, role)
		}
	}
	if d.relayHealth != nil {
		d.relayHealth.reset(keep)
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

	// Self-copy echo detection happens upstream via selfWrapIDs (matched on
	// the outer wrap ID). We deliberately do NOT drop solely on
	// rumor.PubKey == self.PublicHex here, because envelope-v1 commands are
	// authorized only when sender == self (spec §5.1) and must reach
	// dispatchEnvelope.

	switch rumor.Kind {
	case 14:
		d.dispatchEnvelope(ctx, ev, rumor)
	case 25001:
		d.handleInviteRedemption(ctx, ev, rumor)
	default:
		d.Log.Warn("ignored unknown rumor kind", "kind", rumor.Kind, "from", rumor.PubKey)
	}
}

// dispatchEnvelope decodes the kind:14 rumor's content as a v1 envelope and
// dispatches per spec §9.1. Decode errors and forbidden combinations are
// soft-rejected: persisted to inbox with Malformed=true and a RejectReason,
// without triggering chat or command paths.
func (d *Daemon) dispatchEnvelope(ctx context.Context, ev *gnostr.Event, rumor *gnostr.Event) {
	env, err := envelope.Decode(rumor.Content)
	if err != nil {
		reason := "schema_violation"
		switch {
		case errors.Is(err, envelope.ErrNotEnvelope):
			reason = "not_envelope"
		case errors.Is(err, envelope.ErrUnsupportedVersion):
			reason = "unsupported_version"
		}
		d.persistSoftReject(ev, rumor, reason)
		return
	}

	switch env.Type {
	case envelope.TypeChat:
		// Chats from self (e.g., command replies, self-notes) bypass the
		// contact filter — self is always trusted.
		if rumor.PubKey != d.Key.PublicHex {
			c, err := d.Repo.Get(ctx, rumor.PubKey)
			if err != nil || c.Tier == contacts.TierBlocked {
				d.Log.Debug("dropped non-contact / blocked", "from", rumor.PubKey)
				return
			}
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

	case envelope.TypeCommand:
		if rumor.PubKey != d.Key.PublicHex {
			d.persistSoftReject(ev, rumor, "unauthorized_command")
			return
		}
		reply, ok, runErr := dispatchCommand(ctx, d, env.Command.Name, env.Command.Args)
		if !ok {
			d.persistSoftReject(ev, rumor, "unknown_command")
			return
		}
		if runErr != nil {
			reply = "error: " + runErr.Error()
		}
		if err := d.sendChatReply(ctx, rumor.PubKey, reply); err != nil {
			d.Log.Warn("command reply failed", "err", err)
		}
	}
}

// persistSoftReject appends the rumor to inbox with Malformed=true and the
// given reason, then broadcasts. The Content field stores rumor.Content
// as-received so operators can debug interop issues.
func (d *Daemon) persistSoftReject(ev *gnostr.Event, rumor *gnostr.Event, reason string) {
	d.Log.Info("soft-reject", "reason", reason, "from", rumor.PubKey, "event_id", ev.ID)
	msg := inbox.Message{
		EventID:      ev.ID,
		InnerID:      rumor.ID,
		From:         rumor.PubKey,
		Kind:         rumor.Kind,
		Content:      rumor.Content,
		RumorAt:      int64(rumor.CreatedAt),
		ReceivedAt:   time.Now().Unix(),
		Malformed:    true,
		RejectReason: reason,
	}
	if err := d.Box.AppendInbox(msg); err != nil {
		d.Log.Error("append inbox (soft-reject)", "err", err)
		return
	}
	d.broadcastInbox(msg)
}

// sendChatReply wraps text in a v1 chat envelope and publishes a NIP-17
// gift wrap to the given pubkey (which, for v1 commands, is always self).
// Returns an error when no relay accepts the publish so the caller can log
// the failure; in tests, d.testSendChatReply takes precedence to avoid
// network I/O.
func (d *Daemon) sendChatReply(ctx context.Context, toPubkey string, text string) error {
	if d.testSendChatReply != nil {
		return d.testSendChatReply(ctx, toPubkey, text)
	}
	env := envelope.Envelope{
		V:      envelope.SchemaVersion,
		Type:   envelope.TypeChat,
		Text:   text,
		Client: &envelope.Client{Name: "eidos", Ver: version.Version},
	}
	content, err := envelope.Encode(env)
	if err != nil {
		return err
	}
	wrap, _, err := nostr.Wrap(d.Key.PrivateHex, toPubkey, content)
	if err != nil {
		return err
	}
	urls, err := d.ownRelayURLs(ctx)
	if err != nil {
		return fmt.Errorf("own relays: %w", err)
	}
	if len(urls) == 0 {
		return errors.New("no own relays configured for reply")
	}
	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := d.Pool.Publish(publishCtx, urls, wrap)
	for _, r := range res {
		if r.OK {
			return nil
		}
	}
	return fmt.Errorf("publish failed on all %d relays", len(urls))
}

// ownRelayURLs returns the URLs from own_relays for command-reply publish.
// Returns an error if the query or any row scan fails, so callers can
// surface storage-layer problems rather than silently returning an empty
// list (which would otherwise be indistinguishable from "no relays
// configured").
func (d *Daemon) ownRelayURLs(ctx context.Context) ([]string, error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url FROM own_relays`)
	if err != nil {
		return nil, fmt.Errorf("query own_relays: %w", err)
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan own_relays: %w", err)
		}
		urls = append(urls, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate own_relays: %w", err)
	}
	return urls, nil
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
	// Convert the loose payload to a Contact pointer when possible so the
	// dashboard event has structured fields. Fall back to a kind-only event.
	if m, ok := data.(map[string]any); ok {
		if pk, _ := m["pubkey"].(string); pk != "" {
			if c, err := d.Repo.Get(context.Background(), pk); err == nil {
				d.emitDashEvent(dashboard.Event{Kind: "contact.added", Contact: c})
				return
			}
		}
	}
	d.emitDashEvent(dashboard.Event{Kind: "contact.added"})
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
	mc := m
	d.emitDashEvent(dashboard.Event{Kind: "inbox.message", Message: &mc})
}

// dashSub is a single dashboard subscriber: a buffered event channel plus a
// done channel that signals "no more sends, please". This pair lets cancel()
// stop emit() from racing into a closed channel: emit selects on both.
type dashSub struct {
	ch   chan dashboard.Event
	done chan struct{}
	once sync.Once
}

// subscribeDashboard registers a new SSE-bound subscriber. The returned
// cancel function closes the done channel (idempotent) and removes the
// subscriber from the fan-out list. The event channel is never closed by
// cancel() so concurrent emit() sends cannot panic; emit() picks done as
// the abandon signal instead.
func (d *Daemon) subscribeDashboard() (<-chan dashboard.Event, func()) {
	sub := &dashSub{
		ch:   make(chan dashboard.Event, 4),
		done: make(chan struct{}),
	}
	d.mu.Lock()
	d.dashSubs = append(d.dashSubs, sub)
	d.mu.Unlock()
	cancel := func() {
		d.mu.Lock()
		for i, s := range d.dashSubs {
			if s == sub {
				d.dashSubs = append(d.dashSubs[:i], d.dashSubs[i+1:]...)
				break
			}
		}
		d.mu.Unlock()
		sub.once.Do(func() { close(sub.done) })
	}
	return sub.ch, cancel
}

// emitDashEvent fans an event to all dashboard subscribers. Slow consumers
// are dropped (logged) rather than blocking the broadcaster. A subscriber
// whose done channel has been closed is skipped before any send so a
// concurrent cancel cannot race emit into a closed channel.
func (d *Daemon) emitDashEvent(ev dashboard.Event) {
	d.mu.Lock()
	subs := make([]*dashSub, len(d.dashSubs))
	copy(subs, d.dashSubs)
	d.mu.Unlock()
	for _, sub := range subs {
		select {
		case <-sub.done:
			// subscriber cancelled; skip without sending
		case sub.ch <- ev:
		default:
			d.Log.Warn("dashboard subscriber slow; dropping event", "kind", ev.Kind)
		}
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
