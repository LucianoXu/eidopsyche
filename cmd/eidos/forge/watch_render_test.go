package forge

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func parseEv(t *testing.T, s string) transcript.Event {
	t.Helper()
	ev, err := transcript.ParseEvent([]byte(s))
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ev
}

func TestRenderEvent_System(t *testing.T) {
	ev := parseEv(t, `{"type":"system","subtype":"init","model":"claude-sonnet-4-6","tools":["Read","Bash"],"mcp_servers":[{"name":"gmail"}]}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	for _, want := range []string{"wake started", "claude-sonnet-4-6", "Read Bash", "MCP: gmail"} {
		if !strings.Contains(out, want) {
			t.Errorf("system render missing %q: %s", want, out)
		}
	}
}

func TestRenderEvent_AssistantText(t *testing.T) {
	ev := parseEv(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "assistant") {
		t.Errorf("assistant header missing: %s", out)
	}
	if !strings.Contains(out, "Hello world") {
		t.Errorf("text body missing: %s", out)
	}
}

func TestRenderEvent_ThinkingCollapsed(t *testing.T) {
	ev := parseEv(t, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"line1\nline2\nline3"}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "(3 lines, --thinking to expand)") {
		t.Errorf("thinking should be collapsed by default: %s", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("thinking content should not appear when collapsed: %s", out)
	}
}

func TestRenderEvent_ThinkingExpanded(t *testing.T) {
	ev := parseEv(t, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"step1\nstep2"}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{ShowThinking: true}), "\n")
	if !strings.Contains(out, "step1") || !strings.Contains(out, "step2") {
		t.Errorf("thinking content should appear when expanded: %s", out)
	}
}

func TestRenderEvent_ToolUse(t *testing.T) {
	ev := parseEv(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"path":"/eidos/inbox"}}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "tool: Read") {
		t.Errorf("tool name missing: %s", out)
	}
	if !strings.Contains(out, "/eidos/inbox") {
		t.Errorf("tool input summary missing: %s", out)
	}
}

func TestRenderEvent_ToolResultString(t *testing.T) {
	ev := parseEv(t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file contents here"}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "result") {
		t.Errorf("result header missing: %s", out)
	}
	if !strings.Contains(out, "file contents here") {
		t.Errorf("result body missing: %s", out)
	}
}

func TestRenderEvent_ToolResultBlocks(t *testing.T) {
	ev := parseEv(t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"two"},{"type":"text","text":"three"}]}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "two") || !strings.Contains(out, "three") {
		t.Errorf("result text blocks missing: %s", out)
	}
}

func TestRenderEvent_ToolResultTruncatesBigOutput(t *testing.T) {
	// Escape newlines for JSON. 100 lines of "line\n" should trigger
	// the >30 truncation threshold.
	body := strings.Repeat(`line\n`, 100)
	ev := parseEv(t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"`+body+`"}]}}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "truncated; --raw") {
		t.Errorf("truncation hint missing: %s", out)
	}
}

func TestRenderEvent_ResultSuccess(t *testing.T) {
	ev := parseEv(t, `{"type":"result","subtype":"success","total_cost_usd":0.0123,"duration_ms":42017,"is_error":false,"num_turns":3}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	for _, want := range []string{"done", "$0.0123", "42s", "3 turns", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("result render missing %q: %s", want, out)
		}
	}
}

func TestRenderEvent_ResultFailed(t *testing.T) {
	ev := parseEv(t, `{"type":"result","is_error":true,"duration_ms":1500,"num_turns":1}`)
	out := strings.Join(renderEvent(ev, renderOpts{}), "\n")
	if !strings.Contains(out, "failed") {
		t.Errorf("failed status missing: %s", out)
	}
	if !strings.Contains(out, "- ") { // cost dash
		t.Errorf("missing cost shows '-': %s", out)
	}
}

func TestRenderEvent_UnknownTypeIgnored(t *testing.T) {
	ev := parseEv(t, `{"type":"stream_event","event":{"type":"content_block_delta"}}`)
	if got := renderEvent(ev, renderOpts{}); got != nil {
		t.Errorf("stream_event should be silently ignored, got %v", got)
	}
}

func TestSummariseToolInput_KnownKeys(t *testing.T) {
	cases := map[string]string{
		`{"path":"/x"}`:                 "path: /x",
		`{"file_path":"/y","old":"a"}`:  "file_path: /y",
		`{"command":"ls -la"}`:          "command: ls -la",
		`{"url":"https://example.com"}`: "url: https://example.com",
	}
	for in, want := range cases {
		got := summariseToolInput([]byte(in))
		if got != want {
			t.Errorf("summariseToolInput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderListTableForWatch(t *testing.T) {
	cost := 0.05
	idx := transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abcdef1234", Reason: "mindgate", StartedAt: 1700000000, EndedAt: 1700000010, OK: true, CostUSD: &cost},
		{ID: "9876fed", Reason: "planned", StartedAt: 1699000000, OK: false, ExitCode: 0},
		{ID: "feeb", Reason: "heartbeat", StartedAt: 1698000000, OK: false, ExitCode: 7},
	}}
	out := strings.Join(renderListTableForWatch(idx, 0), "\n")
	for _, want := range []string{"ID         REASON", "abcdef12", "$0.0500", "crashed", "failed(7)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list table missing %q: %s", want, out)
		}
	}
}

func TestRenderListTableForWatch_Empty(t *testing.T) {
	out := renderListTableForWatch(transcript.Index{V: 1}, 0)
	if len(out) != 1 || !strings.Contains(out[0], "no wakes") {
		t.Errorf("empty index: %v", out)
	}
}

func TestRenderListTableForWatch_Limit(t *testing.T) {
	idx := transcript.Index{V: 1}
	for i := 0; i < 5; i++ {
		idx.Wakes = append(idx.Wakes, transcript.Entry{ID: string(rune('a' + i)), StartedAt: int64(i)})
	}
	got := renderListTableForWatch(idx, 2)
	// 1 header + 2 rows = 3 lines.
	if len(got) != 3 {
		t.Errorf("limit=2: got %d lines, want 3: %v", len(got), got)
	}
}
