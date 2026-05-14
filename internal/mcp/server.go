// Package mcp implements the eidos-mcp MCP server: a thin adapter that
// exposes a curated subset of the gate daemon's IPC method table as
// typed MCP tools over stdio. The full design lives at
// docs/superpowers/specs/2026-05-14-eidos-mcp-design.md.
package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is the eidos-mcp server. It owns the SDK server instance and
// an IPC client used by all tool handlers to talk to the gate daemon.
type Server struct {
	sdk    *mcp.Server
	client *Client
}

// New returns an unstarted Server bound to the given gate socket path.
// All tools are registered on the SDK server before this returns.
func New(socket, version string) (*Server, error) {
	client, err := newClient(socket)
	if err != nil {
		return nil, err
	}
	sdk := mcp.NewServer(&mcp.Implementation{
		Name:    "eidos",
		Version: version,
	}, nil)
	s := &Server{sdk: sdk, client: client}
	s.registerTools()
	return s, nil
}

// Run blocks until the stdio transport closes (claude exits or ctx
// cancels). It is the main loop for the `eidos mcp` subcommand.
func (s *Server) Run(ctx context.Context) error {
	return s.sdk.Run(ctx, &mcp.StdioTransport{})
}

// registerTools is filled in by the per-domain tools_*.go files via
// their init-time hooks. Kept as a single method so the per-domain
// files can add registrations without each touching server.go.
func (s *Server) registerTools() {
	s.registerStateTools()
	s.registerIdentityTools()
	s.registerContactTools()
	s.registerCardTools()
	s.registerMessagingTools()
	s.registerInviteTools()
	s.registerRelayTools()
}

// Close releases the IPC connection. Called by the cobra cmd on exit.
func (s *Server) Close() error { return s.client.Close() }
