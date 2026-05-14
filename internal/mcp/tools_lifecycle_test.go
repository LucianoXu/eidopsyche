package mcp

import (
	"context"
	"testing"
)

func TestEidosLifecycleStatus_Routes(t *testing.T) {
	fake := &fakeIPC{result: map[string]any{"job_id": "", "running": false}}
	s := &Server{client: &Client{raw: fake}}
	_, out, err := s.eidosLifecycleStatus(context.Background(), nil, LifecycleStatusInput{})
	if err != nil {
		t.Fatalf("eidosLifecycleStatus: %v", err)
	}
	if fake.lastMethod != "lifecycle.status" {
		t.Fatalf("method = %q", fake.lastMethod)
	}
	if out.Snapshot == nil {
		t.Fatal("Snapshot nil")
	}
}
