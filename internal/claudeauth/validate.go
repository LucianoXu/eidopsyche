// internal/claudeauth/validate.go
//
// Package claudeauth orchestrates the per-mindform setup-token flow:
//   - Validate: parse the credentials blob and confirm the required
//     OAuth fields are present and well-typed.
//   - Generate: drive `claude setup-token` against an isolated HOME so
//     the resulting token never lands in the operator's host claude.
//   - WriteToVolume: install the validated blob into the mindform's
//     docker volume at /eidos/claude/.claude/.credentials.json and
//     clear the auth_required marker.
//
// The package replaces the previous installFromHost path, which copied
// the operator's ~/.claude.json and ~/.claude/.credentials.json into
// the volume — a configuration that shared OAuth tokens between host
// and container and failed every time the host's claude rotated its
// access token server-side.
package claudeauth

import (
	"encoding/json"
	"errors"
	"fmt"
)

// claudeCredentials mirrors the on-disk shape of ~/.claude/.credentials.json
// for the fields Validate cares about. Extra fields are tolerated.
type claudeCredentials struct {
	ClaudeAiOauth *struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

// Validate parses the credentials blob and confirms it carries the
// minimum OAuth fields claude needs at runtime. Returns a descriptive
// error otherwise — callers surface it to the operator so they know
// which field of which file is wrong.
func Validate(blob []byte) error {
	if len(blob) == 0 {
		return errors.New("empty credentials blob")
	}
	var c claudeCredentials
	if err := json.Unmarshal(blob, &c); err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}
	if c.ClaudeAiOauth == nil {
		return errors.New("credentials missing claudeAiOauth section")
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return errors.New("credentials missing claudeAiOauth.accessToken")
	}
	if c.ClaudeAiOauth.RefreshToken == "" {
		return errors.New("credentials missing claudeAiOauth.refreshToken")
	}
	if c.ClaudeAiOauth.ExpiresAt == 0 {
		return errors.New("credentials missing claudeAiOauth.expiresAt")
	}
	return nil
}
