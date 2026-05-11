package claudeauth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapSetupToken_RoundTrip(t *testing.T) {
	const token = "sk-ant-oat01-XYZ"
	blob, err := WrapSetupToken(token)
	if err != nil {
		t.Fatalf("WrapSetupToken: %v", err)
	}
	if err := Validate(blob); err != nil {
		t.Fatalf("Validate(WrapSetupToken): %v", err)
	}
	var got struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ClaudeAiOauth.AccessToken != token {
		t.Errorf("accessToken = %q, want %q", got.ClaudeAiOauth.AccessToken, token)
	}
	if got.ClaudeAiOauth.RefreshToken == "" {
		t.Error("refreshToken empty; Validate would reject")
	}
	if got.ClaudeAiOauth.ExpiresAt <= 0 {
		t.Error("expiresAt non-positive; Validate would reject")
	}
}

func TestWrapSetupToken_TrimsWhitespace(t *testing.T) {
	blob, err := WrapSetupToken("  sk-ant-oat01-XYZ\n")
	if err != nil {
		t.Fatalf("WrapSetupToken: %v", err)
	}
	if !strings.Contains(string(blob), `"accessToken": "sk-ant-oat01-XYZ"`) {
		t.Errorf("token not trimmed; got: %s", blob)
	}
}

func TestWrapSetupToken_Empty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		if _, err := WrapSetupToken(in); err == nil {
			t.Errorf("WrapSetupToken(%q) = nil err, want error", in)
		}
	}
}
