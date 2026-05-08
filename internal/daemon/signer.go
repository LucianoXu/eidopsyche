package daemon

import (
	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// keypairSigner adapts identity.Keypair to nostr.Signer. Used by the daemon
// to feed its long-term identity key into Pool's NIP-42 AUTH responder
// without introducing a method name that collides with Keypair.PublicHex
// (which is a field, not a method).
type keypairSigner struct {
	k *identity.Keypair
}

func (s keypairSigner) Sign(ev *gnostr.Event) error { return ev.Sign(s.k.PrivateHex) }
func (s keypairSigner) PublicHex() string           { return s.k.PublicHex }
