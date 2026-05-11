package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

// ── phase 2: contacts ──────────────────────────────────────────────

const contactLabelMaxLen = 64

// settingsContactsHandler handles GET /settings/contacts (list pane)
// and POST /settings/contacts (add a new contact via card URI).
func settingsContactsHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		switch req.Method {
		case http.MethodGet:
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "contacts")
				return
			}
			out, err := r.Render("settings_contacts", buildSettingsContacts(ctx, deps, ""))
			if err != nil {
				logger.Error("render settings_contacts", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))
		case http.MethodPost:
			if err := req.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			cardURI := strings.TrimSpace(req.Form.Get("card"))
			labelOverride := strings.TrimSpace(req.Form.Get("label"))
			if cardURI == "" {
				renderContactsErr(w, r, logger, ctx, deps, "Card URI is required.")
				return
			}
			if _, err := deps.AddContact(ctx, cardURI, labelOverride); err != nil {
				renderContactsErr(w, r, logger, ctx, deps, "Add failed: "+err.Error())
				return
			}
			// Re-render the whole pane so the new row appears in the
			// list and the input clears.
			out, err := r.Render("settings_contacts", buildSettingsContacts(ctx, deps, ""))
			if err != nil {
				logger.Error("render settings_contacts", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderContactsErr(w http.ResponseWriter, r *renderer, logger *slog.Logger, ctx context.Context, deps DashboardDeps, msg string) {
	view := buildSettingsContacts(ctx, deps, msg)
	out, err := r.Render("settings_contacts", view)
	if err != nil {
		logger.Error("render settings_contacts", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func settingsContactsScanHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		cardURI := strings.TrimSpace(req.Form.Get("card"))
		// Carry the operator's optional label override through the
		// preview → confirm round trip; the confirm form re-POSTs both
		// the card URI and this label, so a typed override survives
		// (instead of silently reverting to the card's embedded label).
		labelOverride := strings.TrimSpace(req.Form.Get("label"))
		view := contactScanData{CardURI: cardURI, LabelOverride: labelOverride}
		if cardURI == "" {
			view.Error = "Card URI is required."
		} else if preview, err := deps.ScanCard(req.Context(), cardURI); err != nil {
			view.Error = err.Error()
		} else {
			view.Pubkey = preview.Pubkey
			view.Npub = preview.Npub
			view.Label = preview.Label
			view.Relay = preview.Relay
			view.AlreadyContact = preview.AlreadyContact
		}
		out, err := r.Render("contact_scan", view)
		if err != nil {
			logger.Error("render contact_scan", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// settingsContactsByPubkeyHandler routes the per-contact sub-paths:
//
//	GET   /settings/contacts/<pk>                 → detail pane
//	POST  /settings/contacts/<pk>/label           → rename
//	POST  /settings/contacts/<pk>/tier            → change tier
//	GET   /settings/contacts/<pk>/confirm-remove  → load typed-confirm modal
//	POST  /settings/contacts/<pk>/remove          → remove (with confirm)
func settingsContactsByPubkeyHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/settings/contacts/")
		if path == "" || path == "scan" {
			http.NotFound(w, req)
			return
		}
		parts := strings.Split(path, "/")
		pubkey := parts[0]
		ctx := req.Context()

		// Sub-action dispatch.
		if len(parts) == 1 {
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			// Direct address-bar navigation lacks HX-Request; render
			// the whole shell so the operator doesn't get a fragment
			// hanging in space. The shell's contacts pane shows the
			// list with the row in question opened, but htmx will
			// not auto-open the detail view; that's acceptable for
			// a refresh — the operator clicks the row again.
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "contacts")
				return
			}
			renderContactDetail(w, r, logger, ctx, deps, pubkey)
			return
		}

		switch parts[1] {
		case "label":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleContactSetLabel(w, req, r, logger, deps, pubkey)
		case "tier":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleContactSetTier(w, req, r, logger, deps, pubkey)
		case "confirm-remove":
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			renderContactRemoveModal(w, r, logger, ctx, deps, pubkey)
		case "remove":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleContactRemove(w, req, r, logger, deps, pubkey)
		default:
			http.NotFound(w, req)
		}
	}
}

func renderContactDetail(w http.ResponseWriter, r *renderer, logger *slog.Logger, ctx context.Context, deps DashboardDeps, pubkey string) {
	v, err := buildContactDetail(ctx, deps, pubkey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	out, rerr := r.Render("contact_detail", v)
	if rerr != nil {
		logger.Error("render contact_detail", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func renderContactRemoveModal(w http.ResponseWriter, r *renderer, logger *slog.Logger, ctx context.Context, deps DashboardDeps, pubkey string) {
	c, err := deps.GetContact(ctx, pubkey)
	if err != nil || c == nil {
		http.NotFound(w, &http.Request{})
		return
	}
	out, rerr := r.Render("confirm_modal", confirmModalData{
		Action:         "/settings/contacts/" + pubkey + "/remove",
		Target:         "#settings-pane",
		Swap:           "innerHTML",
		Title:          "Remove contact",
		Body:           "Removing " + c.Label + " also drops the per-contact relay hints. Existing inbox messages are not deleted; only the address-book entry is.",
		Warning:        "This is irreversible. The contact's npub will need to be re-added (and re-trusted) to send to them again.",
		ExpectedPhrase: c.Label,
		ConfirmLabel:   "Remove " + c.Label,
	})
	if rerr != nil {
		logger.Error("render confirm_modal", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func handleContactSetLabel(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, pubkey string) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	raw := req.Form.Get("label")
	label := strings.TrimSpace(raw)
	v, err := buildContactDetail(ctx, deps, pubkey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	switch {
	case label == "":
		v.LabelError = "Label cannot be empty."
	case len([]rune(label)) > contactLabelMaxLen:
		v.LabelError = fmt.Sprintf("Label too long (%d runes; max %d).", len([]rune(label)), contactLabelMaxLen)
	default:
		if err := deps.SetContactLabel(ctx, pubkey, label); err != nil {
			v.LabelError = "Save failed: " + err.Error()
		} else {
			// Persisted: rebuild from snapshot so all fields are fresh.
			v, _ = buildContactDetail(ctx, deps, pubkey)
			v.LabelSaved = true
		}
	}
	if v.LabelError != "" {
		v.FormLabel = raw
	}
	out, rerr := r.Render("contact_detail", v)
	if rerr != nil {
		logger.Error("render contact_detail", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func handleContactSetTier(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, pubkey string) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	tier := contacts.Tier(strings.TrimSpace(req.Form.Get("tier")))
	v, err := buildContactDetail(ctx, deps, pubkey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := deps.SetContactTier(ctx, pubkey, tier); err != nil {
		v.TierError = "Save failed: " + err.Error()
	} else {
		v, _ = buildContactDetail(ctx, deps, pubkey)
		v.TierSaved = true
	}
	out, rerr := r.Render("contact_detail", v)
	if rerr != nil {
		logger.Error("render contact_detail", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func handleContactRemove(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, pubkey string) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	c, err := deps.GetContact(ctx, pubkey)
	if err != nil || c == nil {
		http.NotFound(w, req)
		return
	}
	if err := requireConfirm(req, c.Label); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := deps.RemoveContact(ctx, pubkey); err != nil {
		logger.Warn("dashboard remove-contact failed", "err", err)
		http.Error(w, "remove failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Return the refreshed contacts list pane (swapped into the
	// modal's hx-target=#settings-pane) AND an OOB swap that empties
	// the #modal slot — without the OOB clear, the modal markup
	// stays in the DOM hovering over the freshly-rendered list.
	out, rerr := r.Render("settings_contacts", buildSettingsContacts(ctx, deps, ""))
	if rerr != nil {
		logger.Error("render settings_contacts", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
	_, _ = w.Write([]byte(`<div id="modal" hx-swap-oob="innerHTML"></div>`))
}

// ── settings/contacts view-builders ────────────────────────────────

func buildSettingsContacts(ctx context.Context, deps DashboardDeps, errMsg string) settingsContactsData {
	out := settingsContactsData{Error: errMsg}
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		out.Error = "Could not list contacts: " + err.Error()
		return out
	}
	lastSeen := lastSeenByContact(deps)
	for _, c := range cs {
		var ls *time.Time
		if t, ok := lastSeen[c.Pubkey]; ok {
			ls = &t
		}
		out.Rows = append(out.Rows, contactRow{
			Pubkey:      c.Pubkey,
			PubkeyShort: shortHexID(c.Pubkey),
			Npub:        pubkeyToNpub(c.Pubkey),
			Label:       c.Label,
			Tier:        c.Tier,
			LastSeen:    ls,
		})
	}
	return out
}

func buildContactDetail(ctx context.Context, deps DashboardDeps, pubkey string) (contactDetailData, error) {
	c, err := deps.GetContact(ctx, pubkey)
	if err != nil {
		return contactDetailData{}, err
	}
	if c == nil {
		return contactDetailData{}, fmt.Errorf("contact not found")
	}
	return contactDetailData{
		Pubkey:      c.Pubkey,
		PubkeyShort: shortHexID(c.Pubkey),
		Npub:        pubkeyToNpub(c.Pubkey),
		Label:       c.Label,
		FormLabel:   c.Label,
		Tier:        c.Tier,
		Relays:      c.Relays,
	}, nil
}

// shortHexID returns the first 12 hex chars of a pubkey, used for
// stable but compact DOM ids on contact rows.
func shortHexID(pubkey string) string {
	if len(pubkey) <= 12 {
		return pubkey
	}
	return pubkey[:12]
}

// requireConfirm enforces the typed-confirm contract used by destructive
// actions in later phases. The shared confirm modal asks the operator to
// type a specific phrase; the server validates the same phrase here as
// defense-in-depth against fat-finger or scripted misuse. Called BEFORE
// the handler touches state.
func requireConfirm(req *http.Request, expected string) error {
	got := strings.TrimSpace(req.FormValue("confirm"))
	if got == "" {
		return fmt.Errorf("confirmation phrase required")
	}
	if got != expected {
		return fmt.Errorf("confirmation phrase mismatch")
	}
	return nil
}
