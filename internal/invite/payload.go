// Package invite implements MindGate invite token creation and verification.
//
// An invite token is a self-contained signed credential that lets a redeemer
// establish mutual contact with the issuer in one step, instead of the
// four-step symmetric OOB exchange.
package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// TokenScheme is the URI scheme prefix for invite tokens.
const TokenScheme = "mindgate-invite://"

// Payload holds the canonical fields of a MindGate invite token.
// The JSON field names are fixed (spec §3); do not reorder.
type Payload struct {
	V                 int    `json:"v"`
	IssuerNpub        string `json:"issuer_npub"`
	IssuerRelay       string `json:"issuer_relay"`
	IssuerLabelHint   string `json:"issuer_label_hint"`
	RedeemerLabelHint string `json:"redeemer_label_hint"`
	ID                string `json:"id"`
	ExpiresAt         int64  `json:"expires_at"`
	MaxUses           int    `json:"max_uses"`
	Sig               string `json:"sig,omitempty"`
}

// CanonicalForSigning returns the deterministic JSON of the payload with sig=""
// so both sides hash the same bytes. Keys are sorted lexicographically by
// json.Marshal on a map[string]any (Go's map marshaling sorts keys).
func (p Payload) CanonicalForSigning() ([]byte, error) {
	// Build with sig explicitly empty so the sig field is included in the JSON
	// but its value is the empty string. This makes the canonical form
	// unambiguous regardless of omitempty.
	tmp := map[string]any{
		"v":                   p.V,
		"issuer_npub":         p.IssuerNpub,
		"issuer_relay":        p.IssuerRelay,
		"issuer_label_hint":   p.IssuerLabelHint,
		"redeemer_label_hint": p.RedeemerLabelHint,
		"id":                  p.ID,
		"expires_at":          p.ExpiresAt,
		"max_uses":            p.MaxUses,
		"sig":                 "",
	}
	return json.Marshal(tmp)
}

// Sign fills p.Sig using issuerSK (hex private key).
// It is an error to call Sign when p.IssuerNpub does not derive from issuerSK.
func (p *Payload) Sign(issuerSK string) error {
	expectedPK, err := gnostr.GetPublicKey(issuerSK)
	if err != nil {
		return fmt.Errorf("derive public key: %w", err)
	}
	derivedNpub, err := identity.EncodeNpub(expectedPK)
	if err != nil {
		return fmt.Errorf("encode npub: %w", err)
	}
	if derivedNpub != p.IssuerNpub {
		return errors.New("issuerSK does not match IssuerNpub")
	}

	canonical, err := p.CanonicalForSigning()
	if err != nil {
		return fmt.Errorf("canonical json: %w", err)
	}

	digest := sha256.Sum256(canonical)

	privKeyBytes, err := hex.DecodeString(issuerSK)
	if err != nil {
		return fmt.Errorf("decode private key: %w", err)
	}
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	sig, err := schnorr.Sign(privKey, digest[:], schnorr.FastSign())
	if err != nil {
		return fmt.Errorf("schnorr sign: %w", err)
	}

	p.Sig = hex.EncodeToString(sig.Serialize())
	return nil
}

// Verify returns nil iff p.Sig is a valid Schnorr signature over
// CanonicalForSigning() by the public key derived from p.IssuerNpub.
func (p Payload) Verify() error {
	if p.Sig == "" {
		return errors.New("missing signature")
	}

	canonical, err := p.CanonicalForSigning()
	if err != nil {
		return fmt.Errorf("canonical json: %w", err)
	}
	digest := sha256.Sum256(canonical)

	sigBytes, err := hex.DecodeString(p.Sig)
	if err != nil {
		return fmt.Errorf("decode sig hex: %w", err)
	}
	sig, err := schnorr.ParseSignature(sigBytes)
	if err != nil {
		return fmt.Errorf("parse sig: %w", err)
	}

	pubHex, err := identity.DecodeNpub(p.IssuerNpub)
	if err != nil {
		return fmt.Errorf("decode npub: %w", err)
	}
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		return fmt.Errorf("decode pubkey hex: %w", err)
	}

	pubKey, err := schnorr.ParsePubKey(pubBytes)
	if err != nil {
		return fmt.Errorf("parse pubkey: %w", err)
	}

	if !sig.Verify(digest[:], pubKey) {
		return errors.New("signature verification failed")
	}
	return nil
}

// RandomID returns 32 random bytes hex-encoded (64 chars).
func RandomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
