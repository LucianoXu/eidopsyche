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
