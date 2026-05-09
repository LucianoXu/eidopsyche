package firstcontact_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
)

type fakeClaudeRunner struct {
	queue []string
	err   error
	calls int
}

func (f *fakeClaudeRunner) Run(_ context.Context, _ []string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.calls >= len(f.queue) {
		return "", errors.New("no canned response")
	}
	out := f.queue[f.calls]
	f.calls++
	return out, nil
}

func envelope(result string) string {
	return `{"type":"result","subtype":"success","is_error":false,"result":` + jsonString(result) + `}`
}

func jsonString(s string) string {
	// Minimal JSON-escape: cover " and \. Tests pass simple inputs.
	var out []byte
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, []byte(string(r))...)
		}
	}
	out = append(out, '"')
	return string(out)
}

func TestClaude_CallParsesJSONResult(t *testing.T) {
	r := &fakeClaudeRunner{queue: []string{
		envelope(`{"archetype":"sage","temperament":"still","world":"mountain"}`),
	}}
	c := &firstcontact.Claude{Run: r.Run}
	var p firstcontact.CharacterProfile
	if err := c.Call(context.Background(), "test", &p); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if p.Archetype != "sage" || p.World != "mountain" {
		t.Errorf("unexpected profile: %+v", p)
	}
}

func TestClaude_CallRetriesOnceOnParseError(t *testing.T) {
	r := &fakeClaudeRunner{queue: []string{
		envelope(`not valid json at all`),
		envelope(`{"archetype":"sage"}`),
	}}
	c := &firstcontact.Claude{Run: r.Run}
	var p firstcontact.CharacterProfile
	if err := c.Call(context.Background(), "test", &p); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if p.Archetype != "sage" {
		t.Errorf("retry did not pick up second response: %+v", p)
	}
	if r.calls != 2 {
		t.Errorf("calls = %d, want 2", r.calls)
	}
}

func TestClaude_CallAbortsAfterTwoParseErrors(t *testing.T) {
	r := &fakeClaudeRunner{queue: []string{
		envelope("garbage"),
		envelope("still garbage"),
	}}
	c := &firstcontact.Claude{Run: r.Run}
	var p firstcontact.CharacterProfile
	if err := c.Call(context.Background(), "test", &p); err == nil {
		t.Errorf("expected error after two parse failures")
	}
}

func TestClaude_CallText(t *testing.T) {
	r := &fakeClaudeRunner{queue: []string{
		envelope("the displaying paragraph"),
	}}
	c := &firstcontact.Claude{Run: r.Run}
	got, err := c.CallText(context.Background(), "displaying")
	if err != nil {
		t.Fatalf("CallText: %v", err)
	}
	if got != "the displaying paragraph" {
		t.Errorf("CallText = %q", got)
	}
}

func TestClaude_EmptyResultIsError(t *testing.T) {
	r := &fakeClaudeRunner{queue: []string{envelope("")}}
	c := &firstcontact.Claude{Run: r.Run}
	if _, err := c.CallText(context.Background(), "anything"); err == nil {
		t.Errorf("expected error on empty result")
	}
}

func TestClaude_NilSchemaRejected(t *testing.T) {
	c := &firstcontact.Claude{Run: (&fakeClaudeRunner{}).Run}
	if err := c.Call(context.Background(), "x", nil); err == nil {
		t.Errorf("expected nil-schema rejection")
	}
}

func TestClaude_RunErrorBubbled(t *testing.T) {
	r := &fakeClaudeRunner{err: errors.New("not on PATH")}
	c := &firstcontact.Claude{Run: r.Run}
	if _, err := c.CallText(context.Background(), "x"); err == nil {
		t.Errorf("expected runner error to surface")
	}
}

// TestClaude_CallStripsMarkdownFence pins the fix for the bug observed
// during manual smoke: claude wraps JSON in ```json ... ``` despite the
// prompt asking for raw JSON. The parser must tolerate this.
func TestClaude_CallStripsMarkdownFence(t *testing.T) {
	wrapped := "```json\n" +
		`{"archetype":"sage","temperament":"still"}` + "\n```"
	r := &fakeClaudeRunner{queue: []string{envelope(wrapped)}}
	c := &firstcontact.Claude{Run: r.Run}
	var p firstcontact.CharacterProfile
	if err := c.Call(context.Background(), "test", &p); err != nil {
		t.Fatalf("Call should tolerate fence: %v", err)
	}
	if p.Archetype != "sage" {
		t.Errorf("Archetype = %q, want sage", p.Archetype)
	}
}

// TestClaude_CallStripsBareTripleBacktick covers the same fix without a
// language tag (just ``` ... ```).
func TestClaude_CallStripsBareTripleBacktick(t *testing.T) {
	wrapped := "```\n" + `{"archetype":"sage"}` + "\n```\n"
	r := &fakeClaudeRunner{queue: []string{envelope(wrapped)}}
	c := &firstcontact.Claude{Run: r.Run}
	var p firstcontact.CharacterProfile
	if err := c.Call(context.Background(), "test", &p); err != nil {
		t.Fatalf("Call should tolerate bare fence: %v", err)
	}
	if p.Archetype != "sage" {
		t.Errorf("Archetype = %q", p.Archetype)
	}
}
