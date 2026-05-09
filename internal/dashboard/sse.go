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
		// Wrap as an htmx OOB swap so we REPLACE the bubble that the
		// POST /thread/{pubkey}/send form-submit response already placed
		// in the thread. Without OOB, the regular sse-swap="beforeend"
		// would append a duplicate. CLI / MCP-originated sends produce
		// no existing bubble, so the OOB swap is silently discarded —
		// dashboard users see the row only after a page refresh, which
		// matches the behavior before tier-2 acks landed.
		data := sentToBubble(*ev.Sent)
		data.OOB = true
		out, err := r.Render("bubble", data)
		if err != nil {
			logger.Warn("render outbox bubble for SSE", "err", err)
			return "", ""
		}
		return "outbox.message:" + ev.Sent.To, out
	case "contact.added", "contact.removed", "contact.relabeled", "contact.tier-changed":
		return ev.Kind, "(refresh)"
	case "invite.created", "invite.revoked", "invite.redeemed":
		// Phase 3 signal-only events. The Invites pane subscribes via
		// hx-trigger="sse:invite.created,...,invite.redeemed from:body"
		// and re-fetches /settings/invites — same idempotent pattern as
		// the contacts list. invite.redeemed also fires contact.added
		// (emitted from the daemon side), so the sidebar refreshes too.
		return ev.Kind, "(refresh)"
	case "identity.label-changed", "config.changed":
		// Phase 1 signal-only events: the consumer (topbar, Identity
		// pane, Config pane) re-fetches its own URL on the trigger;
		// the SSE payload is just the literal "(refresh)" string used
		// across the rest of the signal-only family.
		return ev.Kind, "(refresh)"
	case "relay.state":
		// The relay strip renders the full row list on every transition —
		// the strip is small and per-event diffing is more code than it
		// saves. Browser-side, the <footer id="relays-strip"> in
		// templates/shell.html re-fetches /relays via hx-get on
		// sse:relay.state and swaps the rendered partial as innerHTML.
		return ev.Kind, "(refresh)"
	case "relay.added", "relay.removed":
		// Phase 4 signal-only events: the /settings/relays pane
		// subscribes via hx-trigger and re-fetches itself when an
		// own_relays row changes. The right-rail relays panel keeps
		// using relay.state for connection-level transitions.
		return ev.Kind, "(refresh)"
	case "service.status":
		// Phase 5 signal-only event. The Service tab refetches its
		// own status block on the trigger; the lifecycle log doesn't
		// reload (it's append-only — refetching would erase history).
		return ev.Kind, "(refresh)"
	default:
		// Phase 5 lifecycle events use scoped kind names like
		// "lifecycle.line:<jobID>" / "lifecycle.done:<jobID>". They
		// carry the rendered HTML directly on Event.HTML — there's no
		// renderer round-trip because the line text isn't known until
		// the child writes it.
		if ev.HTML != "" && (hasPrefix(ev.Kind, "lifecycle.line:") ||
			hasPrefix(ev.Kind, "lifecycle.done:")) {
			return ev.Kind, ev.HTML
		}
		// Log unhandled `lifecycle.*` kinds so a producer-side typo or
		// a future event family (lifecycle.error, lifecycle.heartbeat)
		// shows up at warn level instead of vanishing silently. Other
		// unrecognised kinds are returned empty (skipped) without a
		// log — most are pre-Phase-1 cruft we don't care to surface.
		if hasPrefix(ev.Kind, "lifecycle.") {
			logger.Warn("unhandled lifecycle event kind; check producer/consumer alignment",
				"kind", ev.Kind, "html_len", len(ev.HTML))
		}
		return "", ""
	}
}

// hasPrefix is a small helper so renderEvent doesn't import strings
// just for one call. Kept private to this package.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
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
