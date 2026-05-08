package dashboard

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
)

func captureLogs(t *testing.T) (*slog.Logger, func() string) {
	t.Helper()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(stringWriter{&buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, func() string { return buf.String() }
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { w.b.WriteString(string(p)); return len(p), nil }

func TestRun_NonLoopbackBindRefused(t *testing.T) {
	logger, getLogs := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: true, Listen: "0.0.0.0:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on non-loopback: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after non-loopback refusal")
	}
	if !strings.Contains(getLogs(), "non-loopback bind not supported") {
		t.Errorf("expected non-loopback refusal log line; got: %s", getLogs())
	}
}

func TestRun_DisabledIsNoop(t *testing.T) {
	logger, _ := captureLogs(t)
	cfg := config.DashboardConfig{Enabled: false, Listen: "127.0.0.1:0"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fakeDeps{}, cfg, logger) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error when disabled: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return when disabled")
	}
}

// fakeDeps is the package-internal stub satisfying DashboardDeps for handler
// unit tests. Behaviour is overridable per-test via assignable fields.
//
// Value semantics: the struct is passed around by value, so mutation
// hooks (e.g. captured calls) live behind pointer-to-slice fields the
// test owns. This keeps every test a single literal initialisation.
type fakeDeps struct {
	pubkey    string
	label     string
	inbox     []inbox.Message
	outbox    []inbox.Sent
	contactsL []*contacts.Contact
	sendErr   error
	sendID    string

	// ── phase 1 ─────────────────────────────────────────────────
	cardURI        string
	cardErr        error
	setLabelErr    error
	setLabelCalls  *[]string
	configSnap     config.Config
	configSnapErr  error
	configSetErr   error
	configSetCalls *[]configSetCall

	// ── phase 2 ─────────────────────────────────────────────────
	getContactFn        func(ctx context.Context, pubkey string) (*contacts.Contact, error)
	addContactFn        func(ctx context.Context, cardURI, label string) (*contacts.Contact, error)
	removeContactErr    error
	removeContactCalls  *[]string
	setContactLabelErr  error
	setContactLabelFn   func(ctx context.Context, pubkey, label string) error
	setContactTierErr   error
	setContactTierCalls *[]contactTierCall
	scanCardFn          func(ctx context.Context, cardURI string) (ScanPreview, error)

	// ── phase 3 ─────────────────────────────────────────────────
	listInvitesFn   func(ctx context.Context, status string) ([]*invitedb.Invite, error)
	createInviteFn  func(ctx context.Context, opts InviteCreateOpts) (*invitedb.Invite, string, error)
	createInviteErr error
	revokeInviteFn  func(ctx context.Context, idPrefix string) (string, error)
	revokeCalls     *[]string
	redeemInviteFn  func(ctx context.Context, token string) (RedeemResult, error)

	// ── phase 4 ─────────────────────────────────────────────────
	ownRelays         []OwnRelay
	relayHealthRows   []RelayState
	addOwnRelayFn     func(ctx context.Context, rawURL, role string) error
	removeOwnRelayFn  func(ctx context.Context, rawURL string) error
	addOwnRelayCalls  *[]addOwnRelayCall
	removeOwnRelayLog *[]string
}

// addOwnRelayCall captures one AddOwnRelay invocation for assertions.
type addOwnRelayCall struct{ URL, Role string }

// contactTierCall captures one SetContactTier invocation for assertions.
type contactTierCall struct {
	Pubkey string
	Tier   contacts.Tier
}

// configSetCall captures one ConfigSet invocation for assertions.
type configSetCall struct{ Path, Value string }

func (f fakeDeps) OwnPubkey() string                        { return f.pubkey }
func (f fakeDeps) OwnLabel(context.Context) (string, error) { return f.label, nil }
func (f fakeDeps) ListInbox(*time.Time, string, int) ([]inbox.Message, error) {
	return f.inbox, nil
}
func (f fakeDeps) ListOutbox(*time.Time, string, int) ([]inbox.Sent, error) {
	return f.outbox, nil
}
func (f fakeDeps) ListContacts(context.Context) ([]*contacts.Contact, error) {
	return f.contactsL, nil
}
func (f fakeDeps) ListRelayHealth() []RelayState { return f.relayHealthRows }
func (f fakeDeps) Send(_ context.Context, _ string, _ envelope.Envelope) (string, error) {
	return f.sendID, f.sendErr
}
func (f fakeDeps) SubscribeEvents() (<-chan Event, func()) {
	ch := make(chan Event, 4)
	return ch, func() {}
}

