package mcp

import (
	"context"
	"testing"
)

func TestEidosRelay_Routing(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"ok": true}}
	s := &Server{client: &Client{raw: fake}}

	_, _, err := s.eidosRelayAdd(context.Background(), nil, RelayAddInput{URL: "wss://r.example", Role: "home"})
	if err != nil {
		t.Fatalf("eidosRelayAdd: %v", err)
	}
	if fake.lastMethod != "relay.add" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	p := fake.lastParams.(RelayAddInput)
	if p.URL != "wss://r.example" || p.Role != "home" {
		t.Fatalf("params = %+v", p)
	}

	_, _, err = s.eidosRelayRemove(context.Background(), nil, RelayRemoveInput{URL: "wss://r.example"})
	if err != nil {
		t.Fatalf("eidosRelayRemove: %v", err)
	}
	if fake.lastMethod != "relay.remove" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
}
