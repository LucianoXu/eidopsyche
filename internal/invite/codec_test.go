package invite

import (
	"strings"
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

func TestEncodeDecodRoundtrip(t *testing.T) {
	sk := gnostr.GeneratePrivateKey()
	pk, _ := gnostr.GetPublicKey(sk)
	npub, _ := identity.EncodeNpub(pk)
	id, _ := RandomID()
	p := &Payload{
		V:                 1,
		IssuerNpub:        npub,
		IssuerRelay:       "ws://127.0.0.1:22895",
		IssuerLabelHint:   "Alice",
		RedeemerLabelHint: "friend",
		ID:                id,
		ExpiresAt:         time.Now().Add(7 * 24 * time.Hour).Unix(),
		MaxUses:           1,
	}
	if err := p.Sign(sk); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	uri, err := p.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(uri, TokenScheme) {
		t.Fatalf("URI missing scheme: %s", uri)
	}

	// Decode with full URI
	got, err := Decode(uri)
	if err != nil {
		t.Fatalf("Decode(full uri): %v", err)
	}
	if got.ID != p.ID {
		t.Fatalf("ID mismatch: want %s got %s", p.ID, got.ID)
	}
	if got.IssuerNpub != p.IssuerNpub {
		t.Fatalf("IssuerNpub mismatch")
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("Verify after decode: %v", err)
	}

	// Decode with bare base64 body (no scheme prefix)
	bare := strings.TrimPrefix(uri, TokenScheme)
	got2, err := Decode(bare)
	if err != nil {
		t.Fatalf("Decode(bare): %v", err)
	}
	if got2.ID != p.ID {
		t.Fatalf("bare decode ID mismatch")
	}
}

func TestDecodeInvalidBase64(t *testing.T) {
	_, err := Decode(TokenScheme + "!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	import64 := "eyJub3Rqc29u" // base64url of "{notjson"
	_, err := Decode(TokenScheme + import64)
	// May fail on base64 or json; either way an error is expected
	if err == nil {
		t.Fatal("expected error for invalid json payload")
	}
}
