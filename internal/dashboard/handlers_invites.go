package dashboard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/invitedb"
)

// ── phase 3: invites ──────────────────────────────────────────────────
//
// Routes:
//   GET   /settings/invites                  pane: list (active/expired/revoked)
//   POST  /settings/invites                  create (form: redeemer-label, expires, max-uses)
//   POST  /settings/invites/redeem           redeem (form: invite uri)
//   GET   /settings/invites/<id-prefix>/confirm-revoke  load typed-confirm modal
//   POST  /settings/invites/<id-prefix>/revoke          revoke (with typed confirm)

const (
	// inviteIDConfirmLen is the prefix length the operator types to
	// confirm a revoke. Mirrors the CLI's display length and the
	// invite.list "ID" column truncation, so the operator types what
	// they see, not what's in the URL path. The full ID stays in the
	// URL (so the server can revoke deterministically); only the
	// human-confirm phrase is truncated.
	inviteIDConfirmLen = 8

	// inviteRedeemerLabelMaxLen is the same upper bound used for own
	// and contact labels. Kept consistent so labels round-trip across
	// the create → redeem → contact path without truncation surprises.
	inviteRedeemerLabelMaxLen = 64
)

// settingsInvitesHandler handles GET /settings/invites (list pane) and
// POST /settings/invites (create new invite).
func settingsInvitesHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		switch req.Method {
		case http.MethodGet:
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "invites")
				return
			}
			renderInvitesPane(w, r, logger, ctx, deps, buildSettingsInvites(ctx, deps))
		case http.MethodPost:
			handleInviteCreate(w, req, r, logger, deps)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// settingsInvitesRedeemHandler handles POST /settings/invites/redeem.
// GET is intentionally not supported — redemption mutates state, so the
// operator must POST a token from the redeem form (or the CLI).
func settingsInvitesRedeemHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleInviteRedeem(w, req, r, logger, deps)
	}
}

// settingsInvitesByIDHandler routes the per-invite sub-paths:
//
//	GET   /settings/invites/<id>/confirm-revoke   → load typed-confirm modal
//	POST  /settings/invites/<id>/revoke           → revoke (with confirm)
func settingsInvitesByIDHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/settings/invites/")
		// "redeem" is handled by its own handler, not this one. A bare
		// "/settings/invites/" with no id is a 404.
		if path == "" || path == "redeem" {
			http.NotFound(w, req)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) < 2 {
			http.NotFound(w, req)
			return
		}
		idPrefix := parts[0]
		ctx := req.Context()

		switch parts[1] {
		case "confirm-revoke":
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			renderInviteRevokeModal(w, r, logger, ctx, deps, idPrefix)
		case "revoke":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleInviteRevoke(w, req, r, logger, deps, idPrefix)
		default:
			http.NotFound(w, req)
		}
	}
}

// handleInviteCreate POSTs a new invite. The form fields:
//
//	redeemer_label   optional label for the redeemer (≤64 runes)
//	expires          duration string (e.g. "7d", "24h", "30m") or "never"
//	max_uses         integer ≥1, or "unlimited"
//
// Validation that mirrors the CLI's `eidos gate invite create` flags.
func handleInviteCreate(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	redeemerLabel := strings.TrimSpace(req.Form.Get("redeemer_label"))
	expiresRaw := strings.TrimSpace(req.Form.Get("expires"))
	maxUsesRaw := strings.TrimSpace(req.Form.Get("max_uses"))

	view := buildSettingsInvites(ctx, deps)

	if len([]rune(redeemerLabel)) > inviteRedeemerLabelMaxLen {
		view.CreateError = fmt.Sprintf("Redeemer label too long (%d runes; max %d).",
			len([]rune(redeemerLabel)), inviteRedeemerLabelMaxLen)
		renderInvitesPane(w, r, logger, ctx, deps, view)
		return
	}

	opts := InviteCreateOpts{RedeemerLabel: redeemerLabel}

	switch strings.ToLower(maxUsesRaw) {
	case "", "1":
		opts.SingleUse = true
	case "unlimited", "0":
		opts.Unlimited = true
	default:
		n, err := strconv.Atoi(maxUsesRaw)
		if err != nil || n < 1 {
			view.CreateError = "Max uses must be a positive integer, '1', or 'unlimited'."
			renderInvitesPane(w, r, logger, ctx, deps, view)
			return
		}
		opts.MaxUses = n
	}

	switch strings.ToLower(expiresRaw) {
	case "":
		opts.ExpiresSeconds = 0 // daemon picks 7d
	case "never":
		opts.ExpiresSeconds = -1
	default:
		d, err := parseInviteDuration(expiresRaw)
		if err != nil {
			view.CreateError = fmt.Sprintf("Invalid expires value %q: %v", expiresRaw, err)
			renderInvitesPane(w, r, logger, ctx, deps, view)
			return
		}
		opts.ExpiresSeconds = int64(d.Seconds())
	}

	inv, uri, err := deps.CreateInvite(ctx, opts)
	if err != nil {
		view.CreateError = "Create failed: " + err.Error()
		logger.Warn("dashboard invite-create failed", "err", err)
		renderInvitesPane(w, r, logger, ctx, deps, view)
		return
	}

	// Refresh from a fresh list so the new row shows in Active.
	view = buildSettingsInvites(ctx, deps)
	view.CreatedInvite = &createdInvite{
		IDShort:    inviteIDShort(inv.ID),
		URI:        uri,
		ExpiresAt:  inv.ExpiresAt,
		MaxUses:    inv.MaxUses,
		SingleUse:  opts.SingleUse,
		Unlimited:  opts.Unlimited,
		Redeemer:   redeemerLabel,
		IssuerHint: inv.IssuerLabel,
	}
	renderInvitesPane(w, r, logger, ctx, deps, view)
}

