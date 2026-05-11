package invite

import (
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

func makePayload(t *testing.T, sk string) (*Payload, error) {
	t.Helper()
	pk, err := gnostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	npub, err := identity.EncodeNpub(pk)
	if err != nil {
		t.Fatalf("EncodeNpub: %v", err)
	}
	id, err := RandomID()
	if err != nil {
		t.Fatalf("RandomID: %v", err)
	}
	return &Payload{
		V:                 1,
		IssuerNpub:        npub,
		IssuerRelay:       "ws://127.0.0.1:22895",
		IssuerLabelHint:   "Alice",
		RedeemerLabelHint: "friend",
		ID:                id,
		ExpiresAt:         time.Now().Add(7 * 24 * time.Hour).Unix(),
		MaxUses:           1,
	}, nil
}

func TestSignVerify(t *testing.T) {
	sk := gnostr.GeneratePrivateKey()
	p, err := makePayload(t, sk)
	if err != nil {
		t.Fatalf("makePayload: %v", err)
	}
	if err := p.Sign(sk); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if p.Sig == "" {
		t.Fatal("Sig should not be empty after Sign")
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	sk := gnostr.GeneratePrivateKey()
	sk2 := gnostr.GeneratePrivateKey()
	p, _ := makePayload(t, sk)
	// sign with sk but use sk2's npub — Sign should reject this
	pk2, _ := gnostr.GetPublicKey(sk2)
	npub2, _ := identity.EncodeNpub(pk2)
	p.IssuerNpub = npub2
	if err := p.Sign(sk); err == nil {
		t.Fatal("Sign with mismatched key should return error")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	sk := gnostr.GeneratePrivateKey()
	p, _ := makePayload(t, sk)
	_ = p.Sign(sk)
	// Tamper MaxUses after signing
	p.MaxUses = 999
	if err := p.Verify(); err == nil {
		t.Fatal("Verify should fail after tamper")
	}
}

func TestCanonicalForSigningIsDeterministic(t *testing.T) {
	sk := gnostr.GeneratePrivateKey()
	p, _ := makePayload(t, sk)
	b1, _ := p.CanonicalForSigning()
	b2, _ := p.CanonicalForSigning()
	if string(b1) != string(b2) {
		t.Fatalf("canonical JSON is not deterministic")
	}
}

func TestRandomIDLength(t *testing.T) {
	id, err := RandomID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 64 {
		t.Fatalf("expected 64-char hex, got %d", len(id))
	}
}
