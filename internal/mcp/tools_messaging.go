package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

type SendInput struct {
	To   string `json:"to" jsonschema:"npub, hex pubkey, or contact label of the recipient"`
	Body string `json:"body" jsonschema:"message text (non-empty)"`
}
type SendOutput struct {
	EventID    string   `json:"event_id"`
	AcceptedBy []string `json:"accepted_by"`
}

func (s *Server) eidosSend(ctx context.Context, _ *mcp.CallToolRequest, in SendInput) (*mcp.CallToolResult, SendOutput, error) {
	if in.Body == "" {
		return nil, SendOutput{}, fmt.Errorf("[INVALID_PARAMS] body must be non-empty")
	}
	env := envelope.Envelope{
		V:      envelope.SchemaVersion,
		Type:   envelope.TypeChat,
		Text:   in.Body,
		Client: &envelope.Client{Name: "eidos-mcp", Ver: version.Version},
	}
	var out SendOutput
	if err := s.client.Call(ctx, "send", daemon.SendParams{To: in.To, Envelope: &env}, &out); err != nil {
		return nil, SendOutput{}, err
	}
	return nil, out, nil
}

type InboxListInput struct {
	Since  int64  `json:"since,omitempty" jsonschema:"unix-second cutoff; omit for 'no lower bound'"`
	From   string `json:"from,omitempty" jsonschema:"filter to a single sender: npub, hex, or contact label"`
	Sender string `json:"sender,omitempty" jsonschema:"'known' (default), 'unknown', or 'all'"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows to return"`
}
type InboxListOutput struct {
	Messages any `json:"messages"`
}

func (s *Server) eidosInboxList(ctx context.Context, _ *mcp.CallToolRequest, in InboxListInput) (*mcp.CallToolResult, InboxListOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "inbox.list", in, &raw); err != nil {
		return nil, InboxListOutput{}, err
	}
	return nil, InboxListOutput{Messages: raw}, nil
}

type OutboxListInput struct {
	To    string `json:"to,omitempty" jsonschema:"filter to a single recipient: npub, hex, or contact label"`
	Since int64  `json:"since,omitempty" jsonschema:"unix-second cutoff"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows to return"`
}
type OutboxListOutput struct {
	Messages any `json:"messages"`
}

func (s *Server) eidosOutboxList(ctx context.Context, _ *mcp.CallToolRequest, in OutboxListInput) (*mcp.CallToolResult, OutboxListOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "outbox.list", in, &raw); err != nil {
		return nil, OutboxListOutput{}, err
	}
	return nil, OutboxListOutput{Messages: raw}, nil
}

func (s *Server) registerMessagingTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_send",
		Description: "Send a NIP-17 encrypted message via MindGate. Recipient may be an npub, hex pubkey, or contact label. The envelope (chat-type, v1) is constructed for you.",
	}, s.eidosSend)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_inbox_list",
		Description: "List inbox messages with optional since/from/sender/limit filters. Default sender='known' excludes contacts you have blocked or never added.",
	}, s.eidosInboxList)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_outbox_list",
		Description: "List messages you have sent, with optional to/since/limit filters.",
	}, s.eidosOutboxList)
}
