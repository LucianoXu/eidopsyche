package transcript

import (
	"testing"
)

func TestParseSystemInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","model":"claude-sonnet-4-6","tools":["Read","Bash"],"mcp_servers":[{"name":"gmail"}]}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "system" || ev.Subtype != "init" {
		t.Errorf("type/subtype: %q/%q", ev.Type, ev.Subtype)
	}
	if ev.Model != "claude-sonnet-4-6" {
		t.Errorf("model: %q", ev.Model)
	}
	if len(ev.Tools) != 2 || ev.Tools[0] != "Read" {
		t.Errorf("tools: %v", ev.Tools)
	}
	if len(ev.MCPServers) != 1 || ev.MCPServers[0].Name != "gmail" {
		t.Errorf("mcp_servers: %v", ev.MCPServers)
	}
}

func TestParseAssistantMessage(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think..."},{"type":"text","text":"Hello"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"/tmp"}}]}}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Message == nil || len(ev.Message.Content) != 3 {
		t.Fatalf("content blocks missing: %+v", ev.Message)
	}
	if ev.Message.Content[0].Type != "thinking" || ev.Message.Content[0].Thinking == "" {
		t.Errorf("thinking block: %+v", ev.Message.Content[0])
	}
	if ev.Message.Content[1].Type != "text" || ev.Message.Content[1].Text != "Hello" {
		t.Errorf("text block: %+v", ev.Message.Content[1])
	}
	if ev.Message.Content[2].Type != "tool_use" || ev.Message.Content[2].Name != "Read" {
		t.Errorf("tool_use block: %+v", ev.Message.Content[2])
	}
}

func TestParseUserToolResult(t *testing.T) {
	line := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file contents","is_error":false}]}}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Message == nil || len(ev.Message.Content) != 1 {
		t.Fatalf("content missing")
	}
	b := ev.Message.Content[0]
	if b.Type != "tool_result" || b.ToolUseID != "toolu_1" {
		t.Errorf("tool_result block: %+v", b)
	}
}

func TestParseResult(t *testing.T) {
	cost := 0.0123
	line := []byte(`{"type":"result","subtype":"success","total_cost_usd":0.0123,"duration_ms":42017,"is_error":false,"num_turns":3}`)
	ev, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "result" || ev.Subtype != "success" {
		t.Errorf("type/subtype: %q/%q", ev.Type, ev.Subtype)
	}
	if ev.TotalCostUSD == nil || *ev.TotalCostUSD != cost {
		t.Errorf("cost: %v", ev.TotalCostUSD)
	}
	if ev.DurationMs != 42017 {
		t.Errorf("duration: %d", ev.DurationMs)
	}
	if ev.NumTurns != 3 {
		t.Errorf("num_turns: %d", ev.NumTurns)
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := ParseEvent([]byte(`not json`)); err == nil {
		t.Errorf("expected error on malformed line")
	}
}

func TestCounter_Accumulates(t *testing.T) {
	var c Counter
	mustObserve := func(s string) {
		ev, err := ParseEvent([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		c.Observe(ev)
	}
	mustObserve(`{"type":"system","subtype":"init","model":"x"}`)
	mustObserve(`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"a"},{"type":"text","text":"hi"},{"type":"tool_use","name":"Read"}]}}`)
	mustObserve(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"},{"type":"tool_use","name":"Edit"}]}}`)
	mustObserve(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x"}]}}`)
	mustObserve(`{"type":"result","total_cost_usd":0.05,"duration_ms":1000,"is_error":false,"num_turns":2}`)

	if c.ToolUseCount != 3 {
		t.Errorf("tool_use_count: got %d, want 3", c.ToolUseCount)
	}
	if c.ThinkingBlocks != 1 {
		t.Errorf("thinking_blocks: got %d, want 1", c.ThinkingBlocks)
	}
	if c.Result == nil || !c.Result.OK || c.Result.DurationMs != 1000 || c.Result.TotalCostUSD == nil || *c.Result.TotalCostUSD != 0.05 {
		t.Errorf("result summary wrong: %+v", c.Result)
	}
}

func TestCounter_ErrorResult(t *testing.T) {
	var c Counter
	ev, _ := ParseEvent([]byte(`{"type":"result","is_error":true,"duration_ms":50}`))
	c.Observe(ev)
	if c.Result == nil || c.Result.OK {
		t.Errorf("expected OK=false, got %+v", c.Result)
	}
}
