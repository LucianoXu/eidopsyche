// Package card holds the eidopsyche identity card — the canonical
// "self-introduction" artifact passed between humans and mind-forms.
//
// Two on-disk representations share one struct:
//
//   - mindgate:// URI         — short, paste-friendly, used in chat / email
//     / NIP-21 contexts. Round-trips Npub +
//     Relay + Label only.
//   - .eidos-card.toml file   — full v1 card. Adds SchemaVersion +
//     PubkeyHex + CreatedAt; the URI fields
//     map to TOML keys via struct tags
//     (Relay → home_relay).
//
// Use URI / Parse for the URI form; Encode / Decode / Read / Write for
// the TOML form. Validate enforces the v1 invariants required by the
// TOML form (and is also useful before producing a URI in contexts
// where the producer knows it's writing a v1 card to disk).
package card

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// Card is one identity-card. The same struct serializes both as a
// mindgate:// URI (URI/Parse) and as a TOML file (Encode/Decode).
//
// URI form populates only Npub / Relay / Label. Decoding a TOML card
// populates every field and runs Validate; the resulting Card is
// guaranteed to satisfy all v1 invariants.
type Card struct {
	SchemaVersion int       `toml:"schema_version"`
	Label         string    `toml:"label"`
	PubkeyHex     string    `toml:"pubkey_hex"`
	Npub          string    `toml:"npub"`
	Relay         string    `toml:"home_relay"`
	CreatedAt     time.Time `toml:"created_at"`
}

// URI encodes the Card as a mindgate:// URI. Reads only Npub / Relay /
// Label; other fields are ignored.
func (c Card) URI() (string, error) {
	if !strings.HasPrefix(c.Npub, "npub1") {
		return "", fmt.Errorf("npub must be bech32: %q", c.Npub)
	}
	if c.Relay == "" {
		return "", errors.New("relay required")
	}
	encodedRelay := url.PathEscape(c.Relay)
	q := url.Values{}
	if c.Label != "" {
		q.Set("label", c.Label)
	}
	out := "mindgate://" + c.Npub + "@" + encodedRelay + "/"
	if encoded := q.Encode(); encoded != "" {
		out += "?" + encoded
	}
	return out, nil
}

// Parse decodes a mindgate:// URI into a Card. Populates only Npub /
// Relay / Label; SchemaVersion / PubkeyHex / CreatedAt remain zero. To
// promote a URI-parsed card to a v1 TOML card, callers must populate
// the missing fields and call Validate.
func Parse(s string) (Card, error) {
	if !strings.HasPrefix(s, "mindgate://") {
		return Card{}, fmt.Errorf("not a mindgate URI: %q", s)
	}
	rest := strings.TrimPrefix(s, "mindgate://")
	atIdx := strings.Index(rest, "@")
	if atIdx <= 0 {
		return Card{}, errors.New("missing npub@relay separator")
	}
	npub := rest[:atIdx]
	if !strings.HasPrefix(npub, "npub1") {
		return Card{}, fmt.Errorf("npub must start with npub1: %q", npub)
	}
	tail := rest[atIdx+1:]
	queryIdx := strings.Index(tail, "?")
	var relayPart, queryPart string
	if queryIdx >= 0 {
		relayPart = tail[:queryIdx]
		queryPart = tail[queryIdx+1:]
	} else {
		relayPart = tail
	}
	relay, err := url.PathUnescape(relayPart)
	if err != nil {
		return Card{}, fmt.Errorf("unescape relay: %w", err)
	}
	// Trim AFTER unescape so an encoded `%2F` and a literal `/` produce
	// the same Card.Relay. Pre-unescape trimming missed the encoded form,
	// which then registered as a distinct relay endpoint downstream
	// (`wss://x/` opens a second WebSocket alongside `wss://x` and 503s
	// at relays whose handler is path-sensitive). TrimRight handles
	// double-slashes (`%2F/` after URI()'s separator). Scheme/host
	// validation still flows through Validate — no http→ws coercion.
	relay = strings.TrimRight(relay, "/")
	c := Card{Npub: npub, Relay: relay}
	if queryPart != "" {
		q, err := url.ParseQuery(queryPart)
		if err != nil {
			return Card{}, fmt.Errorf("parse query: %w", err)
		}
		c.Label = q.Get("label")
	}
	return c, nil
}

