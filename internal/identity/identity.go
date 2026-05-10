package identity

import (
	"fmt"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// Keypair holds both the private and public sides of a secp256k1 keypair,
// plus their NIP-19 bech32 representations.
type Keypair struct {
	PrivateHex string // 64 hex chars (32 bytes)
	PublicHex  string // 64 hex chars (32 bytes)
	Npub       string // bech32 npub1...
	Nsec       string // bech32 nsec1...
}

// Generate creates a new random secp256k1 keypair.
func Generate() (*Keypair, error) {
	priv := nostr.GeneratePrivateKey()
	return fromPrivate(priv)
}

// DerivePublic returns the hex-encoded public key for a given hex-encoded private key.
func DerivePublic(privHex string) (string, error) {
	return nostr.GetPublicKey(privHex)
}

// DecodeNpub returns the hex pubkey for a bech32 npub string.
func DecodeNpub(s string) (string, error) {
	prefix, data, err := nip19.Decode(s)
	if err != nil {
		return "", err
	}
	if prefix != "npub" {
		return "", fmt.Errorf("expected npub prefix, got %s", prefix)
	}
	pub, ok := data.(string)
	if !ok {
		return "", fmt.Errorf("unexpected nip19 payload type %T", data)
	}
	return pub, nil
}

// EncodeNpub returns the bech32 npub string for a hex-encoded public key.
// Used by the daemon when displaying contacts.
func EncodeNpub(hexPub string) (string, error) {
	return nip19.EncodePublicKey(hexPub)
}

// DecodeNsec returns the hex private key for a bech32 nsec string.
// Mirror of DecodeNpub for the wizard's identity-import branch.
func DecodeNsec(s string) (string, error) {
	prefix, data, err := nip19.Decode(s)
	if err != nil {
		return "", err
	}
	if prefix != "nsec" {
		return "", fmt.Errorf("expected nsec prefix, got %s", prefix)
	}
	priv, ok := data.(string)
	if !ok {
		return "", fmt.Errorf("unexpected nip19 payload type %T", data)
	}
	return priv, nil
}

// FromHex constructs a Keypair from an existing hex-encoded private key.
func FromHex(privHex string) (*Keypair, error) {
	return fromPrivate(privHex)
}

// fromPrivate is the internal constructor: derives the full Keypair from a private key hex.
func fromPrivate(privHex string) (*Keypair, error) {
	pub, err := nostr.GetPublicKey(privHex)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	npub, err := nip19.EncodePublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("encode npub: %w", err)
	}
	nsec, err := nip19.EncodePrivateKey(privHex)
	if err != nil {
		return nil, fmt.Errorf("encode nsec: %w", err)
	}
	return &Keypair{
		PrivateHex: privHex,
		PublicHex:  pub,
		Npub:       npub,
		Nsec:       nsec,
	}, nil
}
