package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// submitWake writes a mindgate wake signal to dir after an inbound message is
// persisted. Returns an error so the caller can log and continue — a wake
// failure must never abort the inbox write.
func submitWake(dir string, msg inbox.Message) error {
	summary := truncate(envelopeText(msg), 60)
	hint := fmt.Sprintf("%s sent: %s", shortNpub(msg.From), summary)
	sig := wake.Signal{
		ID:          fmt.Sprintf("%d-mindgate-%s", msg.ReceivedAt, shortID(msg.EventID)),
		Reason:      wake.ReasonMindGate,
		TriggeredAt: msg.ReceivedAt,
		Hint:        hint,
		Context: wake.Context{
			InboxUnread:         1, // v0: placeholder; accurate counting can come later
			FirstUnreadFromNpub: msg.From,
			FirstUnreadSummary:  summary,
		},
	}
	return wake.Submit(dir, sig)
}

// envelopeText extracts the human-readable text from a message. Content may be
// a JSON v1 envelope ({..., "text": "..."}) or raw text (older format). Tries
// to decode the envelope first; falls back to the raw content string.
func envelopeText(msg inbox.Message) string {
	var env struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(msg.Content), &env); err == nil && env.Text != "" {
		return env.Text
	}
	return msg.Content
}

// truncate returns s unchanged if len(s) <= n, otherwise s[:n] + "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// shortNpub returns the first 10 characters of npub followed by "…", or the
// full string if it is already 10 characters or fewer.
func shortNpub(npub string) string {
	if len(npub) > 10 {
		return npub[:10] + "…"
	}
	return npub
}

// shortID returns the first 8 characters of eventID, or the full string if
// shorter. Used to build compact wake signal IDs from event IDs.
func shortID(eventID string) string {
	if len(eventID) > 8 {
		return eventID[:8]
	}
	return eventID
}
