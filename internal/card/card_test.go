package card_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
)

// ---- existing URI form tests ----

func TestRoundtrip(t *testing.T) {
	original := card.Card{
		Npub:  "npub1abc",
		Relay: "wss://alice.host:22895",
		Label: "Alice",
	}
	uri, err := original.URI()
	if err != nil {
		t.Fatalf("URI() error: %v", err)
	}
	parsed, err := card.Parse(uri)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	// Compare only the URI-form fields; SchemaVersion/PubkeyHex/CreatedAt
	// are zero on both sides.
	if parsed.Npub != original.Npub || parsed.Relay != original.Relay || parsed.Label != original.Label {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", parsed, original)
	}
}

func TestParseRejectsBadScheme(t *testing.T) {
	_, err := card.Parse("https://example.com")
	if err == nil {
		t.Fatal("expected error for bad scheme, got nil")
	}
}

func TestParseRejectsMissingNpub(t *testing.T) {
	_, err := card.Parse("mindgate://@wss%3A%2F%2Fhost")
	if err == nil {
		t.Fatal("expected error for missing npub, got nil")
	}
}

// TestParseDecodesRelay locks the card-layer contract: Parse undoes the
// literal `/` separator URI() appends but otherwise preserves the relay
// URL exactly. Cross-form dedup (`wss://x` vs `wss://x/`) is the
// daemon's job via normRelayURL; the card layer must round-trip URI()
// output faithfully so that path-sensitive routes like
// `wss://host/nostr/` survive unchanged.
func TestParseDecodesRelay(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		// URI() output: encoded relay + literal "/" separator. Parse
		// strips exactly the separator.
		{
			"URI() output for slash-less relay",
			"mindgate://npub1abc@wss%3A%2F%2Frelay.example/?label=A",
			"wss://relay.example",
		},
		{
			"URI() output for root-slash relay",
			"mindgate://npub1abc@wss%3A%2F%2Frelay.example%2F/?label=A",
			"wss://relay.example/",
		},
		{
			"URI() output for path-tail-slash relay",
			"mindgate://npub1abc@wss%3A%2F%2Fhost%2Fnostr%2F/?label=A",
			"wss://host/nostr/",
		},
		// Hand-written URIs that skip URI()'s separator must round-trip
		// the encoded relay verbatim — no separator means nothing to
		// strip.
		{
			"hand-written no separator, no slash",
			"mindgate://npub1abc@wss%3A%2F%2Frelay.example?label=A",
			"wss://relay.example",
		},
		{
			"hand-written no separator, encoded root slash",
			"mindgate://npub1abc@wss%3A%2F%2Frelay.example%2F?label=A",
			"wss://relay.example/",
		},
		{
			"hand-written no separator, encoded path-tail slash",
			"mindgate://npub1abc@wss%3A%2F%2Fhost%2Fnostr%2F?label=A",
			"wss://host/nostr/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := card.Parse(tc.uri)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.uri, err)
			}
			if c.Relay != tc.want {
				t.Errorf("Relay=%q, want %q", c.Relay, tc.want)
			}
		})
	}
}

// TestRoundtripPreservesPathTailSlash locks the contract that
// URI()/Parse() round-trip a relay URL whose path ends in a meaningful
// trailing slash (e.g. `wss://host/nostr/`). The original implementation
// used TrimRight on the unescaped relay and silently ate the path-tail
// slash, leaving callers connecting to `wss://host/nostr` — a different
// WebSocket route at path-sensitive relays. Parse must only undo URI()'s
// own appended separator slash, not arbitrary trailing slashes.
func TestRoundtripPreservesPathTailSlash(t *testing.T) {
	original := card.Card{
		Npub:  "npub1abc",
		Relay: "wss://relay.example/nostr/",
		Label: "PathTail",
	}
	uri, err := original.URI()
	if err != nil {
		t.Fatalf("URI(): %v", err)
	}
	parsed, err := card.Parse(uri)
	if err != nil {
		t.Fatalf("Parse(%q): %v", uri, err)
	}
	if parsed.Relay != original.Relay {
		t.Errorf("Relay round-trip lost path-tail slash: got %q, want %q (uri=%q)",
			parsed.Relay, original.Relay, uri)
	}
}

// ---- TOML form tests ----

func TestCard_TOML_RoundTrip(t *testing.T) {
	in := card.Sample()
	var buf bytes.Buffer
	if err := card.Encode(&buf, in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := card.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.SchemaVersion != in.SchemaVersion || out.Label != in.Label ||
		out.PubkeyHex != in.PubkeyHex || out.Npub != in.Npub ||
		out.Relay != in.Relay || !out.CreatedAt.Equal(in.CreatedAt) {
		t.Errorf("toml round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestCard_Sample_Validates(t *testing.T) {
	if err := card.Sample().Validate(); err != nil {
		t.Errorf("Sample failed Validate: %v", err)
	}
}

func TestCard_Validate_Rejects(t *testing.T) {
	good := card.Sample()
	cases := []struct {
		name string
		mut  func(*card.Card)
		want string
	}{
		{"unknown schema_version", func(c *card.Card) { c.SchemaVersion = 2 }, "schema_version"},
		{"empty label", func(c *card.Card) { c.Label = "" }, "label"},
		{"short hex", func(c *card.Card) { c.PubkeyHex = "abc" }, "pubkey_hex"},
		{"upper-case hex", func(c *card.Card) { c.PubkeyHex = "ABCDEF" + good.PubkeyHex[6:] }, "pubkey_hex"},
		{"empty npub", func(c *card.Card) { c.Npub = "" }, "npub"},
		{"npub mismatch", func(c *card.Card) { c.PubkeyHex = "1111111111111111111111111111111111111111111111111111111111111111" }, "npub"},
		{"http relay", func(c *card.Card) { c.Relay = "http://relay.example.com" }, "home_relay"},
		{"empty relay", func(c *card.Card) { c.Relay = "" }, "home_relay"},
		{"zero created_at", func(c *card.Card) { c.CreatedAt = time.Time{} }, "created_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted invalid card: %+v", c)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestCard_WriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alice.eidos-card.toml")
	in := card.Sample()
	if err := card.Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := card.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Label != in.Label || out.PubkeyHex != in.PubkeyHex {
		t.Errorf("file round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestCard_Read_RejectsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.eidos-card.toml")
	body := `schema_version = 1
label = "X"
pubkey_hex = "not hex"
npub = ""
home_relay = "wss://r/"
created_at = 2026-01-01T00:00:00Z
`
	if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := card.Read(bad); err == nil {
		t.Errorf("Read should reject malformed card")
	}
}
