// Package wake defines the wake-signal protocol used between the in-container
// gate daemon, cron, manual triggers, and the supervisor.
//
// Two on-disk slots in /eidos/run/wake/:
//   - pending.json: next-up coalescing slot. Producers atomically replace it.
//   - active.json:  currently-being-processed wake. Owned by the supervisor.
//
// See docs/superpowers/specs/2026-05-09-mindforge-v0-design.md §6 for the
// rationale and coalescing rules.
package wake

// SchemaVersion is bumped when the on-disk format changes.
const SchemaVersion = 1

// Reason names a wake source.
type Reason string

const (
	ReasonMindGate  Reason = "mindgate"
	ReasonHeartBeat Reason = "heartbeat"
	ReasonManual    Reason = "manual"
)

// String returns the wake reason as a string. Useful in tests and logs.
func (r Reason) String() string { return string(r) }

// Context is the situational snapshot the agent receives.
type Context struct {
	InboxUnread          int    `json:"inbox_unread"`
	FirstUnreadFromNpub  string `json:"first_unread_from_npub,omitempty"`
	FirstUnreadSummary   string `json:"first_unread_summary,omitempty"`
	SinceLastWakeSeconds int64  `json:"since_last_wake_seconds"`
	LastWakeReason       Reason `json:"last_wake_reason,omitempty"`
	Scheduled            bool   `json:"scheduled"`
}

// Signal is the on-disk wake payload.
type Signal struct {
	V              int      `json:"v"`
	ID             string   `json:"id"`
	Reason         Reason   `json:"reason"`
	TriggeredAt    int64    `json:"triggered_at"`
	Context        Context  `json:"context"`
	CoalescedCount int      `json:"coalesced_count"`
	CoalescedFrom  []Reason `json:"coalesced_from"`
	Hint           string   `json:"hint,omitempty"`
}
