package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// InviteCreateInput matches the daemon's invite.create shape (see
// internal/daemon/methods_invite.go:26-33). All fields are optional;
// omitting all of them yields a daemon-default invite.
type InviteCreateInput struct {
	SingleUse      bool   `json:"single_use,omitempty" jsonschema:"if true, the invite can be redeemed at most once"`
	MaxUses        int    `json:"max_uses,omitempty" jsonschema:"max redemptions; overrides single_use when set; 0 = use daemon default"`
	ExpiresSeconds int64  `json:"expires_seconds,omitempty" jsonschema:"seconds until expiry; 0 = use daemon default"`
	IssuerLabel    string `json:"issuer_label,omitempty" jsonschema:"optional label for the issuer (you) in the printed invite"`
	RedeemerLabel  string `json:"redeemer_label,omitempty" jsonschema:"optional label suggesting how the redeemer should identify themselves"`
	Unlimited      bool   `json:"unlimited,omitempty" jsonschema:"if true, the invite has no max_uses cap"`
}

type InviteCreateOutput struct {
	Invite any `json:"invite" jsonschema:"created invite row including the mindgate:// URI"`
}

func (s *Server) eidosInviteCreate(ctx context.Context, _ *mcp.CallToolRequest, in InviteCreateInput) (*mcp.CallToolResult, InviteCreateOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "invite.create", in, &raw); err != nil {
		return nil, InviteCreateOutput{}, err
	}
	return nil, InviteCreateOutput{Invite: raw}, nil
}

type InviteListInput struct {
	Status string `json:"status,omitempty" jsonschema:"filter: active | expired | redeemed | revoked | all (default all)"`
}

type InviteListOutput struct {
	Invites any `json:"invites"`
}

func (s *Server) eidosInviteList(ctx context.Context, _ *mcp.CallToolRequest, in InviteListInput) (*mcp.CallToolResult, InviteListOutput, error) {
	var raw any
	if err := s.client.Call(ctx, "invite.list", in, &raw); err != nil {
		return nil, InviteListOutput{}, err
	}
	return nil, InviteListOutput{Invites: raw}, nil
}

// InviteRevokeInput matches the daemon's invite.revoke shape (see
// internal/daemon/methods_invite.go:88-90). IDPrefix may be a full
// invite ID or an unambiguous prefix.
type InviteRevokeInput struct {
	IDPrefix string `json:"id_prefix" jsonschema:"invite ID or unambiguous prefix"`
}

type InviteRevokeOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) eidosInviteRevoke(ctx context.Context, _ *mcp.CallToolRequest, in InviteRevokeInput) (*mcp.CallToolResult, InviteRevokeOutput, error) {
	var out InviteRevokeOutput
	if err := s.client.Call(ctx, "invite.revoke", in, &out); err != nil {
		return nil, InviteRevokeOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) registerInviteTools() {
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_invite_create",
		Description: "Create a new MindGate invite. Returns a row with the mindgate:// URI you can hand out. All fields are optional; omit them all to get a daemon-default invite.",
	}, s.eidosInviteCreate)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_invite_list",
		Description: "List invites filtered by status (active/expired/redeemed/revoked/all).",
	}, s.eidosInviteList)
	mcp.AddTool(s.sdk, &mcp.Tool{
		Name:        "eidos_invite_revoke",
		Description: "Revoke an invite by ID or unambiguous prefix. INVITE_PREFIX_AMBIGUOUS if the prefix matches more than one.",
	}, s.eidosInviteRevoke)
}
