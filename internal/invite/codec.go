package invite

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Encode produces "mindgate-invite://<base64url-no-pad of JSON-with-sig>".
func (p Payload) Encode() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	return TokenScheme + encoded, nil
}

// Decode parses a token URI back into a Payload (does NOT verify the signature).
// Strips "mindgate-invite://" prefix if present; also accepts the bare
// base64url body for ergonomics.
func Decode(s string) (*Payload, error) {
	body := strings.TrimPrefix(s, TokenScheme)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("base64url decode: %w", err)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &p, nil
}
