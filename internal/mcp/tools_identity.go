package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SetLabelInput is the input to eidos_set_label.
type SetLabelInput struct {
	Label string `json:"label" jsonschema:"new advertised display label for this identity"`
}

// SetLabelOutput is the output of eidos_set_label.
type SetLabelOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosSetLabel(ctx context.Context, _ *mcp.CallToolRequest, in SetLabelInput) (*mcp.CallToolResult, SetLabelOutput, error) {
	var out SetLabelOutput
	if err := s.client.Call(ctx, "set-label", in, &out); err != nil {
		return nil, SetLabelOutput{}, err
	}
	return nil, out, nil
}

// ServiceStatusInput has no fields — service.status takes no params.
type ServiceStatusInput struct{}

// ServiceStatusOutput holds the raw daemon status snapshot.
type ServiceStatusOutput struct {
	Snapshot any `json:"snapshot" jsonschema:"daemon meta: socket path, started_at, version, dashboard URL"`
}

func (s *Server) eidosServiceStatus(ctx context.Context, _ *mcp.CallToolRequest, _ ServiceStatusInput) (*mcp.CallToolResult, ServiceStatusOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "service.status", struct{}{}, &raw); err != nil {
		return nil, ServiceStatusOutput{}, err
	}
	return nil, ServiceStatusOutput{Snapshot: raw}, nil
}

// AgentStateInput is empty — agent.state takes no params.
type AgentStateInput struct{}

// AgentStateOutput holds the raw agent-state snapshot.
type AgentStateOutput struct {
	Snapshot any `json:"snapshot" jsonschema:"agent-state.json contents (claude_busy etc.); container-context only"`
}

func (s *Server) eidosAgentState(ctx context.Context, _ *mcp.CallToolRequest, _ AgentStateInput) (*mcp.CallToolResult, AgentStateOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "agent.state", struct{}{}, &raw); err != nil {
		return nil, AgentStateOutput{}, err
	}
	return nil, AgentStateOutput{Snapshot: raw}, nil
}

func (s *Server) registerIdentityTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_set_label",
		Description: "Change your own advertised display label. The new label is signed and announced to your relays.",
	}, s.eidosSetLabel)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_service_status",
		Description: "Daemon-level metadata: socket path, started_at, version, dashboard URL. Useful for debugging.",
	}, s.eidosServiceStatus)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_agent_state",
		Description: "Your own agent-state.json (claude_busy flag, etc.). Container context only — returns CONTEXT_MISMATCH on the host.",
	}, s.eidosAgentState)
}
