package mcp

import (
	"context"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

func TestEidosSend_BuildsEnvelope(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{
		"event_id":    "abc123",
		"accepted_by": []string{"wss://relay.example"},
	}}
	s := &Server{client: &Client{raw: fake}}
	_, out, err := s.eidosSend(context.Background(), nil, SendInput{To: "alice", Body: "hello"})
	if err != nil {
		t.Fatalf("eidosSend: %v", err)
	}
	if fake.lastMethod != "send" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	sp := fake.lastParams.(daemon.SendParams)
	if sp.To != "alice" {
		t.Fatalf("To = %q, want alice", sp.To)
	}
	if sp.Envelope == nil || sp.Envelope.Type != envelope.TypeChat || sp.Envelope.Text != "hello" {
		t.Fatalf("envelope = %+v, want {chat, hello}", sp.Envelope)
	}
	if sp.Envelope.V != envelope.SchemaVersion {
		t.Fatalf("envelope.V = %d, want %d", sp.Envelope.V, envelope.SchemaVersion)
	}
	if out.EventID != "abc123" {
		t.Fatalf("EventID = %q", out.EventID)
	}
}

func TestEidosSend_EmptyBodyRejected(t *testing.T) {
	s := &Server{client: &Client{raw: &fakeIPC{}}}
	_, _, err := s.eidosSend(context.Background(), nil, SendInput{To: "alice", Body: ""})
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if got := err.Error(); got != "[INVALID_PARAMS] body must be non-empty" {
		t.Fatalf("err = %q", got)
	}
}

func TestEidosInboxList_PassesFilters(t *testing.T) {
	fake := &fakeIPC{result: []any{}}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosInboxList(context.Background(), nil, InboxListInput{Since: 100, From: "alice", Sender: "known", Limit: 10})
	if err != nil {
		t.Fatalf("eidosInboxList: %v", err)
	}
	if fake.lastMethod != "inbox.list" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
}

func TestEidosOutboxList_Routes(t *testing.T) {
	fake := &fakeIPC{result: []any{}}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosOutboxList(context.Background(), nil, OutboxListInput{To: "alice", Limit: 5})
	if err != nil {
		t.Fatalf("eidosOutboxList: %v", err)
	}
	if fake.lastMethod != "outbox.list" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
}
