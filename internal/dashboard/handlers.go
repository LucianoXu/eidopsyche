package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
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

func messagesHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		onlyMal := req.URL.Query().Get("malformed") == "1"
		cursor := req.URL.Query().Get("cursor")
		view := buildMessagesView(deps, onlyMal, cursor)
		out, err := r.Render("messages", view)
		if err != nil {
			logger.Error("render messages", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func threadOrSendHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	getH := threadHandler(deps, r, logger)
	postH := sendHandler(deps, r, logger)
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/send") {
			postH(w, req)
			return
		}
		getH(w, req)
	}
}

func threadHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pk := strings.TrimPrefix(req.URL.Path, "/thread/")
		if i := strings.Index(pk, "/"); i >= 0 {
			pk = pk[:i]
		}
		if pk == "" {
			http.NotFound(w, req)
			return
		}
		ctx := req.Context()

		c := lookupContact(ctx, deps, pk)
		var counterpart sidebarContact
		if c != nil {
			counterpart = sidebarContact{Pubkey: c.Pubkey, Label: c.Label, Tier: c.Tier}
		} else {
			counterpart = sidebarContact{Pubkey: pk, Label: shortenPubkey(pk), Tier: contacts.TierAcquaintance}
		}
		bubbles := buildBubbles(deps, pk)
		out, err := r.Render("thread", threadData{
			Counterpart: counterpart,
			Bubbles:     bubbles,
			OwnPubkey:   deps.OwnPubkey(),
		})
		if err != nil {
			logger.Error("render thread", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func composeHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out, err := r.Render("compose", composeData{})
		if err != nil {
			logger.Error("render compose", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// sendHandler is the POST /thread/{pubkey}/send endpoint; defined here for
// proximity to threadHandler. Implementation lives in sendChat below.
func sendHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(req.URL.Path, "/thread/")
		path = strings.TrimSuffix(path, "/send")
		pk := path
		if pk == "" {
			http.NotFound(w, req)
			return
		}
		sendChat(w, req, deps, r, logger, pk)
	}
}

// composeSendHandler routes the compose-to-npub form: the pubkey is in the
// `to` field, not the URL path.
func composeSendHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		pk := strings.TrimSpace(req.Form.Get("to"))
		if pk == "" {
			http.Error(w, "missing recipient", http.StatusBadRequest)
			return
		}
		sendChat(w, req, deps, r, logger, pk)
	}
}

