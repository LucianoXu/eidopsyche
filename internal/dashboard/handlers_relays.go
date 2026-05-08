package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ── phase 4: own relays ───────────────────────────────────────────────
//
// Routes:
//   GET   /settings/relays                       pane (own relays + health)
//   POST  /settings/relays                       add (form: url, role)
//   GET   /settings/relays/<slug>/confirm-remove load typed-confirm modal (home only)
//   POST  /settings/relays/<slug>/remove         remove (typed-confirm if home)
//
// Slugs (hex of sha256(URL)) are used in URL paths so the URL itself
// (which contains // : . and possibly + or query) doesn't have to be
// percent-encoded in hx-* attributes. The slug → URL lookup happens
// server-side against the live own_relays list, so a stale slug from a
// prior listing returns 404.

// settingsRelaysHandler handles GET /settings/relays and POST /settings/relays.
func settingsRelaysHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		switch req.Method {
		case http.MethodGet:
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "relays")
				return
			}
			renderRelaysPane(w, r, logger, buildSettingsRelays(ctx, deps, ""))
		case http.MethodPost:
			handleRelayAdd(w, req, r, logger, deps)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// settingsRelaysBySlugHandler routes the per-relay sub-paths:
//
//	GET   /settings/relays/<slug>/confirm-remove  → load typed-confirm modal
//	POST  /settings/relays/<slug>/remove          → remove
func settingsRelaysBySlugHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/settings/relays/")
		if path == "" {
			http.NotFound(w, req)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) < 2 {
			http.NotFound(w, req)
			return
		}
		slug := parts[0]
		ctx := req.Context()

		switch parts[1] {
		case "confirm-remove":
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			renderRelayRemoveModal(w, r, logger, ctx, deps, slug)
		case "remove":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleRelayRemove(w, req, r, logger, deps, slug)
		default:
			http.NotFound(w, req)
		}
	}
}

func handleRelayAdd(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	rawURL := strings.TrimSpace(req.Form.Get("url"))
	role := strings.TrimSpace(req.Form.Get("role"))
	if role == "" {
		role = "fallback"
	}

	if err := deps.AddOwnRelay(ctx, rawURL, role); err != nil {
		// All AddOwnRelay errors are validation; surface them inline
		// rather than 4xx-ing — the operator should see what went wrong
		// without the page collapsing.
		view := buildSettingsRelays(ctx, deps, "Add failed: "+err.Error())
		renderRelaysPane(w, r, logger, view)
		return
	}
	renderRelaysPane(w, r, logger, buildSettingsRelays(ctx, deps, ""))
}

