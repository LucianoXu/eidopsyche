package forge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// renderOpts controls one render pass.
type renderOpts struct {
	// ShowThinking expands assistant.thinking blocks. Default false:
	// only a one-line "(N lines, --thinking to expand)" placeholder is
	// emitted so the operator can opt in when debugging.
	ShowThinking bool
}

// renderEvent returns the human-readable lines for one event, or nil
// for events that have no visible rendering in this mode (unknown
// subtypes, partial stream_event deltas in v0). The returned slice
// includes any leading blank-line spacers required for visual breathing
// room.
func renderEvent(ev transcript.Event, opts renderOpts) []string {
	switch ev.Type {
	case transcript.TypeSystem:
		return renderSystem(ev)
	case transcript.TypeAssistant:
		return renderAssistant(ev, opts)
	case transcript.TypeUser:
		return renderUserToolResult(ev)
	case transcript.TypeResult:
		return renderResult(ev)
	}
	return nil
}

func renderSystem(ev transcript.Event) []string {
	if ev.Subtype != "init" {
		return nil
	}
	parts := []string{
		fmt.Sprintf("━━━ wake started · model=%s ━━━", strDefault(ev.Model, "?")),
	}
	subline := "    tools: "
	if len(ev.Tools) == 0 {
		subline += "(none)"
	} else {
		subline += strings.Join(ev.Tools, " ")
	}
	if len(ev.MCPServers) > 0 {
		names := make([]string, len(ev.MCPServers))
		for i, m := range ev.MCPServers {
			names[i] = m.Name
		}
		subline += " · MCP: " + strings.Join(names, ",")
	}
	return append(parts, subline)
}

func renderAssistant(ev transcript.Event, opts renderOpts) []string {
	if ev.Message == nil {
		return nil
	}
	var lines []string
	for _, b := range ev.Message.Content {
		switch b.Type {
		case transcript.BlockText:
			lines = append(lines, "")
			lines = append(lines, box("assistant", strings.Split(b.Text, "\n"))...)
		case transcript.BlockThinking:
			lines = append(lines, "")
			if opts.ShowThinking {
				lines = append(lines, box("thinking", strings.Split(b.Thinking, "\n"))...)
			} else {
				n := strings.Count(strings.TrimRight(b.Thinking, "\n"), "\n") + 1
				lines = append(lines, fmt.Sprintf("╭─ thinking ────╮ (%d lines, --thinking to expand)", n))
			}
		case transcript.BlockToolUse:
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("╭─ tool: %s ──╮", b.Name))
			if summary := summariseToolInput(b.Input); summary != "" {
				lines = append(lines, "│ "+summary)
				lines = append(lines, "╰"+strings.Repeat("─", 15)+"╯")
			}
		}
	}
	return lines
}

func renderUserToolResult(ev transcript.Event) []string {
	if ev.Message == nil {
		return nil
	}
	var lines []string
	for _, b := range ev.Message.Content {
		if b.Type != transcript.BlockToolResult {
			continue
		}
		lines = append(lines, "")
		body := summariseToolResult(b.Content)
		bodyLines := strings.Split(body, "\n")
		// Truncate to 30 lines by default — full content is one --raw away.
		const max = 30
		truncated := false
		if len(bodyLines) > max {
			bodyLines = bodyLines[:max]
			truncated = true
		}
		lines = append(lines, box("result", bodyLines)...)
		if truncated {
			lines = append(lines, "    … (truncated; --raw to see full output)")
		}
	}
	return lines
}

func renderResult(ev transcript.Event) []string {
	cost := "-"
	if ev.TotalCostUSD != nil && *ev.TotalCostUSD > 0 {
		cost = fmt.Sprintf("$%.4f", *ev.TotalCostUSD)
	}
	status := "ok"
	if ev.IsError {
		status = "failed"
	}
	durSec := ev.DurationMs / 1000
	return []string{
		"",
		fmt.Sprintf("━━━ done · cost %s · %ds · %d turns · %s ━━━", cost, durSec, ev.NumTurns, status),
	}
}

// box renders a content block with a single-line label header and a
// bordered body. Empty bodyLines yields an empty box (header + footer
// only) — useful for very small results.
func box(label string, bodyLines []string) []string {
	header := fmt.Sprintf("╭─ %s ", label)
	header += strings.Repeat("─", maxIntZero(15-len(header)+1)) + "╮"
	footer := "╰" + strings.Repeat("─", 15) + "╯"
	out := []string{header}
	for _, l := range bodyLines {
		out = append(out, "│ "+l)
	}
	out = append(out, footer)
	return out
}

func maxIntZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func strDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// summariseToolInput renders a tool_use block's input as a single-line
// preview. Strategy: try common single-key inputs (Read{path}, Bash
// {command}, Edit{file_path}); else fall back to the JSON marshal
// truncated to 80 chars.
func summariseToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return truncate(string(raw), 80)
	}
	for _, key := range []string{"path", "file_path", "command", "url", "pattern", "query"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return fmt.Sprintf("%s: %s", key, truncate(s, 80))
			}
		}
	}
	body, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return truncate(string(body), 80)
}

// summariseToolResult flattens a tool_result block's content to a
// readable string. The Claude format allows either a plain string or a
// list of typed sub-blocks; we handle both.
func summariseToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try plain string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Else list of blocks: [{"type":"text","text":"..."}, ...]
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		var parts []string
		for _, b := range arr {
			if t, _ := b["type"].(string); t == "text" {
				if txt, _ := b["text"].(string); txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return truncate(string(raw), 80)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// renderListTableForWatch renders the same table as `transcript-list`
// but filtered/limited at the host so output stays readable when the
// container has hundreds of wakes.
func renderListTableForWatch(idx transcript.Index, limit int) []string {
	wakes := idx.Wakes
	if limit > 0 && len(wakes) > limit {
		wakes = wakes[:limit]
	}
	if len(wakes) == 0 {
		return []string{"no wakes recorded yet"}
	}
	out := []string{"ID         REASON      STARTED              DUR    COST      STATUS"}
	for _, w := range wakes {
		id := w.ID
		if len(id) > 8 {
			id = id[:8]
		}
		started := unixToISOZ(w.StartedAt)
		dur := "-"
		if w.EndedAt > w.StartedAt {
			dur = fmt.Sprintf("%ds", w.EndedAt-w.StartedAt)
		}
		cost := "-"
		if w.CostUSD != nil && *w.CostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", *w.CostUSD)
		}
		status := "ok"
		if !w.OK {
			if w.ExitCode > 0 {
				status = fmt.Sprintf("failed(%d)", w.ExitCode)
			} else {
				status = "crashed"
			}
		}
		out = append(out, fmt.Sprintf("%-10s %-11s %-20s %-6s %-9s %s",
			id, w.Reason, started, dur, cost, status))
	}
	return out
}
