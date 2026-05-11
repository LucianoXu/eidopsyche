package dashboard

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

const messagesPageSize = 50

func registerHandlersWithRenderer(mux *http.ServeMux, deps DashboardDeps, r *renderer, logger *slog.Logger) {
	mux.HandleFunc("/", shellHandler(deps, r, logger))
	mux.HandleFunc("/sidebar/contacts", sidebarHandler(deps, r, logger))
	mux.HandleFunc("/messages", messagesHandler(deps, r, logger))
	mux.HandleFunc("/thread/", threadOrSendHandler(deps, r, logger))
	mux.HandleFunc("/compose", composeHandler(deps, r, logger))
	mux.HandleFunc("/compose/send", composeSendHandler(deps, r, logger))
	mux.HandleFunc("/relays", relaysHandler(deps, r, logger))
	mux.HandleFunc("/topbar", topbarHandler(deps, r, logger))
	mux.HandleFunc("/settings", settingsShellHandler(deps, r, logger))
	mux.HandleFunc("/settings/identity", settingsIdentityHandler(deps, r, logger))
	mux.HandleFunc("/settings/identity/label", settingsLabelPostHandler(deps, r, logger))
	mux.HandleFunc("/settings/config", settingsConfigHandler(deps, r, logger))
	mux.HandleFunc("/settings/contacts", settingsContactsHandler(deps, r, logger))
	mux.HandleFunc("/settings/contacts/scan", settingsContactsScanHandler(deps, r, logger))
	mux.HandleFunc("/settings/contacts/", settingsContactsByPubkeyHandler(deps, r, logger))
	mux.HandleFunc("/settings/invites", settingsInvitesHandler(deps, r, logger))
	mux.HandleFunc("/settings/invites/redeem", settingsInvitesRedeemHandler(deps, r, logger))
	mux.HandleFunc("/settings/invites/", settingsInvitesByIDHandler(deps, r, logger))
	mux.HandleFunc("/settings/relays", settingsRelaysHandler(deps, r, logger))
	mux.HandleFunc("/settings/relays/", settingsRelaysBySlugHandler(deps, r, logger))
	mux.HandleFunc("/settings/service", settingsServiceHandler(deps, r, logger))
	mux.HandleFunc("/settings/service/", settingsServiceActionHandler(deps, r, logger))
	hub := newSSEHub(deps)
	mux.HandleFunc("/events", hub.handler(r, logger))
	sub, err := staticSubFS()
	if err == nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	} else {
		logger.Error("static fs init failed", "err", err)
	}
}

// shellHandler renders the full page with a default main pane (All messages).
func shellHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		ctx := req.Context()
		label, _ := deps.OwnLabel(ctx)
		side := buildSidebar(ctx, deps, "", false)
		main := buildMessagesView(deps, false, "")

		mainHTML, err := r.Render("messages", main)
		if err != nil {
			logger.Error("render messages", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		out, err := r.Render("shell", shellData{
			OwnLabel: label,
			OwnNpub:  deps.OwnPubkey(),
			Sidebar:  side,
			Main:     template.HTML(mainHTML), //nolint:gosec // trusted internal template output
		})
		if err != nil {
			logger.Error("render shell", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func sidebarHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		// Settings-active state survives across SSE-triggered sidebar
		// refreshes via htmx's HX-Current-URL header — htmx fires this
		// hx-get from inside whichever main page the operator is on
		// and the header carries that page's URL. Falls back to a
		// Referer-prefix check (parsed, not substring) for non-htmx
		// reloads.
		activeSettings := isSettingsURL(req.Header.Get("HX-Current-URL")) ||
			isSettingsURL(req.Header.Get("Referer"))
		side := buildSidebar(ctx, deps, "", activeSettings)
		out, err := r.Render("sidebar", side)
		if err != nil {
			logger.Error("render sidebar", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// topbarHandler returns the topbar's inner content (label + npub) so
// the SSE-triggered hx-get on the topbar can refresh it without
// reloading the whole shell. Wired in templates/shell.html.
func topbarHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		label, _ := deps.OwnLabel(ctx)
		out, err := r.Render("topbar_inner", topbarInnerData{
			OwnLabel: label,
			OwnNpub:  deps.OwnPubkey(),
		})
		if err != nil {
			logger.Error("render topbar_inner", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

type topbarInnerData struct {
	OwnLabel string
	OwnNpub  string
}

// --- shared view builders / helpers ---

func buildSidebar(ctx context.Context, deps DashboardDeps, activePubkey string, activeSettings bool) sidebarData {
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		return sidebarData{ActiveSettings: activeSettings}
	}
	out := make([]sidebarContact, 0, len(cs))
	lastSeen := lastSeenByContact(deps)
	for _, c := range cs {
		if c.Tier == contacts.TierBlocked {
			continue
		}
		var ls *time.Time
		if t, ok := lastSeen[c.Pubkey]; ok {
			ls = &t
		}
		out = append(out, sidebarContact{
			Pubkey:   c.Pubkey,
			Label:    c.Label,
			Tier:     c.Tier,
			LastSeen: ls,
			Active:   c.Pubkey == activePubkey,
		})
	}
	return sidebarData{Contacts: sortContactsForSidebar(out), ActiveSettings: activeSettings}
}

func lastSeenByContact(deps DashboardDeps) map[string]time.Time {
	out := map[string]time.Time{}
	if msgs, err := deps.ListInbox(nil, "", 200); err == nil {
		for _, m := range msgs {
			t := time.Unix(m.ReceivedAt, 0)
			if cur, ok := out[m.From]; !ok || t.After(cur) {
				out[m.From] = t
			}
		}
	}
	if sents, err := deps.ListOutbox(nil, "", 200); err == nil {
		for _, s := range sents {
			t := time.Unix(s.SentAt, 0)
			if cur, ok := out[s.To]; !ok || t.After(cur) {
				out[s.To] = t
			}
		}
	}
	return out
}

func lookupContact(ctx context.Context, deps DashboardDeps, pk string) *contacts.Contact {
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		return nil
	}
	for _, c := range cs {
		if c.Pubkey == pk {
			return c
		}
	}
	return nil
}

// pubkeyToNpub converts a hex pubkey to its npub bech32 form. Falls back
// to returning the hex if encoding fails (should never happen for a
// 32-byte hex string but we don't want to surface an empty value).
func pubkeyToNpub(hex string) string {
	if hex == "" {
		return ""
	}
	npub, err := identity.EncodeNpub(hex)
	if err != nil {
		return hex
	}
	return npub
}

// humanSince formats a Duration as "Ns" / "Nm" / "Nh" / "Nd" — sufficient
// for the relay panel's "last event Ns ago" column. Avoids time.Since's
// noisy nanosecond formatting.
func humanSince(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d < time.Hour:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d < 24*time.Hour:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	default:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
}
