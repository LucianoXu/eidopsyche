package mcp

import (
	"context"
	"testing"
)

func TestEidosCardParse_Routes(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"npub": "npub1abc", "label": "alice"}}
	s := &Server{client: &Client{raw: fake}}
	_, out, err := s.eidosCardParse(context.Background(), nil, CardParseInput{URI: "mindgate://x"})
	if err != nil {
		t.Fatalf("eidosCardParse: %v", err)
	}
	if fake.lastMethod != "card.parse" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	if fake.lastParams.(CardParseInput).URI != "mindgate://x" {
		t.Fatalf("uri mismatch: %+v", fake.lastParams)
	}
	if out.Card == nil {
		t.Fatal("Card nil")
	}
}

func TestEidosCardScan_Routes(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"npub": "npub1abc", "known": true}}
	s := &Server{client: &Client{raw: fake}}
	_, out, err := s.eidosCardScan(context.Background(), nil, CardScanInput{URI: "mindgate://y"})
	if err != nil {
		t.Fatalf("eidosCardScan: %v", err)
	}
	if fake.lastMethod != "card.scan" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	if out.Result == nil {
		t.Fatal("Result nil")
	}
}
