package mcp

import (
	"context"
	"testing"
)

func TestEidosInvite_Routing(t *testing.T) {
	tests := []struct {
		name   string
		method string
		call   func(s *Server) error
	}{
		{"create", "invite.create", func(s *Server) error {
			_, _, err := s.eidosInviteCreate(context.Background(), nil, InviteCreateInput{ExpiresSeconds: 3600, MaxUses: 1, IssuerLabel: "for-bob"})
			return err
		}},
		{"list", "invite.list", func(s *Server) error {
			_, _, err := s.eidosInviteList(context.Background(), nil, InviteListInput{Status: "active"})
			return err
		}},
		{"revoke", "invite.revoke", func(s *Server) error {
			_, _, err := s.eidosInviteRevoke(context.Background(), nil, InviteRevokeInput{IDPrefix: "abc12"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeIPC{result: map[string]any{}}
			s := &Server{client: &Client{raw: fake}}
			if err := tc.call(s); err != nil {
				t.Fatalf("call: %v", err)
			}
			if fake.lastMethod != tc.method {
				t.Fatalf("method = %q, want %q", fake.lastMethod, tc.method)
			}
		})
	}
}
