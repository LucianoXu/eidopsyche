// Package nostr provides NIP-17 gift-wrap helpers and a multi-relay pool.
package nostr

import (
	"context"
	"fmt"

	gnostr "github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/nbd-wtf/go-nostr/nip59"
)

// Wrap produces a recipient-addressed gift wrap (kind 1059) for the given
// content. Internally it builds a kind-14 chat rumor and a kind-13 seal per
// NIP-17.
//
//   - senderSK    — sender's private key hex
//   - recipientPK — recipient's public key hex (32 bytes)
//   - content     — plaintext message
//
// Returns the wrap event, the inner rumor's ID, and any error.
func Wrap(senderSK, recipientPK, content string) (wrap *gnostr.Event, rumorID string, err error) {
	return WrapKind(senderSK, recipientPK, content, gnostr.KindDirectMessage)
}

// WrapKind builds a NIP-17 gift wrap with a custom inner rumor kind.
// senderSK and recipientPK are hex-encoded. The kind field is set on the inner
// rumor before encrypting; the outer wrap is always kind 1059 per NIP-17.
func WrapKind(senderSK, recipientPK, content string, kind int) (wrap *gnostr.Event, rumorID string, err error) {
	senderPK, err := gnostr.GetPublicKey(senderSK)
	if err != nil {
		return nil, "", fmt.Errorf("derive sender pubkey: %w", err)
	}

	rumor := gnostr.Event{
		Kind:      kind,
		Content:   content,
		Tags:      gnostr.Tags{gnostr.Tag{"p", recipientPK}},
		CreatedAt: gnostr.Now(),
		PubKey:    senderPK,
	}
	rumor.ID = rumor.GetID()
	rumorID = rumor.ID

	// encryptFn uses NIP-44 from the sender's perspective.
	encryptFn := func(plaintext string) (string, error) {
		convKey, kerr := nip44.GenerateConversationKey(recipientPK, senderSK)
		if kerr != nil {
			return "", fmt.Errorf("generate conversation key: %w", kerr)
		}
		return nip44.Encrypt(plaintext, convKey)
	}

	// signFn signs using the sender's private key.
	signFn := func(ev *gnostr.Event) error {
		return ev.Sign(senderSK)
	}

	wrapEv, err := nip59.GiftWrap(rumor, recipientPK, encryptFn, signFn, nil)
	if err != nil {
		return nil, "", fmt.Errorf("gift wrap: %w", err)
	}

	return &wrapEv, rumorID, nil
}

// Unwrap opens a kind-1059 gift wrap addressed to receiverSK and returns the
// inner kind-14 rumor. The returned rumor's PubKey is the sender's public key
// (extracted from the seal's signature).
func Unwrap(receiverSK string, ev *gnostr.Event) (*gnostr.Event, error) {
	ctx := context.Background()
	_ = ctx // kept for potential future use

	// decryptFn decrypts the seal using receiver's private key and the
	// ephemeral nonce key found in the gift-wrap's PubKey field.
	decryptFn := func(otherPubKey, ciphertext string) (string, error) {
		convKey, err := nip44.GenerateConversationKey(otherPubKey, receiverSK)
		if err != nil {
			return "", fmt.Errorf("generate conversation key: %w", err)
		}
		return nip44.Decrypt(ciphertext, convKey)
	}

	rumor, err := nip59.GiftUnwrap(*ev, decryptFn)
	if err != nil {
		return nil, fmt.Errorf("gift unwrap: %w", err)
	}

	return &rumor, nil
}
