package card_test

import (
	"testing"

	"github.com/yingtexu/eidopsyche/internal/card"
)

func TestRoundtrip(t *testing.T) {
	original := card.Card{
		Npub:  "npub1abc",
		Relay: "wss://alice.host:22895",
		Label: "Alice",
	}
	uri, err := original.URI()
	if err != nil {
		t.Fatalf("URI() error: %v", err)
	}
	parsed, err := card.Parse(uri)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if parsed != original {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", parsed, original)
	}
}

func TestParseRejectsBadScheme(t *testing.T) {
	_, err := card.Parse("https://example.com")
	if err == nil {
		t.Fatal("expected error for bad scheme, got nil")
	}
}

func TestParseRejectsMissingNpub(t *testing.T) {
	_, err := card.Parse("mindgate://@wss%3A%2F%2Fhost")
	if err == nil {
		t.Fatal("expected error for missing npub, got nil")
	}
}
