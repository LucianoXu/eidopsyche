package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RelayAddInput is the parameter schema for eidos_relay_add.
type RelayAddInput struct {
	URL  string `json:"url" jsonschema:"wss:// URL of the relay"`
	Role string `json:"role,omitempty" jsonschema:"role: home (publish + read) or fallback (read only when home is down)"`
}

// RelayAddOutput is the result schema for eidos_relay_add.
type RelayAddOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosRelayAdd(ctx context.Context, _ *mcp.CallToolRequest, in RelayAddInput) (*mcp.CallToolResult, RelayAddOutput, error) {
	var out RelayAddOutput
	if err := s.client.Call(ctx, "relay.add", in, &out); err != nil {
		return nil, RelayAddOutput{}, err
	}
	return nil, out, nil
}

// RelayRemoveInput is the parameter schema for eidos_relay_remove.
type RelayRemoveInput struct {
	URL string `json:"url" jsonschema:"wss:// URL of the relay to remove"`
}

// RelayRemoveOutput is the result schema for eidos_relay_remove.
type RelayRemoveOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosRelayRemove(ctx context.Context, _ *mcp.CallToolRequest, in RelayRemoveInput) (*mcp.CallToolResult, RelayRemoveOutput, error) {
	var out RelayRemoveOutput
	if err := s.client.Call(ctx, "relay.remove", in, &out); err != nil {
		return nil, RelayRemoveOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) registerRelayTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_relay_add",
		Description: "Add a relay URL with a role (home or fallback). Use eidos_state_get('relays') to see what you currently have.",
	}, s.eidosRelayAdd)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_relay_remove",
		Description: "Remove a relay by URL.",
	}, s.eidosRelayRemove)
}
