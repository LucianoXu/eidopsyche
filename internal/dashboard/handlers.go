package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
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
	mux.HandleFunc("/settings", settingsShellHandler(deps, r, logger))
	mux.HandleFunc("/settings/identity", settingsIdentityHandler(deps, r, logger))
	mux.HandleFunc("/settings/identity/label", settingsLabelPostHandler(deps, r, logger))
	mux.HandleFunc("/settings/config", settingsConfigHandler(deps, r, logger))
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
		// refreshes via the Referer header — htmx fires this hx-get from
		// inside whichever main page the operator is on, so the request's
		// Referer points back at it.
		activeSettings := strings.Contains(req.Header.Get("Referer"), "/settings")
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

// settingsShellHandler is the only Settings route that renders a *full
// page* (including topbar + sidebar). The tab fragments below render
// only their pane so htmx can swap into #settings-pane.
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
		label, _ := deps.OwnLabel(ctx)
		side := buildSidebar(ctx, deps, "", true)

		shell := buildSettingsShell(ctx, deps, "identity")
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
				out.Saved = true
				out.Label = label
				if uri, cerr := deps.OwnCardURI(ctx); cerr == nil {
					out.CardURI = uri
				}
			}
		}
		// On error, surface the bad input back to the form so the
		// operator can fix it without retyping.
		if out.Error != "" {
			out.Label = raw
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
	case "config":
		v := buildSettingsConfig(deps, "", "", "")
		out.Config = &v
	}
	return out
}

func buildSettingsIdentity(ctx context.Context, deps DashboardDeps) settingsIdentityData {
	label, _ := deps.OwnLabel(ctx)
	npub := pubkeyToNpub(deps.OwnPubkey())
	cardURI, _ := deps.OwnCardURI(ctx)
	return settingsIdentityData{
		Label:   label,
		Npub:    npub,
		Hex:     deps.OwnPubkey(),
		CardURI: cardURI,
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
