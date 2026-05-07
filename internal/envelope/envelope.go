// Package envelope defines the v1 wire format for NIP-17 inner rumor content
// in MindGate. See docs/superpowers/specs/2026-05-07-envelope-v1-design.md.
package envelope

import (
	"encoding/json"
	"errors"
)

type Type string

const (
	TypeChat    Type = "chat"
	TypeCommand Type = "command"
)

const SchemaVersion = 1

type Envelope struct {
	V       int      `json:"v"`
	Type    Type     `json:"type"`
	Text    string   `json:"text,omitempty"`
	Command *Command `json:"command,omitempty"`
	Client  *Client  `json:"client,omitempty"`
}

type Command struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type Client struct {
	Name string `json:"name"`
	Ver  string `json:"ver"`
}

var (
	ErrNotEnvelope        = errors.New("envelope: not envelope shape")
	ErrUnsupportedVersion = errors.New("envelope: unsupported version")
	ErrSchemaViolation    = errors.New("envelope: schema violation")
)

func Validate(e Envelope) error {
	if e.V == 0 && e.Type == "" {
		return ErrNotEnvelope
	}
	if e.V != SchemaVersion {
		return ErrUnsupportedVersion
	}
	switch e.Type {
	case TypeChat:
		if e.Text == "" {
			return ErrSchemaViolation
		}
		if e.Command != nil {
			return ErrSchemaViolation
		}
	case TypeCommand:
		if e.Command == nil {
			return ErrSchemaViolation
		}
		if e.Command.Name == "" {
			return ErrSchemaViolation
		}
		if e.Command.Args == nil {
			return ErrSchemaViolation
		}
	default:
		return ErrSchemaViolation
	}
	if e.Client != nil {
		if e.Client.Name == "" || e.Client.Ver == "" {
			return ErrSchemaViolation
		}
	}
	return nil
}

// Encode serializes an Envelope into a JSON string suitable for use as the
// .content of a NIP-17 kind:14 rumor. The envelope is validated before
// encoding; an invalid envelope returns an error and produces no output.
func Encode(e Envelope) (string, error) {
	if err := Validate(e); err != nil {
		return "", err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Decode parses a NIP-17 rumor .content string into an Envelope, returning
// one of the sentinel errors on failure. The error vocabulary is:
//
//   - ErrNotEnvelope: not parseable as a JSON object, OR parses but lacks
//     either `v` or `type`. Callers use this to distinguish "random JSON
//     / plain text" from "intentional but broken envelope".
//   - ErrUnsupportedVersion: `v` is present but is not the integer 1.
//   - ErrSchemaViolation: both `v` and `type` are present and `v == 1`,
//     but a field is missing/wrong/forbidden per spec §3.1 (e.g.
//     `type=chat` with empty text, `type=command` without `command`).
func Decode(content string) (Envelope, error) {
	var probe struct {
		V    *int    `json:"v"`
		Type *string `json:"type"`
	}
	if err := json.Unmarshal([]byte(content), &probe); err != nil {
		return Envelope{}, ErrNotEnvelope
	}
	// Per spec §3.1: missing either v or type → ErrNotEnvelope. The shape
	// must look like an envelope (both anchors present) before subsequent
	// field problems are classified as schema violations.
	if probe.V == nil || probe.Type == nil {
		return Envelope{}, ErrNotEnvelope
	}
	if *probe.V != SchemaVersion {
		return Envelope{}, ErrUnsupportedVersion
	}

	var e Envelope
	if err := json.Unmarshal([]byte(content), &e); err != nil {
		return Envelope{}, ErrSchemaViolation
	}
	if err := Validate(e); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
