package mcp

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// rawIPC is the slice of *ipc.Client that this package depends on.
// Defined as an interface so unit tests can substitute a fake.
type rawIPC interface {
	Call(method string, params any, result any) (*ipc.Error, error)
	Close() error
}

// Client wraps an IPC client and collapses the daemon's two error
// channels (typed *ipc.Error result vs. transport error) into a single
// `error` formatted as "[CODE] message" — the shape the MCP SDK
// turns into a CallToolResult{IsError:true} for the model.
type Client struct {
	raw rawIPC
}

func newClient(socket string) (*Client, error) {
	c, err := ipc.Dial(socket)
	if err != nil {
		return nil, fmt.Errorf("dial gate socket %s: %w", socket, err)
	}
	return &Client{raw: c}, nil
}

// Call invokes a daemon IPC method. `params` is JSON-marshalled by
// ipc.Client. `result` is populated from the response body when the
// method succeeds. Errors are returned as `[CODE] message` so the
// LLM sees a stable code prefix it can pattern-match on.
func (c *Client) Call(_ context.Context, method string, params any, result any) error {
	// Note: ipc.Client.Call does not yet take ctx. We expose ctx in
	// the signature so future cancellation can plug in without
	// touching every handler. For now ctx is accepted and ignored.
	ipcErr, err := c.raw.Call(method, params, result)
	if err != nil {
		return fmt.Errorf("[IPC_TRANSPORT] %s", err.Error())
	}
	if ipcErr != nil {
		return fmt.Errorf("[%s] %s", ipcErr.Code, ipcErr.Message)
	}
	return nil
}

// Close releases the underlying IPC connection.
func (c *Client) Close() error { return c.raw.Close() }
