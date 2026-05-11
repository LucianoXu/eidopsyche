package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dashboard"
)

// OwnRelayRow is the typed shape returned by ListOwnRelays. AddedAt is a
// Unix timestamp (0 if the row predates the column or the column is null).
type OwnRelayRow struct {
	URL     string
	Role    string
	AddedAt int64
}

// errOwnRelay* are the sentinel errors AddOwnRelay and RemoveOwnRelay
// return for input validation. The IPC dispatch maps them onto
// ipc.ErrInvalidParams via errors.Is so the wire mapping doesn't depend
// on the error string.
var (
	errOwnRelayInvalidURL   = errors.New("relay: url must be a non-empty ws:// or wss:// URL")
	errOwnRelayInvalidRole  = errors.New("relay: role must be home or fallback")
	errOwnRelayDuplicate    = errors.New("relay: url already in own_relays")
	errOwnRelayNotFound     = errors.New("relay: url not in own_relays")
	errOwnRelayHomeRequired = errors.New("relay: at least one home relay must remain")
)

// ListOwnRelays returns the rows of own_relays ordered by role
// (home > fallback) then URL. Roles outside that pair are returned at
// the bottom — they're forbidden by AddOwnRelay but a future schema
// migration may introduce more, so the helper doesn't filter.
func (d *Daemon) ListOwnRelays(ctx context.Context) ([]OwnRelayRow, error) {
	rows, err := d.DB.QueryContext(ctx,
		`SELECT relay_url, role, COALESCE(added_at, 0) FROM own_relays
		 ORDER BY CASE role WHEN 'home' THEN 0 WHEN 'fallback' THEN 1 ELSE 2 END,
		          relay_url`)
	if err != nil {
		return nil, fmt.Errorf("query own_relays: %w", err)
	}
	defer rows.Close()
	var out []OwnRelayRow
	for rows.Next() {
		var r OwnRelayRow
		if err := rows.Scan(&r.URL, &r.Role, &r.AddedAt); err != nil {
			return nil, fmt.Errorf("scan own_relays: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddOwnRelay inserts a relay into own_relays with the given role. URL
// must parse as ws:// or wss://; role must be "home" or "fallback".
// Duplicate URLs return errOwnRelayDuplicate (caller renders that
// inline rather than 500-ing). Emits dash relay.added on success.
func (d *Daemon) AddOwnRelay(ctx context.Context, rawURL, role string) error {
	rawURL = strings.TrimSpace(rawURL)
	if !isRelayURL(rawURL) {
		return errOwnRelayInvalidURL
	}
	rawURL = normRelayURL(rawURL)
	if role != "home" && role != "fallback" {
		return errOwnRelayInvalidRole
	}
	res, err := d.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO own_relays(relay_url, role, added_at) VALUES(?, ?, ?)`,
		rawURL, role, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("insert own_relay: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errOwnRelayDuplicate
	}
	d.Refresh()
	d.emitDashEvent(dashboard.Event{Kind: "relay.added"})
	return nil
}

// RemoveOwnRelay deletes a relay row by exact URL. Returns
// errOwnRelayNotFound if no row matched (the dashboard renders this as
// a 404), errOwnRelayHomeRequired if removing the row would leave zero
// home relays (the daemon needs at least one home target to publish
// own messages, so we refuse here as defense-in-depth — the dashboard
// also gates this behind the typed-confirm modal). Emits relay.removed.
//
// The role read, home-count check, and DELETE run inside a single
// transaction so two concurrent removes can't both pass the
// `homeCount <= 1` guard and leave zero home relays — SQLite's busy
// handler serialises the txn, the second caller sees the post-DELETE
// state, and its guard correctly refuses.
func (d *Daemon) RemoveOwnRelay(ctx context.Context, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errOwnRelayInvalidURL
	}
	// Match the canonicalization AddOwnRelay applies on insert. Without
	// this, a caller who added `wss://x/` (stored as `wss://x` after
	// normalize) and then tried to remove the same input form would hit
	// errOwnRelayNotFound on the exact-match lookup below.
	rawURL = normRelayURL(rawURL)
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var role string
	if err := tx.QueryRowContext(ctx,
		`SELECT role FROM own_relays WHERE relay_url=?`, rawURL).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errOwnRelayNotFound
		}
		return fmt.Errorf("read role: %w", err)
	}
	if role == "home" {
		var homeCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM own_relays WHERE role='home'`).Scan(&homeCount); err != nil {
			return fmt.Errorf("count home relays: %w", err)
		}
		if homeCount <= 1 {
			return errOwnRelayHomeRequired
		}
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM own_relays WHERE relay_url=?`, rawURL)
	if err != nil {
		return fmt.Errorf("delete own_relay: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errOwnRelayNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	d.Refresh()
	d.emitDashEvent(dashboard.Event{Kind: "relay.removed"})
	return nil
}

// normRelayURL returns the canonical form of a relay URL: lowercased
// scheme/host, root-path trailing slash trimmed. Callers should use it
// before any database write or in-memory dedup-keyed-by-URL — without
// it, `wss://x` and `wss://x/` register as distinct relay endpoints and
// the daemon opens two redundant WebSocket connections (one of which
// often 503s, depending on the relay's HTTP handler).
//
// Only root-path slashes are stripped — `wss://host/nostr/` keeps its
// trailing slash because path-sensitive relays treat `/nostr/` and
// `/nostr` as different WebSocket routes. `go-nostr.NormalizeURL` is
// not delegated to wholesale for this reason; instead we url.Parse,
// lowercase scheme/host, and trim only the empty-path slash.
//
// Returns the input unchanged when it doesn't parse as a relay URL, so
// validation errors surface from isRelayURL (the existing gate) rather
// than turning into empty-string ghosts in the relay registry.
func normRelayURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "ws" && scheme != "wss" {
		return s
	}
	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	// Only strip the root-path slash. `wss://host/` → `wss://host`;
	// `wss://host/nostr/` stays `wss://host/nostr/`.
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

// isRelayURL reports whether s is a ws:// or wss:// URL with a host.
// Tolerates trailing slashes and case-insensitive schemes; rejects
// anything else (no http/https, no bare hostnames, no empty strings).
func isRelayURL(s string) bool {
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "ws" && scheme != "wss" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}
