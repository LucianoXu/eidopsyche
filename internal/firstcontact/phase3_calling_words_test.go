package firstcontact

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPhase3CallingWords_Scratch_AcceptsDefault(t *testing.T) {
	s := &Summoning{
		Lang:         "en",
		SummonedName: "Lyra",
		MasterLabel:  "Bob",
		MasterNpub:   "npub1master",
		MindFormNpub: "npub1self",
		HomeRelay:    "wss://relay.example",
		Displaying:   "A figure stands in the mist.",
		PrefabID:     "",
	}
	r := &fakeRenderer{} // scriptedEdits empty: returns default.
	c := &Claude{Run: func(ctx context.Context, args []string) (string, error) {
		return `{"type":"result","subtype":"success","is_error":false,"result":"Welcome, Lyra."}`, nil
	}}
	if err := Phase3CallingWords(context.Background(), s, r, c); err != nil {
		t.Fatalf("Phase3CallingWords: %v", err)
	}
	if s.CallingWords != "Welcome, Lyra." {
		t.Errorf("CallingWords = %q, want %q", s.CallingWords, "Welcome, Lyra.")
	}
}

func TestPhase3CallingWords_Scratch_OperatorEditOverridesDefault(t *testing.T) {
	s := &Summoning{Lang: "en", SummonedName: "Lyra", MasterLabel: "Bob", Displaying: "..."}
	r := &fakeRenderer{scriptedEdits: []string{"Different words, my own."}}
	c := &Claude{Run: func(ctx context.Context, args []string) (string, error) {
		return `{"type":"result","subtype":"success","is_error":false,"result":"Default draft."}`, nil
	}}
	if err := Phase3CallingWords(context.Background(), s, r, c); err != nil {
		t.Fatalf("Phase3CallingWords: %v", err)
	}
	if s.CallingWords != "Different words, my own." {
		t.Errorf("operator edit not honoured; got %q", s.CallingWords)
	}
}

func TestPhase3CallingWords_Scratch_EmptyEditAllowed(t *testing.T) {
	s := &Summoning{Lang: "en", SummonedName: "Lyra", MasterLabel: "Bob", Displaying: "..."}
	r := &fakeRenderer{scriptedEdits: []string{""}}
	c := &Claude{Run: func(ctx context.Context, args []string) (string, error) {
		return `{"type":"result","subtype":"success","is_error":false,"result":"Default draft."}`, nil
	}}
	if err := Phase3CallingWords(context.Background(), s, r, c); err != nil {
		t.Fatalf("empty edit should not error: %v", err)
	}
	if s.CallingWords != "" {
		t.Errorf("CallingWords = %q, want empty", s.CallingWords)
	}
}

func TestPhase3CallingWords_Prefab_UsesTestFixtureTpl(t *testing.T) {
	s := &Summoning{
		Lang:         "en",
		SummonedName: "Lyra",
		MasterLabel:  "alice",
		MasterNpub:   "npub1master",
		MindFormNpub: "npub1self",
		HomeRelay:    "wss://relay.example",
		PrefabID:     "_test_fixture",
	}
	r := &fakeRenderer{} // accepts default verbatim
	if err := Phase3CallingWords(context.Background(), s, r, nil); err != nil {
		t.Fatalf("Phase3CallingWords (prefab): %v", err)
	}
	if s.CallingWords == "" {
		t.Fatalf("CallingWords empty after prefab path; expected rendered tpl")
	}
	if !strings.Contains(s.CallingWords, "Lyra") {
		t.Errorf("prefab calling-words missing label substitution: %q", s.CallingWords)
	}
}

func TestPhase3CallingWords_Prefab_NoClaudeNeeded(t *testing.T) {
	s := &Summoning{Lang: "en", SummonedName: "Lyra", MasterLabel: "alice", PrefabID: "_test_fixture"}
	r := &fakeRenderer{}
	c := &Claude{Run: func(ctx context.Context, args []string) (string, error) {
		return "", errors.New("claude should not have been called on prefab path")
	}}
	if err := Phase3CallingWords(context.Background(), s, r, c); err != nil {
		t.Fatalf("Phase3CallingWords (prefab): %v", err)
	}
}
