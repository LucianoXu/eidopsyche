package mcp

import (
	"context"
	"testing"
)

func TestEidosStateGet_PassesPath(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"label": "alice"}}
	s := &Server{client: &Client{raw: fake}}

	_, out, err := s.eidosStateGet(context.Background(), nil, StateGetInput{Path: "identity"})
	if err != nil {
		t.Fatalf("eidosStateGet: %v", err)
	}
	if fake.lastMethod != "state.get" {
		t.Fatalf("method = %q, want state.get", fake.lastMethod)
	}
	params := fake.lastParams.(StateGetInput)
	if params.Path != "identity" {
		t.Fatalf("params.path = %q, want identity", params.Path)
	}
	if out.Value == nil {
		t.Fatal("Value is nil, want non-nil")
	}
}

func TestEidosStateGet_PropagatesError(t *testing.T) {
	fake := &fakeIPC{errResp: pathNotFound("config.missing")}
	s := &Server{client: &Client{raw: fake}}
	_, _, err := s.eidosStateGet(context.Background(), nil, StateGetInput{Path: "config.missing"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "[PATH_NOT_FOUND] config.missing" {
		t.Fatalf("err = %q", got)
	}
}
