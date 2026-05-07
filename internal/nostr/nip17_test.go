package nostr

import (
	"testing"

	gnostr "github.com/nbd-wtf/go-nostr"
)

func TestWrapKindCustomKind(t *testing.T) {
	aliceSK := gnostr.GeneratePrivateKey()
	bobSK := gnostr.GeneratePrivateKey()
	bobPK, err := gnostr.GetPublicKey(bobSK)
	if err != nil {
		t.Fatal(err)
	}
	const inviteKind = 25001
	wrap, rumorID, err := WrapKind(aliceSK, bobPK, `{"v":1}`, inviteKind)
	if err != nil {
		t.Fatalf("WrapKind: %v", err)
	}
	if wrap.Kind != 1059 {
		t.Fatalf("outer wrap kind %d, want 1059", wrap.Kind)
	}
	rumor, err := Unwrap(bobSK, wrap)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if rumor.Kind != inviteKind {
		t.Fatalf("inner rumor kind %d, want %d", rumor.Kind, inviteKind)
	}
	if rumor.Content != `{"v":1}` {
		t.Fatalf("content %q", rumor.Content)
	}
	if rumorID != "" && rumor.ID != rumorID {
		t.Fatalf("rumor id mismatch: want %s got %s", rumorID, rumor.ID)
	}
}

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