func (f fakeDeps) SetOwnLabel(_ context.Context, label string) error {
	if f.setLabelCalls != nil {
		*f.setLabelCalls = append(*f.setLabelCalls, label)
	}
	return f.setLabelErr
}
func (f fakeDeps) OwnCardURI(context.Context) (string, error) {
	return f.cardURI, f.cardErr
}
func (f fakeDeps) ConfigSnapshot() (config.Config, error) {
	return f.configSnap, f.configSnapErr
}
func (f fakeDeps) ConfigSet(_ context.Context, path, value string) error {
	if f.configSetCalls != nil {
		*f.configSetCalls = append(*f.configSetCalls, configSetCall{Path: path, Value: value})
	}
	return f.configSetErr
}

func (f fakeDeps) GetContact(ctx context.Context, pubkey string) (*contacts.Contact, error) {
	if f.getContactFn != nil {
		return f.getContactFn(ctx, pubkey)
	}
	for _, c := range f.contactsL {
		if c.Pubkey == pubkey {
			return c, nil
		}
	}
	return nil, contacts.ErrNotFound
}

func (f fakeDeps) AddContact(ctx context.Context, cardURI, label string) (*contacts.Contact, error) {
	if f.addContactFn != nil {
		return f.addContactFn(ctx, cardURI, label)
	}
	return &contacts.Contact{Pubkey: "stub", Label: label, Tier: contacts.TierFriend}, nil
}

func (f fakeDeps) RemoveContact(_ context.Context, pubkey string) error {
	if f.removeContactCalls != nil {
		*f.removeContactCalls = append(*f.removeContactCalls, pubkey)
	}
	return f.removeContactErr
}

func (f fakeDeps) SetContactLabel(ctx context.Context, pubkey, label string) error {
	if f.setContactLabelFn != nil {
		return f.setContactLabelFn(ctx, pubkey, label)
	}
	return f.setContactLabelErr
}

func (f fakeDeps) SetContactTier(_ context.Context, pubkey string, tier contacts.Tier) error {
	if f.setContactTierCalls != nil {
		*f.setContactTierCalls = append(*f.setContactTierCalls, contactTierCall{Pubkey: pubkey, Tier: tier})
	}
	return f.setContactTierErr
}

func (f fakeDeps) ScanCard(ctx context.Context, cardURI string) (ScanPreview, error) {
	if f.scanCardFn != nil {
		return f.scanCardFn(ctx, cardURI)
	}
	return ScanPreview{}, nil
}

func (f fakeDeps) ListInvites(ctx context.Context, status string) ([]*invitedb.Invite, error) {
	if f.listInvitesFn != nil {
		return f.listInvitesFn(ctx, status)
	}
	return nil, nil
}

func (f fakeDeps) CreateInvite(ctx context.Context, opts InviteCreateOpts) (*invitedb.Invite, string, error) {
	if f.createInviteFn != nil {
		return f.createInviteFn(ctx, opts)
	}
	if f.createInviteErr != nil {
		return nil, "", f.createInviteErr
	}
	id := "0123456789abcdef0123456789abcdef"
	return &invitedb.Invite{
		ID:            id,
		CreatedAt:     time.Now(),
		MaxUses:       1,
		Status:        invitedb.StatusActive,
		IssuerLabel:   "stub",
		RedeemerLabel: opts.RedeemerLabel,
	}, "mindgate-invite://" + id, nil
}

func (f fakeDeps) RevokeInvite(ctx context.Context, idPrefix string) (string, error) {
	if f.revokeCalls != nil {
		*f.revokeCalls = append(*f.revokeCalls, idPrefix)
	}
	if f.revokeInviteFn != nil {
		return f.revokeInviteFn(ctx, idPrefix)
	}
	return idPrefix, nil
}

func (f fakeDeps) RedeemInvite(ctx context.Context, token string) (RedeemResult, error) {
	if f.redeemInviteFn != nil {
		return f.redeemInviteFn(ctx, token)
	}
	return RedeemResult{}, nil
}

func (f fakeDeps) ListOwnRelays(context.Context) ([]OwnRelay, error) {
	return f.ownRelays, nil
}

func (f fakeDeps) AddOwnRelay(ctx context.Context, rawURL, role string) error {
	if f.addOwnRelayCalls != nil {
		*f.addOwnRelayCalls = append(*f.addOwnRelayCalls, addOwnRelayCall{URL: rawURL, Role: role})
	}
	if f.addOwnRelayFn != nil {
		return f.addOwnRelayFn(ctx, rawURL, role)
	}
	return nil
}

func (f fakeDeps) RemoveOwnRelay(ctx context.Context, rawURL string) error {
	if f.removeOwnRelayLog != nil {
		*f.removeOwnRelayLog = append(*f.removeOwnRelayLog, rawURL)
	}
	if f.removeOwnRelayFn != nil {
		return f.removeOwnRelayFn(ctx, rawURL)
	}
	return nil
}
