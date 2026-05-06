package identity

import (
	"strings"
	"testing"
)

func TestGenerateRoundtrip(t *testing.T) {
	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(k.PrivateHex) != 64 {
		t.Fatalf("private hex length: got %d want 64", len(k.PrivateHex))
	}
	if len(k.PublicHex) != 64 {
		t.Fatalf("public hex length: got %d want 64", len(k.PublicHex))
	}
	if !strings.HasPrefix(k.Npub, "npub1") {
		t.Fatalf("npub prefix wrong: %q", k.Npub)
	}
	if !strings.HasPrefix(k.Nsec, "nsec1") {
		t.Fatalf("nsec prefix wrong: %q", k.Nsec)
	}
	// Re-derive pubkey from private and confirm match.
	derived, err := DerivePublic(k.PrivateHex)
	if err != nil {
		t.Fatalf("DerivePublic: %v", err)
	}
	if derived != k.PublicHex {
		t.Fatalf("derived %q != stored %q", derived, k.PublicHex)
	}
}

func TestDecodeNpubRoundtrip(t *testing.T) {
	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hex, err := DecodeNpub(k.Npub)
	if err != nil {
		t.Fatalf("DecodeNpub: %v", err)
	}
	if hex != k.PublicHex {
		t.Fatalf("hex %q != %q", hex, k.PublicHex)
	}
}
