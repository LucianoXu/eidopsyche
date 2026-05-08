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
type fakeDeps struct {
	pubkey    string
	label     string
	inbox     []inbox.Message
	outbox    []inbox.Sent
	contactsL []*contacts.Contact
	sendErr   error
	sendID    string
}

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
func (f fakeDeps) ListRelayHealth() []RelayState { return nil }
func (f fakeDeps) Send(_ context.Context, _ string, _ envelope.Envelope) (string, error) {
	return f.sendID, f.sendErr
}
func (f fakeDeps) SubscribeEvents() (<-chan Event, func()) {
	ch := make(chan Event, 4)
	return ch, func() {}
}
