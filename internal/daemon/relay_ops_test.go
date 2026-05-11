package daemon

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// TestAddOwnRelay_CanonicalizesURL locks the invariant that
// `wss://x` and `wss://x/` collide on the unique relay_url constraint:
// without canonicalization the daemon would open two redundant WebSocket
// connections to the same Nostr endpoint (the trailing-slash form 503s
// at some relays, e.g. relay.damus.io).
func TestAddOwnRelay_CanonicalizesURL(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	if err := d.AddOwnRelay(ctx, "wss://relay.example/", "home"); err != nil {
		t.Fatalf("AddOwnRelay first: %v", err)
	}
	err := d.AddOwnRelay(ctx, "wss://relay.example", "fallback")
	if !errors.Is(err, errOwnRelayDuplicate) {
		t.Fatalf("AddOwnRelay second: err=%v, want errOwnRelayDuplicate", err)
	}

	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		t.Fatalf("ListOwnRelays: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly one row after canonical dedup, got %d: %+v", len(rows), rows)
	}
	if rows[0].URL != "wss://relay.example" {
		t.Errorf("URL=%q, want canonical wss://relay.example", rows[0].URL)
	}
}

// TestCanonicalizeOwnRelays_RewritesLegacyRows asserts the one-shot
// startup migration: pre-existing rows whose URL doesn't match
// normRelayURL get rewritten in place, so the post-fix exact-match
// RemoveOwnRelay can still find them by their canonical key. Without
// this, legacy databases would carry trailing-slash rows the new code
// path can never lookup.
func TestCanonicalizeOwnRelays_RewritesLegacyRows(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	// Hand-insert legacy rows that pre-date normalization.
	for _, row := range []struct {
		url, role string
	}{
		{"wss://home.example/", "home"},            // root-slash legacy
		{"wss://Fallback.Example/", "fallback"},    // uppercase host legacy
		{"wss://relay.example/nostr/", "fallback"}, // path-tail slash (should be preserved)
	} {
		if _, err := d.DB.ExecContext(ctx,
			`INSERT INTO own_relays(relay_url, role, added_at) VALUES(?, ?, ?)`,
			row.url, row.role, time.Now().Unix()); err != nil {
			t.Fatalf("seed %q: %v", row.url, err)
		}
	}

	if err := canonicalizeOwnRelays(ctx, d.DB.DB); err != nil {
		t.Fatalf("canonicalizeOwnRelays: %v", err)
	}

	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		t.Fatalf("ListOwnRelays: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.URL+"#"+r.Role)
	}
	sort.Strings(got)
	want := []string{
		"wss://fallback.example#fallback",
		"wss://home.example#home",
		"wss://relay.example/nostr/#fallback",
	}
	if len(got) != len(want) {
		t.Fatalf("rows=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCanonicalizeOwnRelays_CollisionPrefersHome covers the merge path:
// if both a canonical and a legacy row exist (e.g. an operator added
// `wss://x` recently AND has a pre-fix `wss://x/` row), the canonical
// row survives. If the legacy row was home, the surviving canonical
// row is promoted to home so we never silently drop a home relay
// during the migration.
func TestCanonicalizeOwnRelays_CollisionPrefersHome(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO own_relays(relay_url, role, added_at) VALUES(?, ?, ?)`,
		"wss://x.example", "fallback", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO own_relays(relay_url, role, added_at) VALUES(?, ?, ?)`,
		"wss://x.example/", "home", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	if err := canonicalizeOwnRelays(ctx, d.DB.DB); err != nil {
		t.Fatalf("canonicalizeOwnRelays: %v", err)
	}

	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after migration: %d rows, want 1; got %+v", len(rows), rows)
	}
	if rows[0].URL != "wss://x.example" {
		t.Errorf("URL=%q, want wss://x.example", rows[0].URL)
	}
	if rows[0].Role != "home" {
		t.Errorf("Role=%q, want home (promoted from the merged-out legacy row)", rows[0].Role)
	}
}

// TestRemoveOwnRelay_NormalizesInput locks the contract that
// RemoveOwnRelay treats `wss://x/` and `wss://x` as the same key. Without
// this, an operator who adds `wss://x/` (stored canonical as `wss://x`)
// and removes the same form they typed sees errOwnRelayNotFound from the
// exact-match DELETE.
func TestRemoveOwnRelay_NormalizesInput(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	if err := d.AddOwnRelay(ctx, "wss://home.example", "home"); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	if err := d.AddOwnRelay(ctx, "wss://fallback.example", "fallback"); err != nil {
		t.Fatalf("seed fallback: %v", err)
	}

	// Remove with trailing-slash form even though storage canonicalized.
	if err := d.RemoveOwnRelay(ctx, "wss://fallback.example/"); err != nil {
		t.Fatalf("RemoveOwnRelay with trailing slash: %v", err)
	}

	rows, err := d.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].URL != "wss://home.example" {
		t.Errorf("after remove: rows=%+v, want only [wss://home.example]", rows)
	}
}

// TestPublishTargets_NormalizesAndDedupes asserts that publishTargets
// folds trailing-slash variants from the three input layers (own_relays,
// recipient relays, configured fallbacks) into a single entry. Without
// this, a contact relay `wss://x/` and an own_relay `wss://x` would
// each open their own WebSocket to the same Nostr endpoint, reopening
// the duplicate-connection bug this PR is meant to eliminate.
func TestPublishTargets_NormalizesAndDedupes(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	if err := d.AddOwnRelay(ctx, "wss://relay.example", "home"); err != nil {
		t.Fatal(err)
	}
	d.Cfg.Publish.FallbackRelays = []string{
		"wss://relay.example/",       // trailing-slash variant of the own_relay
		"wss://Other.Example/nostr/", // path-tail slash + mixed case
	}
	recipient := []string{
		"wss://Relay.Example",        // mixed-case variant
		"wss://other.example/nostr/", // identical (lowercased) to fallback after norm
	}

	urls, err := d.publishTargets(ctx, recipient)
	if err != nil {
		t.Fatalf("publishTargets: %v", err)
	}
	sort.Strings(urls)
	want := []string{
		"wss://other.example/nostr/",
		"wss://relay.example",
	}
	if len(urls) != len(want) {
		t.Fatalf("urls=%v, want %v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Errorf("url[%d]=%q, want %q", i, urls[i], want[i])
		}
	}
}

func TestNormRelayURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"strip root trailing slash", "wss://relay.example/", "wss://relay.example"},
		{"no slash unchanged", "wss://relay.example", "wss://relay.example"},
		{"lowercase host", "wss://Relay.Example/", "wss://relay.example"},
		{"trim surrounding space", "  wss://relay.example/  ", "wss://relay.example"},
		{"empty stays empty", "", ""},
		// Path-tail slash must be preserved: path-sensitive relays treat
		// `/nostr/` and `/nostr` as different WebSocket routes. Regression
		// guard for the codex review of 2026-05-11 (delegating wholesale
		// to go-nostr's NormalizeURL would have eaten this slash).
		{"preserve path-tail slash", "wss://relay.example/nostr/", "wss://relay.example/nostr/"},
		{"preserve path with no trailing slash", "wss://relay.example/nostr", "wss://relay.example/nostr"},
		// Non-relay schemes pass through unchanged so isRelayURL stays the
		// single source of truth for validation.
		{"unknown scheme unchanged", "http://example.com/", "http://example.com/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normRelayURL(tc.in); got != tc.want {
				t.Errorf("normRelayURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
