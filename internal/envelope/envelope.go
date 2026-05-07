// Package envelope defines the v1 wire format for NIP-17 inner rumor content
// in MindGate. See docs/superpowers/specs/2026-05-07-envelope-v1-design.md.
package envelope

import "errors"

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
