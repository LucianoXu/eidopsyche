package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CardParseInput struct {
	URI string `json:"uri" jsonschema:"a mindgate:// URI to decode"`
}
type CardParseOutput struct {
	Card any `json:"card" jsonschema:"decoded fields: npub, pubkey, relays, label"`
}

func (s *Server) eidosCardParse(ctx context.Context, _ *mcp.CallToolRequest, in CardParseInput) (*mcp.CallToolResult, CardParseOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "card.parse", in, &raw); err != nil {
		return nil, CardParseOutput{}, err
	}
	return nil, CardParseOutput{Card: raw}, nil
}

type CardScanInput struct {
	URI string `json:"uri" jsonschema:"a mindgate:// URI to decode and cross-check against contacts"`
}
type CardScanOutput struct {
	Result any `json:"result" jsonschema:"decoded card + 'already_contact' flag indicating whether this npub is already a contact"`
}

func (s *Server) eidosCardScan(ctx context.Context, _ *mcp.CallToolRequest, in CardScanInput) (*mcp.CallToolResult, CardScanOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "card.scan", in, &raw); err != nil {
		return nil, CardScanOutput{}, err
	}
	return nil, CardScanOutput{Result: raw}, nil
}

func (s *Server) registerCardTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_card_parse",
		Description: "Decode a mindgate:// URI into its parts (npub, pubkey, relays, label). Pure parse — does not touch contacts.",
	}, s.eidosCardParse)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_card_scan",
		Description: "Decode a mindgate:// URI AND check whether the npub is already in your contacts. Use this when deciding whether to call eidos_contact_add_from_card.",
	}, s.eidosCardScan)
}
