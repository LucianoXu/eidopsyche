package card

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Card holds the parsed representation of a mindgate:// URI.
type Card struct {
	Npub  string
	Relay string
	Label string
}

// URI encodes the Card as a mindgate:// URI.
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

// Parse decodes a mindgate:// URI into a Card.
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
	relayPart = strings.TrimSuffix(relayPart, "/")
	relay, err := url.PathUnescape(relayPart)
	if err != nil {
		return Card{}, fmt.Errorf("unescape relay: %w", err)
	}
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
