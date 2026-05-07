package envelope

import (
	"errors"
	"testing"
)

func TestValidate_ChatOK(t *testing.T) {
	e := Envelope{V: 1, Type: TypeChat, Text: "hello"}
	if err := Validate(e); err != nil {
		t.Fatalf("Validate: want nil, got %v", err)
	}
}

func TestValidate_Cases(t *testing.T) {
	cmd := func(name string, args map[string]any) *Command { return &Command{Name: name, Args: args} }
	cli := &Client{Name: "eidos", Ver: "0.3.0"}
	cases := []struct {
		name string
		env  Envelope
		want error
	}{
		{"chat ok no client", Envelope{V: 1, Type: TypeChat, Text: "hi"}, nil},
		{"chat ok with client", Envelope{V: 1, Type: TypeChat, Text: "hi", Client: cli}, nil},
		{"chat empty text", Envelope{V: 1, Type: TypeChat, Text: ""}, ErrSchemaViolation},
		{"chat with command field", Envelope{V: 1, Type: TypeChat, Text: "hi", Command: cmd("x", map[string]any{})}, ErrSchemaViolation},
		{"command ok empty args", Envelope{V: 1, Type: TypeCommand, Command: cmd("status", map[string]any{})}, nil},
		{"command ok with text", Envelope{V: 1, Type: TypeCommand, Text: "/status", Command: cmd("status", map[string]any{})}, nil},
		{"command nil command", Envelope{V: 1, Type: TypeCommand}, ErrSchemaViolation},
		{"command empty name", Envelope{V: 1, Type: TypeCommand, Command: cmd("", map[string]any{})}, ErrSchemaViolation},
		{"command nil args", Envelope{V: 1, Type: TypeCommand, Command: &Command{Name: "x"}}, ErrSchemaViolation},
		{"unknown type", Envelope{V: 1, Type: Type("frobnicate"), Text: "hi"}, ErrSchemaViolation},
		{"empty zero value", Envelope{}, ErrNotEnvelope},
		{"v=2", Envelope{V: 2, Type: TypeChat, Text: "hi"}, ErrUnsupportedVersion},
		{"client missing ver", Envelope{V: 1, Type: TypeChat, Text: "hi", Client: &Client{Name: "x"}}, ErrSchemaViolation},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := Validate(tc.env)
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
