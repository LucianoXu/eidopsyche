package relayd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/store"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestPairedModeRejection(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := gnostr.GeneratePrivateKey()
	ownerPK, _ := gnostr.GetPublicKey(owner)
	if err := db.SetMeta(ctx, "owner_pubkey", ownerPK); err != nil {
		t.Fatal(err)
	}
	wl := NewWhitelistSource(db, 100*time.Millisecond)
	if err := wl.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}

	addr := freePort(t)
	srv, err := New(Config{
		Mode:      ModePaired,
		Listen:    addr,
		OwnerHex:  ownerPK,
		Whitelist: wl,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(200 * time.Millisecond)

	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// A kind:1 event addressed to nobody should be rejected.
	stranger := gnostr.GeneratePrivateKey()
	strangerPK, _ := gnostr.GetPublicKey(stranger)
	ev := gnostr.Event{
		Kind:      1,
		PubKey:    strangerPK,
		CreatedAt: gnostr.Now(),
		Content:   "hi",
	}
	if err := ev.Sign(stranger); err != nil {
		t.Fatal(err)
	}
	err = relay.Publish(ctx, ev)
	if err == nil {
		t.Fatal("expected publish rejection for non-1059 event")
	}
}

// generateSelfSignedCert writes a fresh self-signed cert + key into dir
// and returns the (certPath, keyPath). Used by TLS startup tests.
//
// Errors at every step are surfaced via t.Fatal — a partially-written
// cert / key surfaces several lines later as an opaque TLS startup
// failure, which is much harder to diagnose than the underlying I/O
// error.
func generateSelfSignedCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	var certBuf, keyBuf bytes.Buffer
	if err := pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(&keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestRelayd_TLS_BothSetServesHTTPS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		TLS:    TLSConfig{CertFile: certPath, KeyFile: keyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	defer srv.Shutdown(context.Background())

	// Wait briefly for the listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("TLS dial never succeeded; relay never came up")
}

func TestRelayd_TLS_OnlyCertSet_Errors(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := generateSelfSignedCert(t, dir)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: freePort(t),
		TLS:    TLSConfig{CertFile: certPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenAndServe(); err == nil {
		t.Fatal("expected error when only CertFile is set")
	}
}

func TestRelayd_TLS_OnlyKeySet_Errors(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := generateSelfSignedCert(t, dir)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: freePort(t),
		TLS:    TLSConfig{KeyFile: keyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenAndServe(); err == nil {
		t.Fatal("expected error when only KeyFile is set")
	}
}

func TestRelayd_Auth_RejectsUnauthenticatedKind1059(t *testing.T) {
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		Auth:   AuthConfig{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// Generate a target pubkey to filter on.
	sk := gnostr.GeneratePrivateKey()
	target, _ := gnostr.GetPublicKey(sk)

	sub, err := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{target}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case reason := <-sub.ClosedReason:
		if want := "auth-required"; !contains(reason, want) {
			t.Fatalf("unauthenticated kind:1059 REQ closed with %q; want substring %q", reason, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected ClosedReason within 3s; subscription remained open")
	}
}

func TestRelayd_Auth_AcceptsAuthenticatedMatchingP(t *testing.T) {
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		Auth:   AuthConfig{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// Start a subscription first so the relay sends an AUTH challenge,
	// populating relay.challenge for the subsequent Auth() call.
	sk := gnostr.GeneratePrivateKey()
	pk, _ := gnostr.GetPublicKey(sk)
	sub, err := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pk}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Wait briefly for the relay to send AUTH challenge + first CLOSED;
	// drain the close so it doesn't pollute later state.
	select {
	case <-sub.ClosedReason:
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive initial CLOSED before AUTH")
	}

	// Now AUTH as pk.
	if err := relay.Auth(ctx, func(ev *gnostr.Event) error {
		return ev.Sign(sk)
	}); err != nil {
		t.Fatalf("AUTH failed: %v", err)
	}

	// Re-subscribe — this time the AUTH'd connection should accept the REQ.
	sub2, err := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pk}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sub2.EndOfStoredEvents:
		// EOSE = relay accepted the REQ and signaled it has no stored events for us. Pass.
	case reason := <-sub2.ClosedReason:
		t.Fatalf("authenticated REQ closed unexpectedly: %s", reason)
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated REQ never reached EOSE within 3s")
	}
}

func TestRelayd_Auth_RejectsAuthenticatedMismatchedP(t *testing.T) {
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		Auth:   AuthConfig{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	skMe := gnostr.GeneratePrivateKey()
	pkMe, _ := gnostr.GetPublicKey(skMe)
	skOther := gnostr.GeneratePrivateKey()
	pkOther, _ := gnostr.GetPublicKey(skOther)

	// Trigger AUTH challenge.
	sub, _ := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pkMe}},
	}})
	<-sub.ClosedReason

	if err := relay.Auth(ctx, func(ev *gnostr.Event) error {
		return ev.Sign(skMe)
	}); err != nil {
		t.Fatalf("AUTH failed: %v", err)
	}

	// REQ for someone else's events: must be rejected as auth-mismatch.
	sub2, err := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pkOther}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-sub2.ClosedReason:
		if !contains(reason, "auth-mismatch") {
			t.Fatalf("got %q; want substring auth-mismatch", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected auth-mismatch CLOSED within 3s")
	}
}

// Regression test: NIP-01 says an empty/absent Kinds list matches all
// kinds (including 1059). Without this gate, an unauthenticated client
// could read every gift wrap by simply omitting kinds from the REQ.
func TestRelayd_Auth_RejectsUnauthenticatedEmptyKinds(t *testing.T) {
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		Auth:   AuthConfig{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// REQ with no kinds, only a #p tag. NIP-01 says this matches all kinds
	// including 1059 — must be rejected as auth-required.
	sk := gnostr.GeneratePrivateKey()
	pk, _ := gnostr.GetPublicKey(sk)
	sub, err := relay.Subscribe(ctx, gnostr.Filters{{
		Tags: gnostr.TagMap{"p": []string{pk}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-sub.ClosedReason:
		if !contains(reason, "auth-required") {
			t.Fatalf("unauth empty-kinds REQ closed with %q; want auth-required", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unauth empty-kinds REQ was not rejected — bypass!")
	}
}

func TestRelayd_Auth_NotRequired_AcceptsUnauth(t *testing.T) {
	addr := freePort(t)
	srv, err := New(Config{
		Mode:   ModePublic,
		Listen: addr,
		Auth:   AuthConfig{Required: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	sk := gnostr.GeneratePrivateKey()
	pk, _ := gnostr.GetPublicKey(sk)
	sub, err := relay.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pk}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sub.EndOfStoredEvents:
	case reason := <-sub.ClosedReason:
		t.Fatalf("unauth REQ unexpectedly closed: %s", reason)
	case <-time.After(2 * time.Second):
		t.Fatal("unauth REQ never reached EOSE")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
