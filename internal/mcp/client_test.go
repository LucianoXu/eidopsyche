package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestClient_Call_TypedDaemonError(t *testing.T) {
	fake := &fakeIPC{
		errResp: &ipc.Error{Code: "CONTACT_NOT_FOUND", Message: `no contact with label "alice"`},
	}
	c := &Client{raw: fake}
	err := c.Call(context.Background(), "contact.remove", map[string]any{"label": "alice"}, nil)
	if err == nil {
		t.Fatal("Call returned nil, want error")
	}
	want := `[CONTACT_NOT_FOUND] no contact with label "alice"`
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestClient_Call_TransportError(t *testing.T) {
	fake := &fakeIPC{transportErr: errors.New("broken pipe")}
	c := &Client{raw: fake}
	err := c.Call(context.Background(), "send", nil, nil)
	if err == nil {
		t.Fatal("Call returned nil, want error")
	}
	if got := err.Error(); got != "[IPC_TRANSPORT] broken pipe" {
		t.Fatalf("err = %q, want %q", got, "[IPC_TRANSPORT] broken pipe")
	}
}

func TestClient_Call_RoundTripParams(t *testing.T) {
	fake := &fakeIPC{}
	c := &Client{raw: fake}
	_ = c.Call(context.Background(), "set-label", map[string]any{"label": "alice"}, nil)
	if fake.lastMethod != "set-label" {
		t.Fatalf("method = %q, want set-label", fake.lastMethod)
	}
	if got := fake.lastParams.(map[string]any)["label"]; got != "alice" {
		t.Fatalf("params.label = %v, want alice", got)
	}
}