// handleInviteRedeem POSTs a single invite URI (or bare token).
func handleInviteRedeem(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	token := strings.TrimSpace(req.Form.Get("token"))
	view := buildSettingsInvites(ctx, deps)
	if token == "" {
		view.RedeemError = "Invite URI is required."
		renderInvitesPane(w, r, logger, ctx, deps, view)
		return
	}
	res, err := deps.RedeemInvite(ctx, token)
	if err != nil {
		view.RedeemError = "Redeem failed: " + err.Error()
		logger.Warn("dashboard invite-redeem failed", "err", err)
		renderInvitesPane(w, r, logger, ctx, deps, view)
		return
	}
	view = buildSettingsInvites(ctx, deps)
	view.RedeemResult = &redeemFlash{
		IssuerNpub:  res.IssuerNpub,
		IssuerRelay: res.IssuerRelay,
		AcceptedBy:  res.AcceptedBy,
	}
	renderInvitesPane(w, r, logger, ctx, deps, view)
}

func handleInviteRevoke(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, idPrefix string) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	// The typed-confirm phrase is the 8-char id prefix as the operator
	// sees it in the list. The handler validates that the typed phrase
	// matches before calling RevokeInvite. The id_prefix in the URL
	// path is used to look up the actual invite; the confirm field is
	// re-checked against the same prefix.
	confirm := inviteIDShort(idPrefix)
	if err := requireConfirm(req, confirm); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := deps.RevokeInvite(ctx, idPrefix); err != nil {
		// Map the well-known sentinel errors to specific messages so
		// the operator can act on them. Anything else surfaces as a
		// generic 502 with the wrapped daemon error.
		switch {
		case errors.Is(err, invitedb.ErrNotFound):
			http.Error(w, "invite not found", http.StatusNotFound)
		case errors.Is(err, invitedb.ErrPrefixAmbiguous):
			http.Error(w, "id prefix matches multiple invites — use a longer prefix", http.StatusBadRequest)
		default:
			logger.Warn("dashboard invite-revoke failed", "err", err)
			http.Error(w, "revoke failed: "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	// Re-render the whole invites pane (swapped into #settings-pane)
	// AND clear the modal slot via OOB swap, mirroring the contacts
	// remove handler.
	view := buildSettingsInvites(ctx, deps)
	out, rerr := r.Render("settings_invites", view)
	if rerr != nil {
		logger.Error("render settings_invites", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
	_, _ = w.Write([]byte(`<div id="modal" hx-swap-oob="innerHTML"></div>`))
}

// renderInvitesPane writes settings_invites with the supplied view —
// shared between the list-only path and the post-action paths so flash
// messages appear without a reload.
func renderInvitesPane(w http.ResponseWriter, r *renderer, logger *slog.Logger, _ context.Context, _ DashboardDeps, view settingsInvitesData) {
	out, err := r.Render("settings_invites", view)
	if err != nil {
		logger.Error("render settings_invites", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func renderInviteRevokeModal(w http.ResponseWriter, r *renderer, logger *slog.Logger, ctx context.Context, deps DashboardDeps, idPrefix string) {
	// Look up the invite so the modal can show identifying detail (the
	// issuer/redeemer labels) — without that, "Revoke abc12345" is a
	// fairly opaque ask. ErrNotFound and ErrPrefixAmbiguous both
	// surface as 404 here; the operator clicks Revoke from a row, so
	// either error means the row was stale and they should reload.
	invs, err := deps.ListInvites(ctx, "")
	if err != nil {
		http.Error(w, "list invites: "+err.Error(), http.StatusBadGateway)
		return
	}
	var match *invitedb.Invite
	for _, inv := range invs {
		if strings.HasPrefix(inv.ID, idPrefix) {
			if match != nil {
				http.Error(w, "id prefix matches multiple invites", http.StatusBadRequest)
				return
			}
			match = inv
		}
	}
	if match == nil {
		http.NotFound(w, &http.Request{})
		return
	}
	short := inviteIDShort(match.ID)
	body := fmt.Sprintf("Revoke invitation %s. ", short)
	if match.RedeemerLabel != "" {
		body += fmt.Sprintf("Issued for redeemer %q. ", match.RedeemerLabel)
	}
	if match.Uses > 0 {
		body += fmt.Sprintf("Already redeemed %d time(s); existing redeemers stay in your contacts.", match.Uses)
	} else {
		body += "Not yet redeemed."
	}
	out, rerr := r.Render("confirm_modal", confirmModalData{
		Action:         "/settings/invites/" + match.ID + "/revoke",
		Target:         "#settings-pane",
		Swap:           "innerHTML",
		Title:          "Revoke invite " + short,
		Body:           body,
		Warning:        "This is irreversible. Future redemption attempts with this URI will fail; redemptions already in flight may still complete (race window is on the order of a relay round-trip).",
		ExpectedPhrase: short,
		ConfirmLabel:   "Revoke " + short,
	})
	if rerr != nil {
		logger.Error("render confirm_modal", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// ── settings/invites view-builders ─────────────────────────────────

// buildSettingsInvites pulls the full invite list and buckets it by
// status. The bucketing happens here (not in SQL or the template) so a
// single ListInvites call covers all three sections — three separate
// status-filtered queries would deliver an inconsistent snapshot if a
// concurrent expiry sweep or revoke landed between them.
func buildSettingsInvites(ctx context.Context, deps DashboardDeps) settingsInvitesData {
	out := settingsInvitesData{}
	invs, err := deps.ListInvites(ctx, "")
	if err != nil {
		out.Error = "Could not list invites: " + err.Error()
		return out
	}
	for _, inv := range invs {
		row := inviteRow{
			ID:            inv.ID,
			IDShort:       inviteIDShort(inv.ID),
			CreatedAt:     inv.CreatedAt,
			ExpiresAt:     inv.ExpiresAt,
			MaxUses:       inv.MaxUses,
			Uses:          inv.Uses,
			IssuerLabel:   inv.IssuerLabel,
			RedeemerLabel: inv.RedeemerLabel,
			Status:        string(inv.Status),
		}
		switch inv.Status {
		case invitedb.StatusActive:
			out.Active = append(out.Active, row)
		case invitedb.StatusExpired:
			out.Expired = append(out.Expired, row)
		case invitedb.StatusRevoked:
			out.Revoked = append(out.Revoked, row)
		default:
			// Unknown status — surface in Active so the operator sees
			// it (they'll spot the unrecognised pill and report it).
			out.Active = append(out.Active, row)
		}
	}
	return out
}

// inviteIDShort returns the prefix the operator sees and types. Same
// length as the CLI's `id=...` stderr line and the list-table column.
func inviteIDShort(id string) string {
	if len(id) <= inviteIDConfirmLen {
		return id
	}
	return id[:inviteIDConfirmLen]
}

// parseInviteDuration accepts the same tokens as `eidos gate invite
// create --expires` (Go's time.ParseDuration plus an "Nd" extension
// for days). The CLI lives in cmd/eidos/gate/invite.go; we duplicate
// the helper here because cmd/* cannot be imported from internal/*.
// Keep the two implementations synced if either changes.
func parseInviteDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		if n <= 0 {
			return 0, fmt.Errorf("days must be positive")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return d, nil
}
