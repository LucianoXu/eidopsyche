package nostr

import (
	"errors"
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

// TestUnwrap_RejectsNilEvent guards against a nil-pointer deref / silent
// pass-through when the caller hands us a nil event.
func TestUnwrap_RejectsNilEvent(t *testing.T) {
	bobSK := gnostr.GeneratePrivateKey()
	if _, err := Unwrap(bobSK, nil); !errors.Is(err, ErrNotGiftWrap) {
		t.Fatalf("err = %v, want %v", err, ErrNotGiftWrap)
	}
}

// TestUnwrap_RejectsWrongKind: a forged event whose outer Kind != 1059 must
// be rejected up-front, before any decryption attempt. The subscription
// filter constrains kind = 1059 for inbound, but Unwrap is reused from
// other paths (and relays are untrusted), so the helper must self-validate.
func TestUnwrap_RejectsWrongKind(t *testing.T) {
	aliceSK := gnostr.GeneratePrivateKey()
	bobSK := gnostr.GeneratePrivateKey()
	bobPK, err := gnostr.GetPublicKey(bobSK)
	if err != nil {
		t.Fatal(err)
	}
	wrap, _, err := Wrap(aliceSK, bobPK, "hi bob")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	// Mutate the outer wrap to a different kind (NIP-04 legacy DM kind).
	wrap.Kind = 4
	if _, err := Unwrap(bobSK, wrap); !errors.Is(err, ErrNotGiftWrap) {
		t.Fatalf("err = %v, want %v", err, ErrNotGiftWrap)
	}
}

// TestUnwrap_RejectsMissingPTag: a kind-1059 event with no "p" tag cannot
// be addressed to any receiver and must be rejected.
func TestUnwrap_RejectsMissingPTag(t *testing.T) {
	aliceSK := gnostr.GeneratePrivateKey()
	bobSK := gnostr.GeneratePrivateKey()
	bobPK, err := gnostr.GetPublicKey(bobSK)
	if err != nil {
		t.Fatal(err)
	}
	wrap, _, err := Wrap(aliceSK, bobPK, "hi bob")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	// Strip all p tags from the outer wrap.
	filtered := wrap.Tags[:0]
	for _, tag := range wrap.Tags {
		if len(tag) >= 1 && tag[0] == "p" {
			continue
		}
		filtered = append(filtered, tag)
	}
	wrap.Tags = filtered
	if _, err := Unwrap(bobSK, wrap); !errors.Is(err, ErrGiftWrapNotForReceiver) {
		t.Fatalf("err = %v, want %v", err, ErrGiftWrapNotForReceiver)
	}
}

// TestUnwrap_RejectsForeignPTag: a kind-1059 event whose p-tag points to a
// pubkey OTHER than the receiver's must be rejected before any decryption.
// Stops a malicious relay from feeding us wraps addressed to someone else
// to provoke decryption errors (or worse, oracle behavior) under our key.
func TestUnwrap_RejectsForeignPTag(t *testing.T) {
	aliceSK := gnostr.GeneratePrivateKey()
	bobSK := gnostr.GeneratePrivateKey()
	bobPK, err := gnostr.GetPublicKey(bobSK)
	if err != nil {
		t.Fatal(err)
	}
	// Carol is the actual addressee; Bob is the caller trying to Unwrap.
	carolSK := gnostr.GeneratePrivateKey()
	carolPK, err := gnostr.GetPublicKey(carolSK)
	if err != nil {
		t.Fatal(err)
	}
	wrap, _, err := Wrap(aliceSK, bobPK, "hi bob")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	// Rewrite the p tag to point at Carol.
	for i, tag := range wrap.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			wrap.Tags[i] = gnostr.Tag{"p", carolPK}
			break
		}
	}
	if _, err := Unwrap(bobSK, wrap); !errors.Is(err, ErrGiftWrapNotForReceiver) {
		t.Fatalf("err = %v, want %v", err, ErrGiftWrapNotForReceiver)
	}
}
