//go:build !windows

package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildWakeMessage(t *testing.T) {
	got := buildWakeMessage(wakePromptInput{
		Reason:               "mindgate",
		Hint:                 "Alice sent: hello",
		InboxUnread:          1,
		SinceLastWakeSeconds: 60,
	})
	for _, want := range []string{"You have just woken", "mindgate", "Alice sent", "1 unread"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("wake message missing %q; got: %s", want, got)
		}
	}
}

func TestAcquireLockExcludes(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "agent.lock")
	first, err := acquireAgentLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := acquireAgentLock(lockPath); err == nil {
		t.Errorf("second acquire should fail (locked)")
	}
	_ = os.Remove(lockPath)
}

func TestBuildClaudeArgs_NoModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("log_level = \"info\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildClaudeArgs("identity-text", "wake-msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity-text",
		"--dangerously-skip-permissions",
		"-p", "wake-msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_WithModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := "[mindform]\nmodel = \"claude-sonnet-4-7\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildClaudeArgs("identity", "msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity",
		"--dangerously-skip-permissions",
		"--model", "claude-sonnet-4-7",
		"-p", "msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_MissingConfig(t *testing.T) {
	got := buildClaudeArgs("ident", "m", "/nonexistent/path/config.toml")
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"-p", "m",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func slicesEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
