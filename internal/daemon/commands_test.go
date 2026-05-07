package daemon

import (
	"context"
	"strings"
	"testing"
)

func TestStatusCommand_RunsAndIncludesUptime(t *testing.T) {
	d := newTestDaemon(t)
	out, err := statusCommand(context.Background(), d, map[string]any{})
	if err != nil {
		t.Fatalf("statusCommand: %v", err)
	}
	if !strings.Contains(out, "uptime") {
		t.Fatalf("status output missing uptime: %q", out)
	}
	if !strings.Contains(out, "contacts:") {
		t.Fatalf("status output missing contacts line: %q", out)
	}
}

func TestDispatchCommand_UnknownReturnsNotOK(t *testing.T) {
	d := newTestDaemon(t)
	_, ok, err := dispatchCommand(context.Background(), d, "frobnicate", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for unknown command")
	}
}

func TestDispatchCommand_StatusOK(t *testing.T) {
	d := newTestDaemon(t)
	out, ok, err := dispatchCommand(context.Background(), d, "status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false on known command")
	}
	if out == "" {
		t.Fatal("empty status output")
	}
}
