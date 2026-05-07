package dashboard

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
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
		side := buildSidebar(ctx, deps, "")
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
		side := buildSidebar(ctx, deps, "")
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

func buildSidebar(ctx context.Context, deps DashboardDeps, activePubkey string) sidebarData {
	cs, err := deps.ListContacts(ctx)
	if err != nil {
		return sidebarData{}
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
	return sidebarData{Contacts: sortContactsForSidebar(out)}
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
