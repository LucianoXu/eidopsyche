package transcript

import (
	"encoding/json"
)

// Top-level event types emitted by `claude --output-format stream-json`.
// See https://code.claude.com/docs/en/headless and the Agent SDK docs.
const (
	TypeSystem      = "system"
	TypeAssistant   = "assistant"
	TypeUser        = "user"
	TypeResult      = "result"
	TypeStreamEvent = "stream_event" // partial events when --include-partial-messages is set
)

// Content block types nested inside assistant.message.content[] / user.message.content[].
const (
	BlockText       = "text"
	BlockThinking   = "thinking"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
)

// Event is the minimal envelope the transcript layer needs. Fields not
// listed here are preserved as raw bytes by the store (which writes the
// claude stdout verbatim) but ignored at parse time. The renderer in
// PR-B2 will introduce richer types as needed.
type Event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// system-event init fields
	Model      string      `json:"model,omitempty"`
	Tools      []string    `json:"tools,omitempty"`
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`

	// assistant-event / user-event envelope
	Message *Message `json:"message,omitempty"`

	// result-event fields
	TotalCostUSD *float64 `json:"total_cost_usd,omitempty"`
	DurationMs   int64    `json:"duration_ms,omitempty"`
	IsError      bool     `json:"is_error,omitempty"`
	NumTurns     int      `json:"num_turns,omitempty"`
}

// MCPServer is the per-server entry in a system-init event's mcp_servers.
type MCPServer struct {
	Name string `json:"name"`
}

// Message is the loosely-structured assistant/user message body.
type Message struct {
	Role    string         `json:"role,omitempty"`
	Content []ContentBlock `json:"content,omitempty"`
}

// ContentBlock is a single block inside Message.Content. The exact set
// of populated fields depends on Type. Unknown block types parse without
// error and surface as Type="..." with no recognised payload.
type ContentBlock struct {
	Type string `json:"type"`

	// text / thinking
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ParseEvent unmarshals one NDJSON line into an Event. Returns the
// zero value and a non-nil error on malformed input.
func ParseEvent(line []byte) (Event, error) {
	var ev Event
	err := json.Unmarshal(line, &ev)
	return ev, err
}

// Counter accumulates per-wake metrics from a stream of events. Used by
// agent-runner to build the index entry without a second-pass file read.
type Counter struct {
	ToolUseCount   int
	ThinkingBlocks int
	Result         *ResultSummary
}

// ResultSummary captures the headline fields from the final result event.
type ResultSummary struct {
	OK           bool
	DurationMs   int64
	TotalCostUSD *float64
	NumTurns     int
}

// Observe folds one event into the counter. Idempotent on repeated calls
// only when called on distinct events.
func (c *Counter) Observe(ev Event) {
	switch ev.Type {
	case TypeAssistant:
		if ev.Message != nil {
			for _, b := range ev.Message.Content {
				switch b.Type {
				case BlockToolUse:
					c.ToolUseCount++
				case BlockThinking:
					c.ThinkingBlocks++
				}
			}
		}
	case TypeResult:
		c.Result = &ResultSummary{
			OK:           !ev.IsError,
			DurationMs:   ev.DurationMs,
			TotalCostUSD: ev.TotalCostUSD,
			NumTurns:     ev.NumTurns,
		}
	}
}
