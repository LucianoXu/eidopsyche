//go:build !windows

package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestParseClaudeVersion(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"claude-cli 2.1.39 (foo)", true},
		{"v2.1.39", true},
		{"2.1.0", true},     // exactly the floor
		{"2.0.99", false},   // below floor
		{"1.99.999", false}, // below floor
		{"3.0.0-beta1", true},
		{"no version here", false},
		{"", false},
	}
	for _, c := range cases {
		if got := parseClaudeVersion(c.in); got != c.want {
			t.Errorf("parseClaudeVersion(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPlainClaudeArgs(t *testing.T) {
	in := []string{
		"--append-system-prompt", "x",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"-p", "msg",
	}
	want := []string{
		"--append-system-prompt", "x",
		"--dangerously-skip-permissions",
		"-p", "msg",
	}
	got := plainClaudeArgs(in)
	if !slicesEqualStr(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDrainStreamJSON_TeesAndCounts(t *testing.T) {
	src := strings.NewReader(
		`{"type":"system","subtype":"init","model":"x","tools":["Read"]}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"a"},{"type":"tool_use","name":"Read"}]}}` + "\n" +
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}` + "\n" +
			`{"type":"result","total_cost_usd":0.012,"duration_ms":3000,"is_error":false,"num_turns":2}` + "\n",
	)
	var dst bytes.Buffer
	counter := &transcript.Counter{}
	if err := drainStreamJSON(src, &dst, counter, "abc"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dst.String(), `"type":"system"`) {
		t.Errorf("tee did not write system event: %q", dst.String())
	}
	if counter.ToolUseCount != 2 {
		t.Errorf("tool_use_count: got %d, want 2", counter.ToolUseCount)
	}
	if counter.ThinkingBlocks != 1 {
		t.Errorf("thinking_blocks: got %d, want 1", counter.ThinkingBlocks)
	}
	if counter.Result == nil || !counter.Result.OK {
		t.Errorf("result missing or not ok: %+v", counter.Result)
	}
}

func TestDrainStreamJSON_LargeLine(t *testing.T) {
	bigContent := strings.Repeat("x", 1024*1024) // 1 MiB
	src := strings.NewReader(
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"` + bigContent + `"}]}}` + "\n" +
			`{"type":"result","is_error":false}` + "\n",
	)
	var dst bytes.Buffer
	counter := &transcript.Counter{}
	if err := drainStreamJSON(src, &dst, counter, "big"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dst.String(), bigContent) {
		t.Errorf("big content not preserved through tee")
	}
	if counter.Result == nil || !counter.Result.OK {
		t.Errorf("result missing: %+v", counter.Result)
	}
}

func TestDrainStreamJSON_TolerateMalformedLines(t *testing.T) {
	src := strings.NewReader(
		`{"type":"system"}` + "\n" +
			`not json at all` + "\n" +
			`{"type":"result","is_error":true}` + "\n",
	)
	var dst bytes.Buffer
	counter := &transcript.Counter{}
	if err := drainStreamJSON(src, &dst, counter, "x"); err != nil {
		t.Fatalf("malformed lines should not fail the drain: %v", err)
	}
	if counter.Result == nil {
		t.Errorf("result event not observed past malformed line")
	}
	if !strings.Contains(dst.String(), `not json at all`) {
		t.Errorf("malformed line should still be tee'd to file: %q", dst.String())
	}
}

// streamFixture configures the agent-runner test environment: stub
// claude binary on PATH + temp transcripts dir + lowered version floor.
// Returns the transcripts dir for assertion convenience.
func streamFixture(t *testing.T, runBody string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "stub 9.9.9"
  exit 0
fi
` + runBody + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	prevBin, prevTr, prevMin := claudeBin, transcriptsRuntimeDir, claudeMinVersion
	claudeBin = bin
	transcriptsRuntimeDir = t.TempDir()
	claudeMinVersion = [3]int{0, 0, 1}
	t.Cleanup(func() {
		claudeBin = prevBin
		transcriptsRuntimeDir = prevTr
		claudeMinVersion = prevMin
	})
	return transcriptsRuntimeDir
}

func TestRunWithTranscript_PersistsAndIndexesWake(t *testing.T) {
	trDir := streamFixture(t, `printf '%s\n' \
  '{"type":"system","subtype":"init","model":"claude-sonnet-4-6","tools":["Read","Bash"],"mcp_servers":[]}' \
  '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Read","id":"t1","input":{"path":"/tmp"}}]}}' \
  '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file"}]}}' \
  '{"type":"result","subtype":"success","total_cost_usd":0.001,"duration_ms":42,"is_error":false,"num_turns":1}'
exit 0`)

	wakeID := "test-wake-1"
	sig := wake.Signal{V: 1, ID: wakeID, Reason: wake.ReasonMindGate, TriggeredAt: 1}
	if err := runWithTranscript(sig, t.TempDir(), []string{"-p", "ignored"}); err != nil {
		t.Fatalf("runWithTranscript: %v", err)
	}

	store, _ := transcript.NewStore(trDir)
	body, err := os.ReadFile(store.WakePath(wakeID))
	if err != nil {
		t.Fatalf("transcript file missing: %v", err)
	}
	if !strings.Contains(string(body), `"type":"system"`) || !strings.Contains(string(body), `"type":"result"`) {
		t.Errorf("transcript content unexpected: %q", body)
	}

	idx, err := store.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].ID != wakeID {
		t.Fatalf("index missing entry: %+v", idx)
	}
	e := idx.Wakes[0]
	if !e.OK {
		t.Errorf("entry should be OK")
	}
	if e.Reason != "mindgate" {
		t.Errorf("entry.Reason = %q", e.Reason)
	}
	if e.ToolUseCount != 1 {
		t.Errorf("entry.ToolUseCount = %d", e.ToolUseCount)
	}
	if e.CostUSD == nil || *e.CostUSD != 0.001 {
		t.Errorf("entry.CostUSD = %v", e.CostUSD)
	}
	if e.SizeBytes == 0 {
		t.Errorf("entry.SizeBytes not populated")
	}
	if _, err := os.Lstat(store.CurrentPath()); err == nil {
		t.Errorf("current symlink should be cleared after wake")
	}
}

func TestRunWithTranscript_RecordsCrashedExit(t *testing.T) {
	trDir := streamFixture(t, `printf '%s\n' \
  '{"type":"system","subtype":"init"}' \
  '{"type":"result","is_error":true}'
exit 7`)

	sig := wake.Signal{V: 1, ID: "crash-1", Reason: wake.ReasonHeartBeat, TriggeredAt: 1}
	if err := runWithTranscript(sig, t.TempDir(), []string{"-p", "x"}); err == nil {
		t.Errorf("expected non-nil error from non-zero exit")
	}

	store, _ := transcript.NewStore(trDir)
	idx, _ := store.ReadIndex()
	if len(idx.Wakes) != 1 || idx.Wakes[0].OK || idx.Wakes[0].ExitCode != 7 {
		t.Errorf("wrong index state: %+v", idx.Wakes)
	}
}