func sendChat(w http.ResponseWriter, req *http.Request, deps DashboardDeps, r *renderer, logger *slog.Logger, pk string) {
	// Defence in depth: refuse non-POST even when the route handler
	// already checked. Side effects must never happen on safe methods.
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Form.Get("text"))
	if text == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	env := envelope.Envelope{
		V:      envelope.SchemaVersion,
		Type:   envelope.TypeChat,
		Text:   text,
		Client: &envelope.Client{Name: "eidos-dashboard", Ver: "0.1.0"},
	}
	eventID, err := deps.Send(req.Context(), pk, env)
	if err != nil {
		logger.Warn("dashboard send failed", "err", err, "to", pk)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<div class="error">send failed: ` + template.HTMLEscapeString(err.Error()) + `</div>`))
		return
	}

	out, rerr := r.Render("bubble", bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Now(),
		EventID: eventID,
	})
	if rerr != nil {
		logger.Error("render bubble", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// --- view builders ---

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

func buildMessagesView(deps DashboardDeps, onlyMal bool, cursor string) messagesData {
	// v1 returns the newest page only. Cursor / "Load more" pagination
	// requires a "before" (upper-bound) timestamp filter that the inbox
	// store does not yet expose; building it on top of the existing
	// "since" (lower-bound) API was racy and shipped broken in the
	// initial draft. Deferred to v2 alongside a store API extension.
	_ = cursor
	rows := []messageRow{}

	if !onlyMal {
		if sents, err := deps.ListOutbox(nil, "", messagesPageSize); err == nil {
			for _, s := range sents {
				rows = append(rows, sentToRow(s))
			}
		}
	}
	if msgs, err := deps.ListInbox(nil, "", messagesPageSize); err == nil {
		for _, m := range msgs {
			if onlyMal && !m.Malformed {
				continue
			}
			rows = append(rows, msgToRow(m))
		}
	}
	// Newest first.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].At.After(rows[j-1].At); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	if len(rows) > messagesPageSize {
		rows = rows[:messagesPageSize]
	}
	return messagesData{Rows: rows, OnlyMal: onlyMal}
}

func msgToRow(m inbox.Message) messageRow {
	return messageRow{
		Direction:    "in",
		From:         shortenPubkey(m.From),
		Pubkey:       m.From,
		Preview:      previewFor(m.Content),
		At:           time.Unix(m.ReceivedAt, 0),
		Malformed:    m.Malformed,
		RejectReason: m.RejectReason,
		EventID:      m.EventID,
	}
}

func sentToRow(s inbox.Sent) messageRow {
	return messageRow{
		Direction: "out",
		From:      shortenPubkey(s.To),
		Pubkey:    s.To,
		Preview:   previewFor(s.Content),
		At:        time.Unix(s.SentAt, 0),
		EventID:   s.EventID,
	}
}

func previewFor(content string) string {
	env, err := envelope.Decode(content)
	if err != nil {
		return content
	}
	return env.Text
}

func buildBubbles(deps DashboardDeps, pk string) []bubbleData {
	bubbles := []bubbleData{}
	if msgs, err := deps.ListInbox(nil, pk, 100); err == nil {
		for _, m := range msgs {
			bubbles = append(bubbles, msgToBubble(m))
		}
	}
	if sents, err := deps.ListOutbox(nil, pk, 100); err == nil {
		for _, s := range sents {
			bubbles = append(bubbles, sentToBubble(s))
		}
	}
	for i := 1; i < len(bubbles); i++ {
		for j := i; j > 0 && bubbles[j].At.Before(bubbles[j-1].At); j-- {
			bubbles[j], bubbles[j-1] = bubbles[j-1], bubbles[j]
		}
	}
	return bubbles
}

func msgToBubble(m inbox.Message) bubbleData {
	text := m.Content
	if env, err := envelope.Decode(m.Content); err == nil {
		text = env.Text
	}
	return bubbleData{
		Self:         false,
		From:         shortenPubkey(m.From),
		Text:         text,
		At:           time.Unix(m.ReceivedAt, 0),
		Malformed:    m.Malformed,
		RejectReason: m.RejectReason,
		EventID:      m.EventID,
	}
}

func sentToBubble(s inbox.Sent) bubbleData {
	text := s.Content
	if env, err := envelope.Decode(s.Content); err == nil {
		text = env.Text
	}
	return bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Unix(s.SentAt, 0),
		EventID: s.EventID,
	}
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

func shortenPubkey(pk string) string {
	if len(pk) <= 16 {
		return pk
	}
	return pk[:8] + "…" + pk[len(pk)-4:]
}

// relaysData is the template payload for the relay-health panel.
type relaysData struct {
	Rows []relayRow
}

type relayRow struct {
	URL          string
	Role         string
	State        string
	LastError    string
	LastEventAgo string
}

func buildRelaysView(deps DashboardDeps) relaysData {
	now := time.Now().Unix()
	snap := deps.ListRelayHealth()
	rows := make([]relayRow, 0, len(snap))
	for _, h := range snap {
		ago := "—"
		if h.LastEventAt > 0 {
			d := time.Duration(now-h.LastEventAt) * time.Second
			ago = humanSince(d)
		}
		rows = append(rows, relayRow{
			URL:          h.URL,
			Role:         h.Role,
			State:        h.State,
			LastError:    h.LastError,
			LastEventAgo: ago,
		})
	}
	// Stable order: home first, fallback, contact, extra; URL alphabetic within.
	sortRelayRows(rows)
	return relaysData{Rows: rows}
}

func relaysHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out, err := r.Render("relays", buildRelaysView(deps))
		if err != nil {
			logger.Error("render relays", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// sortRelayRows orders rows by role priority (home > fallback > contact >
// extra > anything else) then alphabetically by URL within each role.
func sortRelayRows(rows []relayRow) {
	rank := func(role string) int {
		switch role {
		case "home":
			return 0
		case "fallback":
			return 1
		case "contact":
			return 2
		case "extra":
			return 3
		default:
			return 4
		}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if rank(a.Role) < rank(b.Role) {
				break
			}
			if rank(a.Role) == rank(b.Role) && a.URL <= b.URL {
				break
			}
			rows[j-1], rows[j] = b, a
		}
	}
}

// ---------------------------------------------------------------- *
// Phase 1 — Settings (Operator panel)
//
// Routes:
//   GET  /settings                       (renders shell + Identity tab)
//   GET  /settings/identity              (Identity pane fragment)
//   POST /settings/identity/label        (set own label)
//   GET  /settings/config                (Config pane fragment)
//   POST /settings/config                (set one config key)
// ---------------------------------------------------------------- */

const labelMaxLen = 64

// settingsShellHandler renders /settings. With HX-Request set (e.g. the
// sidebar Operator → Settings link clicked from an already-loaded page),
// it returns just the "settings" fragment so htmx can swap it into
// #main without nesting a whole document. On a cold address-bar load,
// it returns the full layout (topbar + sidebar + settings pane) so the
// page stands on its own.
func settingsShellHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/settings" {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		shell := buildSettingsShell(ctx, deps, "identity")

		if req.Header.Get("HX-Request") != "" {
			out, err := r.Render("settings", shell)
			if err != nil {
				logger.Error("render settings", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))
			return
		}

		// Cold load: render the full document.
		renderSettingsFullPage(w, deps, r, logger, ctx, "identity")
	}
}

func settingsIdentityHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		// Direct navigation (e.g. address bar) lacks the HX-Request
		// header; in that case render the full shell so the page is
		// not just a fragment hanging in space.
		if req.Header.Get("HX-Request") == "" {
			renderSettingsFullPage(w, deps, r, logger, ctx, "identity")
			return
		}
		out, err := r.Render("settings_identity", buildSettingsIdentity(ctx, deps))
		if err != nil {
			logger.Error("render settings_identity", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func settingsLabelPostHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		raw := req.Form.Get("label")
		label := strings.TrimSpace(raw)
		ctx := req.Context()

		// Start from the current persisted snapshot — Label and CardURI
		// reflect what's actually saved; FormLabel will be set below
		// to either the just-saved value or the rejected raw input so
		// the input shows the right thing without ever poisoning the
		// colophon with unsaved data.
		out := buildSettingsIdentity(ctx, deps)
		switch {
		case label == "":
			out.Error = "Label cannot be empty."
		case len([]rune(label)) > labelMaxLen:
			out.Error = fmt.Sprintf("Label too long (%d runes; max %d).", len([]rune(label)), labelMaxLen)
		default:
			if err := deps.SetOwnLabel(ctx, label); err != nil {
				logger.Warn("dashboard set-label failed", "err", err)
				out.Error = "Save failed: " + err.Error()
			} else {
				// Persisted: pull a fresh snapshot so Label, CardURI,
				// and FormLabel all reflect the new state atomically.
				out = buildSettingsIdentity(ctx, deps)
				out.Saved = true
			}
		}
		if out.Error != "" {
			// Preserve the operator's raw input in the form input only.
			// out.Label still holds the persisted label so the colophon
			// keeps showing the truth.
			out.FormLabel = raw
		}
		rendered, rerr := r.Render("settings_identity", out)
		if rerr != nil {
			logger.Error("render settings_identity", "err", rerr)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Even on validation error we return 200; htmx default doesn't
		// swap 4xx, and the form-flash chip carries the message.
		_, _ = w.Write([]byte(rendered))
	}
}

func settingsConfigHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		switch req.Method {
		case http.MethodGet:
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "config")
				return
			}
			out, err := r.Render("settings_config", buildSettingsConfig(deps, "", "", ""))
			if err != nil {
				logger.Error("render settings_config", "err", err)
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
			path := strings.TrimSpace(req.Form.Get("path"))
			value := req.Form.Get("value")
			if _, ok := config.KeyByPath(path); !ok {
				http.Error(w, "unknown config key", http.StatusBadRequest)
				return
			}

			var rowErr string
			if err := deps.ConfigSet(ctx, path, value); err != nil {
				rowErr = err.Error()
				logger.Warn("dashboard config-set failed", "err", err, "path", path)
			}

			// Render only the affected row so htmx can swap-out-of-place
			// — every other row stays intact.
			snap, err := deps.ConfigSnapshot()
			if err != nil {
				logger.Error("config snapshot after set", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			row := configRowFromKey(path, snap, rowErr)
			rendered, rerr := r.Render("settings_config_row", row)
			if rerr != nil {
				logger.Error("render settings_config_row", "err", rerr)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(rendered))

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// renderSettingsFullPage emits the whole layout (topbar + sidebar +
// settings pane) so deep-link or address-bar navigation works without a
// preceding "/" load.
func renderSettingsFullPage(
	w http.ResponseWriter, deps DashboardDeps, r *renderer, logger *slog.Logger,
	ctx context.Context, active string,
) {
	label, _ := deps.OwnLabel(ctx)
	side := buildSidebar(ctx, deps, "", true)
	shell := buildSettingsShell(ctx, deps, active)
	mainHTML, err := r.Render("settings", shell)
	if err != nil {
		logger.Error("render settings", "err", err)
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

// ── settings view-builders ──────────────────────────────────────────

func buildSettingsShell(ctx context.Context, deps DashboardDeps, active string) settingsShellData {
	out := settingsShellData{Active: active}
	out.OwnLabel, _ = deps.OwnLabel(ctx)
	switch active {
	case "identity":
		v := buildSettingsIdentity(ctx, deps)
		out.Identity = &v
	case "contacts":
		v := buildSettingsContacts(ctx, deps, "")
		out.Contacts = &v
	case "config":
		v := buildSettingsConfig(deps, "", "", "")
		out.Config = &v
	case "invites":
		v := buildSettingsInvites(ctx, deps)
		out.Invites = &v
	case "relays":
		v := buildSettingsRelays(ctx, deps, "")
		out.Relays = &v
	case "service":
		v := buildSettingsService(ctx, deps)
		out.Service = &v
	}
	return out
}

func buildSettingsIdentity(ctx context.Context, deps DashboardDeps) settingsIdentityData {
	label, _ := deps.OwnLabel(ctx)
	npub := pubkeyToNpub(deps.OwnPubkey())
	cardURI, _ := deps.OwnCardURI(ctx)
	return settingsIdentityData{
		Label:     label,
		FormLabel: label, // input prefills with the current value
		Npub:      npub,
		Hex:       deps.OwnPubkey(),
		CardURI:   cardURI,
	}
}

func buildSettingsConfig(deps DashboardDeps, errMsg, errPath, errMsgRow string) settingsConfigData {
	out := settingsConfigData{Error: errMsg}
	snap, err := deps.ConfigSnapshot()
	if err != nil {
		out.Error = "Could not load config.toml: " + err.Error()
		return out
	}
	for _, k := range config.KeyList() {
		row := configRowFromKey(k.Path, snap, "")
		if k.Path == errPath {
			row.Error = errMsgRow
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

func configRowFromKey(path string, snap config.Config, rowErr string) settingsConfigRow {
	k, ok := config.KeyByPath(path)
	if !ok {
		return settingsConfigRow{Path: path, Slug: slugifyPath(path), Editable: false, Error: "unknown key"}
	}
	return settingsConfigRow{
		Path:        k.Path,
		Slug:        slugifyPath(k.Path),
		Description: k.Description,
		Value:       k.Get(&snap),
		Editable:    true,
		Error:       rowErr,
	}
}

func slugifyPath(path string) string { return strings.ReplaceAll(path, ".", "-") }

// isSettingsURL parses raw as a URL and reports whether the path is
// /settings or any /settings/ sub-path. Used to flag the sidebar
// Settings entry as active across SSE-triggered refreshes. Resilient
// to absolute or path-only inputs and rejects unrelated paths whose
// query string just happens to contain "/settings".
func isSettingsURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := u.Path
	return p == "/settings" || strings.HasPrefix(p, "/settings/")
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