func handleRelayRemove(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, slug string) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	row, err := lookupRelayBySlug(ctx, deps, slug)
	if err != nil {
		http.Error(w, "relay not found", http.StatusNotFound)
		return
	}
	// Home relays require the operator to type the URL as confirm.
	// Fallback relays don't need a phrase — the action is reversible
	// (re-adding the URL yields the same row). This split mirrors the
	// design spec's "typed-confirm if role=home" rule.
	if row.Role == "home" {
		if err := requireConfirm(req, row.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := deps.RemoveOwnRelay(ctx, row.URL); err != nil {
		// Map the daemon's defensive refusals to specific status codes
		// so a stale Remove click doesn't turn into a 500.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not in own_relays"):
			http.Error(w, msg, http.StatusNotFound)
		case strings.Contains(msg, "at least one home relay"):
			http.Error(w, msg, http.StatusBadRequest)
		default:
			logger.Warn("dashboard relay-remove failed", "err", err)
			http.Error(w, "remove failed: "+msg, http.StatusBadGateway)
		}
		return
	}
	out, rerr := r.Render("settings_relays", buildSettingsRelays(ctx, deps, ""))
	if rerr != nil {
		logger.Error("render settings_relays", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
	// OOB-clear the modal slot for the home-relay path; harmless for the
	// fallback path (the modal was never opened, the slot is already empty).
	_, _ = w.Write([]byte(`<div id="modal" hx-swap-oob="innerHTML"></div>`))
}

func renderRelayRemoveModal(w http.ResponseWriter, r *renderer, logger *slog.Logger, ctx context.Context, deps DashboardDeps, slug string) {
	row, err := lookupRelayBySlug(ctx, deps, slug)
	if err != nil {
		http.Error(w, "relay not found", http.StatusNotFound)
		return
	}
	// Only home-relay removals route through this modal; the row's
	// Remove button on a fallback row POSTs straight to /remove. If
	// somehow a non-home slug arrives here, fall through to the same
	// modal — the operator typing the URL is still meaningful UX.
	body := fmt.Sprintf("Remove %s relay %s.", row.Role, row.URL)
	if row.Role == "home" {
		body += " The daemon needs at least one home target to publish; if this is your only home, the action is refused."
	}
	out, rerr := r.Render("confirm_modal", confirmModalData{
		Action:         "/settings/relays/" + slug + "/remove",
		Target:         "#settings-pane",
		Swap:           "innerHTML",
		Title:          "Remove home relay",
		Body:           body,
		Warning:        "Removing a home relay drops it from the publish/subscribe set immediately. Peers who only know this URL won't reach you until you share a fresh card.",
		ExpectedPhrase: row.URL,
		ConfirmLabel:   "Remove " + row.URL,
	})
	if rerr != nil {
		logger.Error("render confirm_modal", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func renderRelaysPane(w http.ResponseWriter, r *renderer, logger *slog.Logger, view settingsRelaysData) {
	out, err := r.Render("settings_relays", view)
	if err != nil {
		logger.Error("render settings_relays", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// ── view builders ──────────────────────────────────────────────────

// buildSettingsRelays merges own_relays rows with the live relay-health
// snapshot to render one row per own_relays entry with its current
// connection state. Health entries that don't correspond to an own_relays
// row (e.g. a contact relay) are NOT rendered here — those belong to the
// existing right-rail panel.
func buildSettingsRelays(ctx context.Context, deps DashboardDeps, addErr string) settingsRelaysData {
	out := settingsRelaysData{AddError: addErr}
	relays, err := deps.ListOwnRelays(ctx)
	if err != nil {
		out.Error = "Could not list own relays: " + err.Error()
		return out
	}
	healthByURL := map[string]RelayState{}
	for _, h := range deps.ListRelayHealth() {
		healthByURL[h.URL] = h
	}
	homeCount := 0
	for _, rl := range relays {
		if rl.Role == "home" {
			homeCount++
		}
	}
	out.HomeCount = homeCount
	now := time.Now().Unix()
	for _, rl := range relays {
		row := ownRelayRow{
			URL:        rl.URL,
			Slug:       relaySlug(rl.URL),
			Role:       rl.Role,
			IsLastHome: rl.Role == "home" && homeCount == 1,
		}
		if rl.AddedAt > 0 {
			row.AddedAt = time.Unix(rl.AddedAt, 0)
		}
		if h, ok := healthByURL[rl.URL]; ok {
			row.State = h.State
			row.LastError = h.LastError
			if h.LastEventAt > 0 {
				row.LastEventAgo = humanSince(time.Duration(now-h.LastEventAt) * time.Second)
			} else {
				row.LastEventAgo = "—"
			}
		} else {
			row.State = "(unknown)"
			row.LastEventAgo = "—"
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// lookupRelayBySlug resolves the URL+role for a given slug by re-listing
// own_relays and recomputing the slug. Stale slugs (URL no longer in the
// table) return errOwnRelaySlugNotFound.
func lookupRelayBySlug(ctx context.Context, deps DashboardDeps, slug string) (OwnRelay, error) {
	relays, err := deps.ListOwnRelays(ctx)
	if err != nil {
		return OwnRelay{}, fmt.Errorf("list own_relays: %w", err)
	}
	for _, rl := range relays {
		if relaySlug(rl.URL) == slug {
			return rl, nil
		}
	}
	return OwnRelay{}, errOwnRelaySlugNotFound
}

var errOwnRelaySlugNotFound = errors.New("own_relays: slug not found")

// relaySlug returns the first 16 hex chars of sha256(url). Used as a
// path segment for /settings/relays/<slug>/{confirm-remove,remove} so
// the URL itself doesn't need percent-encoding through the hx-get/post
// chain. Collisions inside 16 hex chars are not a security concern —
// the slug is resolved against the live own_relays list, so the worst
// case is a stale-row 404.
func relaySlug(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(h[:8])
}
