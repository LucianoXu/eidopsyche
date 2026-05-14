package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type LifecycleStatusInput struct{}
type LifecycleStatusOutput struct {
	Snapshot any `json:"snapshot" jsonschema:"current lifecycle job snapshot (job_id, running, started_at, ...)"`
}

func (s *Server) eidosLifecycleStatus(ctx context.Context, _ *mcp.CallToolRequest, _ LifecycleStatusInput) (*mcp.CallToolResult, LifecycleStatusOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "lifecycle.status", struct{}{}, &raw); err != nil {
		return nil, LifecycleStatusOutput{}, err
	}
	return nil, LifecycleStatusOutput{Snapshot: raw}, nil
}

func (s *Server) registerLifecycleTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_lifecycle_status",
		Description: "Poll the current lifecycle job snapshot. Container context only — returns CONTEXT_MISMATCH on the host.",
	}, s.eidosLifecycleStatus)
}
