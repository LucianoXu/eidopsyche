package nostr

import (
	"testing"

	gnostr "github.com/nbd-wtf/go-nostr"
)

func TestWrapUnwrap(t *testing.T) {
	aliceSK := gnostr.GeneratePrivateKey()
	bobSK := gnostr.GeneratePrivateKey()
	bobPK, err := gnostr.GetPublicKey(bobSK)
	if err != nil {
		t.Fatal(err)
	}
	wrap, rumorID, err := Wrap(aliceSK, bobPK, "hi bob")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if wrap.Kind != 1059 {
		t.Fatalf("wrap kind %d", wrap.Kind)
	}
	rumor, err := Unwrap(bobSK, wrap)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if rumor.Content != "hi bob" {
		t.Fatalf("content %q", rumor.Content)
	}
	if rumorID != "" && rumor.ID != rumorID {
		t.Fatalf("rumor id mismatch: want %s got %s", rumorID, rumor.ID)
	}
	alicePK, _ := gnostr.GetPublicKey(aliceSK)
	if rumor.PubKey != alicePK {
		t.Fatalf("sender %q != alice %q", rumor.PubKey, alicePK)
	}
}