// Encode writes c as TOML to w. Caller is responsible for any preceding
// validation; Encode itself does not validate.
func Encode(w io.Writer, c Card) error {
	return toml.NewEncoder(w).Encode(c)
}

// Decode reads a Card from r and validates it against v1 invariants.
// Returns the validation error if the file is malformed or fails any
// check.
func Decode(r io.Reader) (Card, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return Card{}, fmt.Errorf("read card: %w", err)
	}
	var c Card
	if _, err := toml.Decode(string(body), &c); err != nil {
		return Card{}, fmt.Errorf("decode card: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Card{}, err
	}
	return c, nil
}

// Read loads a Card from path with Decode's validation.
func Read(path string) (Card, error) {
	f, err := os.Open(path)
	if err != nil {
		return Card{}, err
	}
	defer f.Close()
	return Decode(f)
}

// Write encodes c to path with mode 0o600. Mkdirs the parent (mode
// 0o700). The card body itself is public information — restricted file
// mode is defense-in-depth; nothing in the card is secret.
func Write(path string, c Card) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return Encode(f, c)
}

// hex64 matches a 64-character lowercase hexadecimal string.
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate enforces all v1 TOML-card invariants:
//
//   - schema_version == 1
//   - label, pubkey_hex, npub, home_relay, created_at all non-zero
//   - pubkey_hex: 64 lowercase hex chars
//   - npub decodes (NIP-19) bytes-equal pubkey_hex
//   - home_relay parses as ws:// or wss:// with non-empty host
//
// Decode calls Validate. Manual constructors (e.g. card.export from
// gate state) MUST also call it before Write — the file format is the
// contract; an invalid file is undefined behaviour to consumers.
func (c Card) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schema_version: want 1, got %d", c.SchemaVersion)
	}
	if c.Label == "" {
		return errors.New("label: must not be empty")
	}
	if !hex64.MatchString(c.PubkeyHex) {
		return errors.New("pubkey_hex: must be 64 lowercase hex chars")
	}
	if c.Npub == "" {
		return errors.New("npub: must not be empty")
	}
	decoded, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return fmt.Errorf("npub: %w", err)
	}
	if decoded != c.PubkeyHex {
		return fmt.Errorf("npub does not decode to pubkey_hex (got %q, want %q)", decoded, c.PubkeyHex)
	}
	if c.Relay == "" {
		return errors.New("home_relay: must not be empty")
	}
	u, err := url.Parse(c.Relay)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fmt.Errorf("home_relay: must be ws:// or wss:// URL with host (got %q)", c.Relay)
	}
	if c.CreatedAt.IsZero() {
		return errors.New("created_at: must not be zero")
	}
	return nil
}

// Sample returns a representative valid v1 Card for help text and
// docs. The hex / npub pair is freshly derived so the returned card
// always passes Validate. Sample is a function rather than a var so
// callers do not accidentally share a mutable instance.
func Sample() Card {
	// A fixed-seed pair would have been cleaner but the project's
	// identity helpers don't expose seed-deterministic generation; a
	// fresh keypair on each call is fine — Sample is for examples.
	k, err := identity.Generate()
	if err != nil {
		// identity.Generate is infallible in practice (no I/O, just a
		// secp256k1 random scalar). If it ever fails, return an
		// obviously-invalid card so anyone using Sample as a fixture
		// notices immediately.
		return Card{Label: "sample-broken"}
	}
	return Card{
		SchemaVersion: 1,
		Label:         "Alice",
		PubkeyHex:     k.PublicHex,
		Npub:          k.Npub,
		Relay:         "wss://relay.damus.io",
		CreatedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}
