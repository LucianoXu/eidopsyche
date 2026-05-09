package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// makeTestCard returns a mindgate:// URI for a freshly-generated key
// plus the hex pubkey it encodes, for use in card.scan tests.
func makeTestCard(t *testing.T, label, relay string) (uri, pubkey string) {
	t.Helper()
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	c := card.Card{Npub: k.Npub, Relay: relay, Label: label}
	uri, err = c.URI()
	if err != nil {
		t.Fatal(err)
	}
	return uri, k.PublicHex
}

func TestCardScan_Strangeris_NotAlreadyContact(t *testing.T) {
	d := newTestDaemon(t)
	uri, pk := makeTestCard(t, "alice", "wss://relay.example/")
	var preview dashboard.ScanPreview
	if err := d.Call(context.Background(), "card.scan",
		map[string]string{"uri": uri}, &preview); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if preview.Pubkey != pk {
		t.Errorf("Pubkey=%q, want %q", preview.Pubkey, pk)
	}
	if preview.Label != "alice" {
		t.Errorf("Label=%q, want alice", preview.Label)
	}
	if preview.AlreadyContact {
		t.Errorf("AlreadyContact=true for fresh key, want false")
	}
}

func TestCardScan_KnownContact_FlagsAlready(t *testing.T) {
	d := newTestDaemon(t)
	pk := addTestContact(t, d, "bob")
	npub, err := identity.EncodeNpub(pk)
	if err != nil {
		t.Fatal(err)
	}
	c := card.Card{Npub: npub, Relay: "wss://relay.example/", Label: "bob"}
	uri, err := c.URI()
	if err != nil {
		t.Fatal(err)
	}
	var preview dashboard.ScanPreview
	if err := d.Call(context.Background(), "card.scan",
		map[string]string{"uri": uri}, &preview); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !preview.AlreadyContact {
		t.Errorf("AlreadyContact=false for known contact, want true")
	}
}

func TestCardScan_Garbage_RejectedAsCardInvalid(t *testing.T) {
	d := newTestDaemon(t)
	err := d.Call(context.Background(), "card.scan",
		map[string]string{"uri": "not-a-card"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrCardInvalid {
		t.Fatalf("err=%v, want CARD_INVALID", err)
	}
}

func TestServiceStatus_PopulatesStateDirAndSocket(t *testing.T) {
	d := newTestDaemon(t)
	d.startedAt = time.Now()
	d.Cfg = config.Config{
		Daemon:    config.DaemonConfig{Socket: "sock"},
		Dashboard: config.DashboardConfig{Listen: "127.0.0.1:22893"},
	}
	var st dashboard.ServiceStatus
	if err := d.Call(context.Background(), "service.status", nil, &st); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if st.StateDir != d.StateDir {
		t.Errorf("StateDir=%q, want %q", st.StateDir, d.StateDir)
	}
	if st.IPCSocket != filepath.Join(d.StateDir, "sock") {
		t.Errorf("IPCSocket=%q, want %q", st.IPCSocket, filepath.Join(d.StateDir, "sock"))
	}
	if st.DashboardURL != "http://127.0.0.1:22893" {
		t.Errorf("DashboardURL=%q, want http://127.0.0.1:22893", st.DashboardURL)
	}
	if st.ActiveJobID != "" {
		t.Errorf("ActiveJobID=%q, want empty", st.ActiveJobID)
	}
}

// TestLifecycleRun_ReturnsJobID asserts the IPC method kicks off a
// child and returns a job id that the in-flight snapshot reflects.
// Streaming is covered by lifecycle_test.go; this just pins the unary
// kickoff contract.
func TestLifecycleRun_ReturnsJobID(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`sleep 0.5; echo done`))

	var out struct {
		JobID string `json:"job_id"`
	}
	if err := d.Call(context.Background(), "lifecycle.run",
		LifecycleRunParams{Args: []string{"gate", "reconnect"}}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.JobID == "" {
		t.Fatal("JobID empty")
	}

	var snap LifecycleStatus
	if err := d.Call(context.Background(), "lifecycle.status", nil, &snap); err != nil {
		t.Fatalf("Call lifecycle.status: %v", err)
	}
	if !snap.Active || snap.JobID != out.JobID {
		t.Errorf("status=%+v, want Active=true JobID=%q", snap, out.JobID)
	}
}

func TestLifecycleRun_BusyMapsToLifecycleBusy(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`sleep 1; echo done`))

	var first struct {
		JobID string `json:"job_id"`
	}
	if err := d.Call(context.Background(), "lifecycle.run",
		LifecycleRunParams{Args: []string{"gate", "stop"}}, &first); err != nil {
		t.Fatalf("first Call: %v", err)
	}

	err := d.Call(context.Background(), "lifecycle.run",
		LifecycleRunParams{Args: []string{"gate", "stop"}}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrLifecycleBusy {
		t.Fatalf("err=%v, want LIFECYCLE_BUSY", err)
	}
}
