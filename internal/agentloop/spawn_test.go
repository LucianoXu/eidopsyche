//go:build !windows

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
		Binary:       stubClaudeBin,
		Mode:         SessionNew,
		SessionUUID:  "test-session",
		SystemPrompt: "test mindform",
		Cwd:          t.TempDir(),
		ClaudeDir:    t.TempDir(),
		ExtraArgs:    []string{"--mode", "normal"},
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

func TestBuildClaudeArgs_StreamJSONContract(t *testing.T) {
	cases := []struct {
		name    string
		opts    SpawnOpts
		require []string // substrings that must appear in the joined argv
		exclude []string // substrings that must NOT appear
	}{
		{
			name: "new session has --session-id",
			opts: SpawnOpts{Mode: SessionNew, SessionUUID: "uuid-1", SystemPrompt: "x"},
			require: []string{
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose",
				"--include-partial-messages",
				"--session-id", "uuid-1",
				"--system-prompt", "x",
				"--dangerously-skip-permissions",
			},
			exclude: []string{"--resume"},
		},
		{
			name:    "resume session has --resume",
			opts:    SpawnOpts{Mode: SessionResume, SessionUUID: "uuid-2", SystemPrompt: "y"},
			require: []string{"--resume", "uuid-2"},
			exclude: []string{"--session-id"},
		},
		{
			name:    "model when non-empty",
			opts:    SpawnOpts{Mode: SessionNew, SessionUUID: "u", Model: "claude-opus-4-7", SystemPrompt: "x"},
			require: []string{"--model", "claude-opus-4-7"},
		},
		{
			name:    "effort when non-empty",
			opts:    SpawnOpts{Mode: SessionNew, SessionUUID: "u", Effort: "high", SystemPrompt: "x"},
			require: []string{"--effort", "high"},
		},
		{
			name:    "effort omitted when empty",
			opts:    SpawnOpts{Mode: SessionNew, SessionUUID: "u", SystemPrompt: "x"},
			exclude: []string{"--effort"},
		},
		{
			name:    "extra args appended last",
			opts:    SpawnOpts{Mode: SessionNew, SessionUUID: "u", SystemPrompt: "x", ExtraArgs: []string{"--mode", "normal"}},
			require: []string{"--mode", "normal"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := buildClaudeArgs(tc.opts)
			joined := strings.Join(args, " ")
			for _, want := range tc.require {
				if !strings.Contains(joined, want) {
					t.Errorf("missing required arg %q in: %s", want, joined)
				}
			}
			for _, no := range tc.exclude {
				if strings.Contains(joined, no) {
					t.Errorf("forbidden arg %q present in: %s", no, joined)
				}
			}
		})
	}
}

func TestSpawnedClaude_WaitIsIdempotent(t *testing.T) {
	c, err := SpawnClaude(SpawnOpts{
		Binary:       stubClaudeBin,
		Mode:         SessionNew,
		SessionUUID:  "test-idempotent",
		SystemPrompt: "test",
		Cwd:          t.TempDir(),
		ClaudeDir:    t.TempDir(),
		ExtraArgs:    []string{"--mode", "crash-after", "--at-n", "1"},
	})
	if err != nil {
		t.Fatalf("SpawnClaude: %v", err)
	}
	// Send one stdin line so the stub processes a turn, then crashes.
	_, _ = c.Stdin.Write([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"x"}]}}` + "\n"))
	c.Stdin.Close()

	// Drain stdout so the stub can exit.
	go func() { _, _ = io.Copy(io.Discard, c.Stdout) }()

	first := c.Wait()
	if first == nil {
		t.Fatalf("expected first Wait() to return crash exit error, got nil")
	}
	second := c.Wait()
	if second == nil {
		t.Errorf("expected second Wait() to return the same error, got nil (idempotency broken)")
	}
	if first.Error() != second.Error() {
		t.Errorf("Wait() not idempotent: first=%v, second=%v", first, second)
	}
}
