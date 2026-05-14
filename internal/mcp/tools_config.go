package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConfigSetInput matches the daemon's ConfigSetParams shape (see
// internal/daemon/methods_config.go:17). Path is the dotted config
// key (e.g. "log_level", "heartbeat.interval", "mindform.model").
type ConfigSetInput struct {
	Path  string `json:"path" jsonschema:"registered config key path (e.g. log_level, heartbeat.interval, mindform.model)"`
	Value string `json:"value" jsonschema:"new value (string form; daemon validates against the key's type/registry)"`
}

// ConfigSetOutput is the result schema for eidos_config_set.
type ConfigSetOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosConfigSet(ctx context.Context, _ *mcp.CallToolRequest, in ConfigSetInput) (*mcp.CallToolResult, ConfigSetOutput, error) {
	var out ConfigSetOutput
	if err := s.client.Call(ctx, "config.set", in, &out); err != nil {
		return nil, ConfigSetOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) registerConfigTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_config_set",
		Description: "Set a registered config key. The daemon validates the key, checks its Context (host vs container), and applies any registered hooks. CONTEXT_MISMATCH means the key only makes sense in the other context (e.g. mindform.model is container-only).",
	}, s.eidosConfigSet)
}
