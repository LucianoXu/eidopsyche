// internal/claudeauth/validate_test.go
package claudeauth

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // empty = expect nil
	}{
		{
			name: "full-creds-ok",
			body: `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-AA","refreshToken":"sk-ant-ort01-BB","expiresAt":1778549450074,"scopes":["user:inference"],"subscriptionType":"max"}}`,
		},
		{
			name:    "empty",
			body:    ``,
			wantErr: "empty",
		},
		{
			name:    "not-json",
			body:    `nope`,
			wantErr: "parse",
		},
		{
			name:    "missing-oauth",
			body:    `{}`,
			wantErr: "claudeAiOauth",
		},
		{
			name:    "missing-access-token",
			body:    `{"claudeAiOauth":{"refreshToken":"r","expiresAt":1}}`,
			wantErr: "accessToken",
		},
		{
			name:    "missing-refresh-token",
			body:    `{"claudeAiOauth":{"accessToken":"a","expiresAt":1}}`,
			wantErr: "refreshToken",
		},
		{
			name:    "missing-expires-at",
			body:    `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`,
			wantErr: "expiresAt",
		},
		{
			name:    "wrong-type-access-token",
			body:    `{"claudeAiOauth":{"accessToken":123,"refreshToken":"r","expiresAt":1}}`,
			wantErr: "parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
