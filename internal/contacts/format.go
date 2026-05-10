package contacts

import "fmt"

// shortHexPrefix is the number of leading hex chars to keep before the
// truncation ellipsis. 7 hex chars = 28 bits — enough to disambiguate
// at a glance among the handful of pubkeys an operator interacts with.
const shortHexPrefix = 7

// FormatPubkey returns a display string for a pubkey: the contact label
// when known, otherwise a short hex prefix with an ellipsis. Used at
// every operator-facing surface (CLI rows, dashboard, daemon logs) so
// naming stays consistent.
func FormatPubkey(label, pubkey string) string {
	if label != "" {
		return label
	}
	return ShortHex(pubkey)
}

// ShortHex truncates a hex pubkey to a short prefix followed by an
// ellipsis. Pubkeys already at or below the prefix length are returned
// verbatim — there is nothing to truncate.
func ShortHex(pubkey string) string {
	if len(pubkey) <= shortHexPrefix {
		return pubkey
	}
	return pubkey[:shortHexPrefix] + "…"
}

// FormatPubkeyWithHex returns "label (abc1234…)" when a label is known
// and "abc1234…" otherwise. Used by daemon log entries where operators
// benefit from seeing both the friendly name AND the disambiguating
// hex prefix in one place.
func FormatPubkeyWithHex(label, pubkey string) string {
	short := ShortHex(pubkey)
	if label == "" {
		return short
	}
	return fmt.Sprintf("%s (%s)", label, short)
}
