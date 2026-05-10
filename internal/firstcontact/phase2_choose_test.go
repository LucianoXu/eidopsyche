package firstcontact

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/card"
)

func TestParseCardContent_URI(t *testing.T) {
	sample := card.Sample()
	uri, err := sample.URI()
	if err != nil {
		t.Fatalf("Sample.URI(): %v", err)
	}
	got, err := parseCardContent(uri)
	if err != nil {
		t.Fatalf("parseCardContent(uri): %v", err)
	}
	if got.Npub != sample.Npub || got.Relay != sample.Relay || got.Label != sample.Label {
		t.Errorf("URI parse mismatch: got %+v, want %+v", got, sample)
	}
}

func TestParseCardContent_URIIgnoresTrailingNoise(t *testing.T) {
	sample := card.Sample()
	uri, err := sample.URI()
	if err != nil {
		t.Fatalf("Sample.URI(): %v", err)
	}
	// Simulate a paste that includes the URI plus a trailing line of
	// chat noise. Only the first line should be parsed.
	got, err := parseCardContent(uri + "\nbye\n")
	if err != nil {
		t.Fatalf("parseCardContent(uri+noise): %v", err)
	}
	if got.Npub != sample.Npub {
		t.Errorf("URI+trailing parse mismatch: got %+v, want npub=%s", got, sample.Npub)
	}
}

func TestParseCardContent_URIRejectsMissingLabel(t *testing.T) {
	sample := card.Sample()
	uri := "mindgate://" + sample.Npub + "@wss%3A%2F%2Frelay.example/" // no ?label=
	_, err := parseCardContent(uri)
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Errorf("expected missing-label error, got %v", err)
	}
}

func TestParseCardContent_URIRejectsBadNpub(t *testing.T) {
	// Looks like an npub (passes Parse's prefix check) but does not
	// decode bech32. Must be rejected.
	uri := "mindgate://npub1notvalid@wss%3A%2F%2Frelay.example/?label=Alice"
	_, err := parseCardContent(uri)
	if err == nil {
		t.Fatal("expected error for non-decodable npub, got nil")
	}
}

func TestParseCardContent_URIRejectsBadRelayScheme(t *testing.T) {
	sample := card.Sample()
	// Build a URI by hand with an http:// relay.
	uri := "mindgate://" + sample.Npub + "@http%3A%2F%2Frelay.example/?label=Alice"
	_, err := parseCardContent(uri)
	if err == nil || !strings.Contains(err.Error(), "home_relay") {
		t.Errorf("expected home_relay error, got %v", err)
	}
}

func TestParseCardContent_TOML(t *testing.T) {
	sample := card.Sample()
	var buf bytes.Buffer
	if err := card.Encode(&buf, sample); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := parseCardContent(buf.String())
	if err != nil {
		t.Fatalf("parseCardContent(toml): %v", err)
	}
	if got.PubkeyHex != sample.PubkeyHex || got.Npub != sample.Npub ||
		got.Label != sample.Label || got.Relay != sample.Relay {
		t.Errorf("TOML parse mismatch: got %+v, want %+v", got, sample)
	}
}

func TestParseCardContent_TOMLRejectsInvalid(t *testing.T) {
	// Missing schema_version triggers Validate failure.
	_, err := parseCardContent(`label = "X"`)
	if err == nil {
		t.Fatal("expected error for incomplete TOML, got nil")
	}
}

func TestParseCardContent_Empty(t *testing.T) {
	if _, err := parseCardContent("   \n\n   "); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

// --- interactive flow tests ---

// TestPhase2_PasteSummonsCard exercises the full menu walk: action
// menu → "use card" → source menu → "paste" → URI → card applied.
func TestPhase2_PasteSummonsCard(t *testing.T) {
	sample := card.Sample()
	uri, err := sample.URI()
	if err != nil {
		t.Fatalf("Sample.URI(): %v", err)
	}
	r := &fakeRenderer{
		choices: []int{2, 1}, // action=card (with all 3 options visible), source=paste
		prompts: []string{uri},
	}
	s := &Summoning{Lang: "en", OperatorPresent: true}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{EntryMode: EntryBareEidos})
	if err != nil {
		t.Fatalf("Phase2: %v", err)
	}
	if got != Phase2SummonCard {
		t.Errorf("got %v, want Phase2SummonCard", got)
	}
	if s.MasterNpub != sample.Npub || s.MasterLabel != sample.Label || s.HomeRelay != sample.Relay {
		t.Errorf("summoning state not populated from card: got %+v", s)
	}
}

// TestPhase2_FileBranchStillWorks confirms the file path is still
// reachable after the new source menu was introduced.
func TestPhase2_FileBranchStillWorks(t *testing.T) {
	sample := card.Sample()
	dir := t.TempDir()
	path := filepath.Join(dir, "card.toml")
	var buf bytes.Buffer
	if err := card.Encode(&buf, sample); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := &fakeRenderer{
		choices: []int{2, 0}, // action=card, source=file
		prompts: []string{path},
	}
	s := &Summoning{Lang: "en", OperatorPresent: true}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{EntryMode: EntryBareEidos})
	if err != nil {
		t.Fatalf("Phase2: %v", err)
	}
	if got != Phase2SummonCard {
		t.Errorf("got %v, want Phase2SummonCard", got)
	}
	if s.MasterNpub != sample.Npub {
		t.Errorf("file branch did not populate npub: got %q", s.MasterNpub)
	}
}

// TestPhase2_MasterCardFlagWins confirms --master-card still bypasses
// the menu entirely.
func TestPhase2_MasterCardFlagWins(t *testing.T) {
	sample := card.Sample()
	dir := t.TempDir()
	path := filepath.Join(dir, "card.toml")
	var buf bytes.Buffer
	if err := card.Encode(&buf, sample); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := &fakeRenderer{} // must not be consulted
	s := &Summoning{Lang: "en", OperatorPresent: true}
	got, err := Phase2(context.Background(), s, r, Phase2Deps{
		EntryMode:      EntrySummon,
		MasterCardPath: path,
	})
	if err != nil {
		t.Fatalf("Phase2: %v", err)
	}
	if got != Phase2SummonCard {
		t.Errorf("got %v, want Phase2SummonCard", got)
	}
	if s.MasterNpub != sample.Npub {
		t.Errorf("flag branch did not populate npub: got %q", s.MasterNpub)
	}
}
