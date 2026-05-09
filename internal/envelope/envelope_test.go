package envelope

import (
	"encoding/json"
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

func TestEncodeDecode_RoundTrip(t *testing.T) {
	cases := []Envelope{
		{V: 1, Type: TypeChat, Text: "hello world"},
		{V: 1, Type: TypeChat, Text: "hi", Client: &Client{Name: "eidos", Ver: "0.3.0"}},
		{V: 1, Type: TypeCommand, Text: "/status", Command: &Command{Name: "status", Args: map[string]any{}}},
	}
	for i, in := range cases {
		s, err := Encode(in)
		if err != nil {
			t.Fatalf("case %d Encode: %v", i, err)
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			t.Fatalf("case %d not JSON: %v", i, err)
		}
		out, err := Decode(s)
		if err != nil {
			t.Fatalf("case %d Decode: %v", i, err)
		}
		if out.V != in.V || out.Type != in.Type || out.Text != in.Text {
			t.Fatalf("case %d round-trip mismatch: in=%+v out=%+v", i, in, out)
		}
	}
}

func TestDecode_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    error
	}{
		{"plain text", "hello", ErrNotEnvelope},
		{"json without v or type", `{"foo":"bar"}`, ErrNotEnvelope},
		{"json with v only", `{"v":1,"text":"hi"}`, ErrNotEnvelope},
		{"json with type only", `{"type":"chat","text":"hi"}`, ErrNotEnvelope},
		{"v=2", `{"v":2,"type":"chat","text":"hi"}`, ErrUnsupportedVersion},
		{"unknown type", `{"v":1,"type":"frob","text":"hi"}`, ErrSchemaViolation},
		{"chat missing text", `{"v":1,"type":"chat"}`, ErrSchemaViolation},
		{"chat empty text", `{"v":1,"type":"chat","text":""}`, ErrSchemaViolation},
		{"chat with command", `{"v":1,"type":"chat","text":"hi","command":{"name":"x","args":{}}}`, ErrSchemaViolation},
		{"command missing command", `{"v":1,"type":"command"}`, ErrSchemaViolation},
		{"command missing args", `{"v":1,"type":"command","command":{"name":"x"}}`, ErrSchemaViolation},
		{"command empty name", `{"v":1,"type":"command","command":{"name":"","args":{}}}`, ErrSchemaViolation},
		{"json array", `[1,2,3]`, ErrNotEnvelope},
		{"empty string", "", ErrNotEnvelope},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.content)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRoundTripAck(t *testing.T) {
	const ref = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []Envelope{
		{V: 1, Type: TypeAck, Ref: ref},
		{V: 1, Type: TypeAck, Ref: ref, Client: &Client{Name: "eidos", Ver: "0.11.0"}},
	}
	for _, in := range cases {
		s, err := Encode(in)
		if err != nil {
			t.Fatalf("Encode(%+v) err=%v", in, err)
		}
		out, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q) err=%v", s, err)
		}
		if out.Type != TypeAck || out.Ref != in.Ref {
			t.Errorf("round-trip: got %+v, want %+v", out, in)
		}
	}
}

func TestRejectAck(t *testing.T) {
	const validRef = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	bad := []string{
		`{"v":1,"type":"ack"}`,                // missing ref
		`{"v":1,"type":"ack","ref":"short"}`,  // ref too short
		`{"v":1,"type":"ack","ref":"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"}`, // uppercase
		`{"v":1,"type":"ack","ref":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdez"}`, // non-hex
		`{"v":1,"type":"ack","ref":"` + validRef + `","text":"x"}`,                                     // chat field forbidden
		`{"v":1,"type":"ack","ref":"` + validRef + `","command":{"name":"x","args":{}}}`,               // command field forbidden
	}
	for _, c := range bad {
		if _, err := Decode(c); !errors.Is(err, ErrSchemaViolation) {
			t.Errorf("Decode(%s) err=%v, want ErrSchemaViolation", c, err)
		}
	}
}
