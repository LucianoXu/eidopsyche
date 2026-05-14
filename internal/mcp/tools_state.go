package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StateGetInput selects a subtree of the daemon's unified state by
// dotted path. Examples:
//   - "" or omitted → full state root
//   - "identity"
//   - "contacts.<hex-pubkey>"
//   - "config.heartbeat.interval"
//   - "inbox.recent"
//
// See internal/daemon/state_contributors.go for the contributor list.
type StateGetInput struct {
	Path string `json:"path,omitempty" jsonschema:"dotted path into the state tree (e.g. 'contacts', 'config.log_level'); empty/omitted returns the full snapshot"`
}

// StateGetOutput holds the value at Path. Shape is method-dependent
// (map/slice/scalar) so we use `any`.
type StateGetOutput struct {
	Value any `json:"value"`
}

func (s *Server) eidosStateGet(ctx context.Context, _ *mcp.CallToolRequest, in StateGetInput) (*mcp.CallToolResult, StateGetOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "state.get", in, &raw); err != nil {
		return nil, StateGetOutput{}, err
	}
	return nil, StateGetOutput{Value: raw}, nil
}

func (s *Server) registerStateTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_state_get",
		Description: "Read a subtree of your unified state by dotted path (identity, contacts, config, relays, inbox.recent, lifecycle, ...). The single read tool — prefer this over reading files for state the daemon owns.",
	}, s.eidosStateGet)
}
