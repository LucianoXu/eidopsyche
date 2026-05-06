package daemon

import (
	"context"
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
	Box      *inbox.Store
	Pool     *nostr.Pool
	Log      *slog.Logger

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
		Box:         inbox.New(stateDir),
		Pool:        nostr.NewPool(),
		Log:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
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

// runSubscriber subscribes to gift-wrap events addressed to our pubkey on all
// known relays and dispatches them through handleIncoming.
func (d *Daemon) runSubscriber(ctx context.Context) {
	urls, err := d.subscriptionURLs(ctx)
	if err != nil {
		d.Log.Error("subscription urls", "err", err)
		return
	}
	if len(urls) == 0 {
		d.Log.Warn("no relays to subscribe; idle")
		return
	}
	since := gnostr.Timestamp(time.Now().Add(-48 * time.Hour).Unix())
	filter := gnostr.Filter{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{d.Key.PublicHex}},
		Since: &since,
	}
	d.Log.Info("subscribing", "relays", urls)
	ch, err := d.Pool.Subscribe(ctx, urls, filter)
	if err != nil {
		d.Log.Error("subscribe", "err", err)
		return
	}
	for ev := range ch {
		d.handleIncoming(ctx, ev)
	}
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
// unwrap, contact filter, inbox append, and subscriber broadcast.
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
