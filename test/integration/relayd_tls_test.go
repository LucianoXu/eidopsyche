//go:build integration

package integration

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

	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

func mkSelfSignedCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
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
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	cf, _ := os.Create(certPath)
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	cf.Close()
	keyDER, _ := x509.MarshalECPrivateKey(priv)
	kf, _ := os.Create(keyPath)
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	kf.Close()
	return certPath, keyPath
}

// TestRelayd_TLS_NostrClientConnects verifies the wss:// path through a
// real go-nostr Relay connection: the relay terminates TLS itself
// (BYO cert), the client dials with InsecureSkipVerify (test-only,
// because the cert is self-signed), opens a Subscribe, and reaches
// EOSE — proving the full TLS+WebSocket+Nostr stack works end-to-end.
func TestRelayd_TLS_NostrClientConnects(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	certPath, keyPath := mkSelfSignedCert(t)
	addr := freePort(t)
	srv, err := relayd.New(relayd.Config{
		Mode:   relayd.ModePublic,
		Listen: addr,
		TLS:    relayd.TLSConfig{CertFile: certPath, KeyFile: keyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := gnostr.NewRelay(ctx, "wss://"+addr)
	if err := r.ConnectWithTLS(ctx, &tls.Config{InsecureSkipVerify: true}); err != nil {
		t.Fatalf("connect wss: %v", err)
	}
	defer r.Close()

	pk := gnostr.GeneratePrivateKey()
	pub, _ := gnostr.GetPublicKey(pk)
	sub, err := r.Subscribe(ctx, gnostr.Filters{{
		Kinds: []int{1059},
		Tags:  gnostr.TagMap{"p": []string{pub}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sub.EndOfStoredEvents:
		// success — the relay accepted the wss connection, parsed the
		// REQ, and signaled EOSE for an empty store.
	case reason := <-sub.ClosedReason:
		t.Fatalf("subscription closed unexpectedly: %s", reason)
	case <-time.After(3 * time.Second):
		t.Fatal("never reached EOSE within 3s")
	}
}
