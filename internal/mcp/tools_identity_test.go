package mcp

import (
	"context"
	"testing"
)

func TestEidosSetLabel_PassesLabel(t *testing.T) {
	fake := &fakeIPC{}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosSetLabel(context.Background(), nil, SetLabelInput{Label: "alice"})
	if err != nil {
		t.Fatalf("eidosSetLabel: %v", err)
	}
	if fake.lastMethod != "set-label" {
		t.Fatalf("method = %q, want set-label", fake.lastMethod)
	}
	if got := fake.lastParams.(SetLabelInput).Label; got != "alice" {
		t.Fatalf("label = %q, want alice", got)
	}
}

func TestEidosServiceStatus_RoutesAndDecodes(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{
		"socket":     "/eidos/gate/sock",
		"started_at": "2026-05-14T00:00:00Z",
		"version":    map[string]any{"version": "0.7.1"},
	}}
	s := &Server{client: &Client{raw: fake}}
	_, out, err := s.eidosServiceStatus(context.Background(), nil, ServiceStatusInput{})
	if err != nil {
		t.Fatalf("eidosServiceStatus: %v", err)
	}
	if fake.lastMethod != "service.status" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	if out.Snapshot == nil {
		t.Fatal("Snapshot is nil")
	}
}

func TestEidosAgentState_Routes(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"claude_busy": true}}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosAgentState(context.Background(), nil, AgentStateInput{})
	if err != nil {
		t.Fatalf("eidosAgentState: %v", err)
	}
	if fake.lastMethod != "agent.state" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
}
