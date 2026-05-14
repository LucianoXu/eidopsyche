package mcp

import (
	"context"
	"testing"
)

func TestEidosContact_Routing(t *testing.T) {
	tests := []struct {
		name   string
		method string
		call   func(s *Server) error
		verify func(t *testing.T, params any)
	}{
		{
			name:   "add",
			method: "contact.add",
			call: func(s *Server) error {
				_, _, err := s.eidosContactAdd(context.Background(), nil, ContactAddInput{Npub: "npub1abc", Label: "alice", Tier: "contact"})
				return err
			},
			verify: func(t *testing.T, params any) {
				p := params.(ContactAddInput)
				if p.Npub != "npub1abc" || p.Label != "alice" || p.Tier != "contact" {
					t.Fatalf("params = %+v", p)
				}
			},
		},
		{
			name:   "add_from_card",
			method: "contact.add-from-card",
			call: func(s *Server) error {
				_, _, err := s.eidosContactAddFromCard(context.Background(), nil, ContactAddFromCardInput{URI: "mindgate://abc"})
				return err
			},
			verify: func(t *testing.T, params any) {
				if params.(ContactAddFromCardInput).URI != "mindgate://abc" {
					t.Fatalf("uri mismatch: %+v", params)
				}
			},
		},
		{
			name:   "remove",
			method: "contact.remove",
			call: func(s *Server) error {
				_, _, err := s.eidosContactRemove(context.Background(), nil, ContactRemoveInput{Npub: "alice"})
				return err
			},
			verify: func(t *testing.T, params any) {
				if params.(ContactRemoveInput).Npub != "alice" {
					t.Fatalf("npub mismatch: %+v", params)
				}
			},
		},
		{
			name:   "set_label",
			method: "contact.set-label",
			call: func(s *Server) error {
				_, _, err := s.eidosContactSetLabel(context.Background(), nil, ContactSetLabelInput{Target: "npub1abc", Label: "alicia"})
				return err
			},
			verify: func(t *testing.T, params any) {
				p := params.(ContactSetLabelInput)
				if p.Target != "npub1abc" || p.Label != "alicia" {
					t.Fatalf("params = %+v", p)
				}
			},
		},
		{
			name:   "set_tier",
			method: "contact.set-tier",
			call: func(s *Server) error {
				_, _, err := s.eidosContactSetTier(context.Background(), nil, ContactSetTierInput{Target: "alice", Tier: "friend"})
				return err
			},
			verify: func(t *testing.T, params any) {
				p := params.(ContactSetTierInput)
				if p.Target != "alice" || p.Tier != "friend" {
					t.Fatalf("params = %+v", p)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeIPC{}
			s := &Server{client: &Client{raw: fake}}
			if err := tc.call(s); err != nil {
				t.Fatalf("call: %v", err)
			}
			if fake.lastMethod != tc.method {
				t.Fatalf("method = %q, want %q", fake.lastMethod, tc.method)
			}
			tc.verify(t, fake.lastParams)
		})
	}
}
