package dashboard

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// sseHub wires DashboardDeps.SubscribeEvents to an HTTP SSE handler.
type sseHub struct {
	deps DashboardDeps
}

func newSSEHub(deps DashboardDeps) *sseHub { return &sseHub{deps: deps} }

func (h *sseHub) handler(r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		// Flush headers immediately so the client knows the connection is
		// live; without this, HTTP clients wait for a body before returning
		// from their Do() call.
		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ch, cancel := h.deps.SubscribeEvents()
		defer cancel()

		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()

		ctx := req.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				_, _ = fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case ev, ok := <-ch:
				if !ok {
					return
				}
				eventName, html := renderEvent(r, ev, logger)
				if eventName == "" || html == "" {
					continue
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, escapeSSEData(html))
				flusher.Flush()
			}
		}
	}
}

// renderEvent maps an Event to the HTML fragment payload htmx will swap.
// Returns ("", "") if the event has no UI projection.
//
// inbox.message and outbox.message are emitted under a counterpart-scoped
// event name (e.g. "inbox.message:<hex>") so a thread view subscribed to
// "inbox.message:<bob>" only receives bubbles from Bob — without this,
// every chat thread would mix every other contact's bubbles into its
// scroll. Sidebar-targeted events (contact.added/removed/relabeled) are
// emitted under their bare kind name.
func renderEvent(r *renderer, ev Event, logger *slog.Logger) (string, string) {
	switch ev.Kind {
	case "inbox.message":
		if ev.Message == nil {
			return "", ""
		}
		out, err := r.Render("bubble", msgToBubble(*ev.Message))
		if err != nil {
			logger.Warn("render inbox bubble for SSE", "err", err)
			return "", ""
		}
		return "inbox.message:" + ev.Message.From, out
	case "outbox.message":
		if ev.Sent == nil {
			return "", ""
		}
		out, err := r.Render("bubble", sentToBubble(*ev.Sent))
		if err != nil {
			logger.Warn("render outbox bubble for SSE", "err", err)
			return "", ""
		}
		return "outbox.message:" + ev.Sent.To, out
	case "contact.added", "contact.removed", "contact.relabeled":
		return ev.Kind, "(refresh)"
	default:
		return "", ""
	}
}

// escapeSSEData replaces newlines so the payload fits on one `data:` line.
func escapeSSEData(s string) string {
	out := make([]byte, 0, len(s))
	for _, ch := range []byte(s) {
		switch ch {
		case '\r':
			// drop
		case '\n':
			out = append(out, ' ')
		default:
			out = append(out, ch)
		}
	}
	return string(out)
}
