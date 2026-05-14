package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ContactAddInput is the input to eidos_contact_add.
type ContactAddInput struct {
	Npub   string   `json:"npub" jsonschema:"npub (bech32 pubkey) of the contact to add"`
	Label  string   `json:"label,omitempty" jsonschema:"optional display label"`
	Tier   string   `json:"tier,omitempty" jsonschema:"optional trust tier: friend, contact, block"`
	Relays []string `json:"relays,omitempty" jsonschema:"optional override of relays for this contact"`
}

// ContactAddOutput is the output of eidos_contact_add.
type ContactAddOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosContactAdd(ctx context.Context, _ *mcp.CallToolRequest, in ContactAddInput) (*mcp.CallToolResult, ContactAddOutput, error) {
	var out ContactAddOutput
	if err := s.client.Call(ctx, "contact.add", in, &out); err != nil {
		return nil, ContactAddOutput{}, err
	}
	return nil, out, nil
}

// ContactAddFromCardInput matches the daemon's ContactAddFromCardParams
// shape: {uri, label_override}. The daemon admits the npub from the
// decoded card; trust tier is NOT settable here (use contact_set_tier
// after to adjust). See internal/daemon/methods_contact.go:20.
type ContactAddFromCardInput struct {
	URI           string `json:"uri" jsonschema:"a mindgate:// invite/card URI"`
	LabelOverride string `json:"label_override,omitempty" jsonschema:"optional display label (overrides any label embedded in the card)"`
}

// ContactAddFromCardOutput is the output of eidos_contact_add_from_card.
type ContactAddFromCardOutput struct {
	OK   bool   `json:"ok"`
	Npub string `json:"npub,omitempty" jsonschema:"npub of the added contact (echoed for confirmation)"`
}

func (s *Server) eidosContactAddFromCard(ctx context.Context, _ *mcp.CallToolRequest, in ContactAddFromCardInput) (*mcp.CallToolResult, ContactAddFromCardOutput, error) {
	var out ContactAddFromCardOutput
	if err := s.client.Call(ctx, "contact.add-from-card", in, &out); err != nil {
		return nil, ContactAddFromCardOutput{}, err
	}
	return nil, out, nil
}

// ContactRemoveInput matches the daemon's contact.remove shape:
// {npub} where the value may be npub / hex / label (resolveTarget).
// See internal/daemon/methods_contact.go:176-184.
type ContactRemoveInput struct {
	Npub string `json:"npub" jsonschema:"npub, hex pubkey, or display label of the contact to remove"`
}

// ContactRemoveOutput is the output of eidos_contact_remove.
type ContactRemoveOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosContactRemove(ctx context.Context, _ *mcp.CallToolRequest, in ContactRemoveInput) (*mcp.CallToolResult, ContactRemoveOutput, error) {
	var out ContactRemoveOutput
	if err := s.client.Call(ctx, "contact.remove", in, &out); err != nil {
		return nil, ContactRemoveOutput{}, err
	}
	return nil, out, nil
}

// ContactSetLabelInput is the input to eidos_contact_set_label.
type ContactSetLabelInput struct {
	Target string `json:"target" jsonschema:"npub, hex pubkey, or current label of the contact"`
	Label  string `json:"label" jsonschema:"new display label"`
}

// ContactSetLabelOutput is the output of eidos_contact_set_label.
type ContactSetLabelOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosContactSetLabel(ctx context.Context, _ *mcp.CallToolRequest, in ContactSetLabelInput) (*mcp.CallToolResult, ContactSetLabelOutput, error) {
	var out ContactSetLabelOutput
	if err := s.client.Call(ctx, "contact.set-label", in, &out); err != nil {
		return nil, ContactSetLabelOutput{}, err
	}
	return nil, out, nil
}

// ContactSetTierInput is the input to eidos_contact_set_tier.
type ContactSetTierInput struct {
	Target string `json:"target" jsonschema:"npub, hex pubkey, or label of the contact"`
	Tier   string `json:"tier" jsonschema:"trust tier: friend, contact, block"`
}

// ContactSetTierOutput is the output of eidos_contact_set_tier.
type ContactSetTierOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosContactSetTier(ctx context.Context, _ *mcp.CallToolRequest, in ContactSetTierInput) (*mcp.CallToolResult, ContactSetTierOutput, error) {
	var out ContactSetTierOutput
	if err := s.client.Call(ctx, "contact.set-tier", in, &out); err != nil {
		return nil, ContactSetTierOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) registerContactTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_contact_add",
		Description: "Add a contact by npub. Optionally set label, tier (friend/contact/block), and override relays.",
	}, s.eidosContactAdd)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_contact_add_from_card",
		Description: "Add a contact from a mindgate:// URI (invite or card). Easier than handling the raw npub when the operator pasted a URI. Note: tier defaults to the daemon's default — use eidos_contact_set_tier after if you need to change it.",
	}, s.eidosContactAddFromCard)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_contact_remove",
		Description: "Remove a contact by npub, hex pubkey, or label. Errors with CONTACT_NOT_FOUND if no match; LABEL_AMBIGUOUS if more than one match by label.",
	}, s.eidosContactRemove)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_contact_set_label",
		Description: "Rename a contact. Same resolution rules as contact_remove.",
	}, s.eidosContactSetLabel)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_contact_set_tier",
		Description: "Change a contact's trust tier. Allowed tiers: friend, contact, block.",
	}, s.eidosContactSetTier)
}
