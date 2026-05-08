package relayd

import (
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
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	cf, err := os.Create(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	cf.Close()
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	kf, err := os.Create(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	kf.Close()
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
