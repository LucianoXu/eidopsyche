package dashboard

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

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
			counterpart = sidebarContact{Pubkey: pk, Label: contacts.ShortHex(pk), Tier: contacts.TierAcquaintance}
		}
		bubbles := buildBubbles(deps, pk)
		threadHTML, err := r.Render("thread", threadData{
			Counterpart: counterpart,
			Bubbles:     bubbles,
			OwnPubkey:   deps.OwnPubkey(),
		})
		if err != nil {
			logger.Error("render thread", "err", err)
			http.Error(w, "render failed", 500)
			return
		}

		// Direct navigation (refresh, deep link, no HX-Request header):
		// wrap the thread fragment in the full shell so CSS, sidebar,
		// and topbar render. Without this, refreshing a thread page
		// returns just the inner thread template — the browser shows
		// it as a bare HTML fragment with no flex layout, causing the
		// thread-body to grow to fit all bubbles instead of scrolling.
		if req.Header.Get("HX-Request") == "" {
			label, _ := deps.OwnLabel(ctx)
			side := buildSidebar(ctx, deps, pk, false)
			out, rerr := r.Render("shell", shellData{
				OwnLabel: label,
				OwnNpub:  deps.OwnPubkey(),
				Sidebar:  side,
				Main:     template.HTML(threadHTML), //nolint:gosec // trusted internal template output
			})
			if rerr != nil {
				logger.Error("render shell", "err", rerr)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(threadHTML))
	}
}

func composeHandler(_ DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
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
		// Plain-text body so the global toast handler in shell.html
		// surfaces it cleanly. htmx 2.x drops 4xx/5xx responses by
		// default, so before the toast layer landed this 502 was
		// silently invisible — the operator clicked Send and saw
		// nothing. Status code stays 502 to keep the contract
		// (programmatic clients can still distinguish failure).
		http.Error(w, "send failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// deps.Send only returns nil when at least one relay accepted the
	// publish (otherwise it returns ErrNoRelaysReachable), so we can
	// safely render the initial bubble at tier-1 (✓). Tier-2 (✓✓) is
	// applied later by the SSE OOB-swap path when the peer's ack arrives.
	out, rerr := r.Render("bubble", bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Now(),
		EventID: eventID,
		Status:  "sent",
	})
	if rerr != nil {
		logger.Error("render bubble", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
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
		From:         contacts.FormatPubkey(m.Label, m.From),
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
		From:      contacts.FormatPubkey(s.Label, s.To),
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
		From:         contacts.FormatPubkey(m.Label, m.From),
		Text:         text,
		At:           time.Unix(m.ReceivedAt, 0),
		Malformed:    m.Malformed,
		RejectReason: m.RejectReason,
		EventID:      m.EventID,
	}
}

func sentToBubble(s inbox.Sent) bubbleData {
	text := s.Content
	if env, err := envelope.Decode(s.Content); err == nil && env.Type == envelope.TypeChat {
		text = env.Text
	}
	status := ""
	switch {
	case s.AckedAt != 0:
		status = "delivered"
	case len(s.AcceptedBy) > 0:
		status = "sent"
	}
	return bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Unix(s.SentAt, 0),
		EventID: s.EventID,
		Status:  status,
	}
}
