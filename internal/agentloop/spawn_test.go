// internal/agentloop/spawn_test.go
package agentloop

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestSpawnClaude_StreamJSONInputProcessesOneTurn(t *testing.T) {
	c, err := SpawnClaude(SpawnOpts{
		Binary:         stubClaudeBin,
		Mode:           SessionNew,
		SessionUUID:    "test-session",
		IdentityPrompt: "test mindform",
		Cwd:            t.TempDir(),
		ClaudeDir:      t.TempDir(),
		ExtraArgs:      []string{"--mode", "normal"},
	})
	if err != nil {
		t.Fatalf("SpawnClaude: %v", err)
	}
	defer c.Wait()

	if _, err := c.Stdin.Write([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"ping"}]}}` + "\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	r := bufio.NewReader(c.Stdout)
	saw := map[string]int{}
	for i := 0; i < 3; i++ {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("read line %d: %v", i, err)
		}
		switch {
		case strings.Contains(line, `"type":"system"`):
			saw["system"]++
		case strings.Contains(line, `"type":"assistant"`):
			saw["assistant"]++
		case strings.Contains(line, `"type":"result"`):
			saw["result"]++
		}
	}
	if saw["system"] != 1 || saw["assistant"] != 1 || saw["result"] != 1 {
		t.Errorf("event counts: %+v (want one each)", saw)
	}

	c.Stdin.Close()
}

func TestEncodeCWD(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/eidos/mindforms/alice", "-eidos-mindforms-alice"},
		{"/home/user/my-dir", "-home-user-my-dir"},
		{"simple", "simple"},
		{"", ""},
	}
	for _, tc := range cases {
		got := EncodeCWD(tc.in)
		if got != tc.want {
			t.Errorf("EncodeCWD(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestSessionJsonlPath(t *testing.T) {
	path := SessionJsonlPath("/eidos/mindforms/alice", "my-uuid-123")
	want := "/eidos/mindforms/alice/.claude/projects/-eidos-mindforms-alice/my-uuid-123.jsonl"
	if path != want {
		t.Errorf("SessionJsonlPath = %q; want %q", path, want)
	}
}

func TestSessionJsonlPath_EmptyDir(t *testing.T) {
	path := SessionJsonlPath("", "some-uuid")
	if path != "" {
		t.Errorf("SessionJsonlPath with empty dir = %q; want empty", path)
	}
}
