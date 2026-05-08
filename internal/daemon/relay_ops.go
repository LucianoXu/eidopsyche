package daemon

import (
	"context"
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
func (d *Daemon) RemoveOwnRelay(ctx context.Context, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errOwnRelayInvalidURL
	}
	// Look up the row to learn its role; we need to know whether
	// removing it would leave us with no home.
	var role string
	if err := d.DB.QueryRowContext(ctx,
		`SELECT role FROM own_relays WHERE relay_url=?`, rawURL).Scan(&role); err != nil {
		return errOwnRelayNotFound
	}
	if role == "home" {
		var homeCount int
		if err := d.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM own_relays WHERE role='home'`).Scan(&homeCount); err != nil {
			return fmt.Errorf("count home relays: %w", err)
		}
		if homeCount <= 1 {
			return errOwnRelayHomeRequired
		}
	}
	res, err := d.DB.ExecContext(ctx,
		`DELETE FROM own_relays WHERE relay_url=?`, rawURL)
	if err != nil {
		return fmt.Errorf("delete own_relay: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errOwnRelayNotFound
	}
	d.Refresh()
	d.emitDashEvent(dashboard.Event{Kind: "relay.removed"})
	return nil
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
