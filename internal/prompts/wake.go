package prompts

import (
	"fmt"
	"strings"
	"time"
)

// WakeInput is the set of facts the per-wake message in BuildWake
// depends on. The supervisor populates it from the wake signal,
// gate config, and dream state.
type WakeInput struct {
	Reason                string
	Hint                  string
	InboxUnread           int
	SinceLastWakeSeconds  int64
	MasterLikelyAsleep    bool
	QuietStart            string
	QuietEnd              string
	TZ                    string
	SinceLastDreamSeconds int64
	DreamEligible         bool
	LastDreamNote         string
	PlanID                string

	// IsFirstWakeOfNewSession marks this wake as the first one in a
	// freshly minted Claude session — either the very first wake of the
	// mind-form, or the first wake after a dream-end. When set, BuildWake
	// prepends a paragraph telling the mind-form that working memory was
	// reset and on-disk state is authoritative.
	IsFirstWakeOfNewSession bool
	// DreamCount is the most recently completed dream's index (0 if no
	// dream has ever finished). Surfaced in the first-wake prefix.
	DreamCount int
	// LastDreamFinishedAt is the unix-second timestamp of the most
	// recent dream-end (0 if never). Surfaced in the first-wake prefix.
	LastDreamFinishedAt int64
}

// BuildWake renders the user-message body the supervisor sends to
// claude on every wake except birth.
func BuildWake(in WakeInput) string {
	var sb strings.Builder
	if in.IsFirstWakeOfNewSession {
		if in.LastDreamFinishedAt > 0 {
			fmt.Fprintf(&sb,
				"This is the first wake of a new session (your prior working memory was consolidated in dream #%d at %s; on-disk memory/journal/essence are intact, refer to them as needed).\n\n",
				in.DreamCount,
				time.Unix(in.LastDreamFinishedAt, 0).UTC().Format(time.RFC3339),
			)
		} else {
			sb.WriteString("This is the first wake of a new session (no prior dream — this is the mind-form's first session; on-disk substrate is intact).\n\n")
		}
	}
	fmt.Fprintf(&sb, "You have just woken. Reason: %s.", in.Reason)
	if in.Hint != "" {
		fmt.Fprintf(&sb, " %s.", in.Hint)
	}
	fmt.Fprintf(&sb, " Inbox has %d unread message(s).", in.InboxUnread)
	if in.SinceLastWakeSeconds > 0 {
		fmt.Fprintf(&sb, " %ds since last wake.", in.SinceLastWakeSeconds)
	}
	if in.MasterLikelyAsleep && in.QuietStart != "" {
		fmt.Fprintf(&sb, " Master is likely asleep (quiet hours %s–%s%s).",
			in.QuietStart, in.QuietEnd, tzSuffix(in.TZ))
	}
	if in.SinceLastDreamSeconds > 0 {
		hours := in.SinceLastDreamSeconds / 3600
		fmt.Fprintf(&sb, " %dh since your last dream.", hours)
	}
	if in.DreamEligible && in.SinceLastDreamSeconds > 0 {
		fmt.Fprintf(&sb, " You are eligible to dream now.")
	}
	if in.LastDreamNote != "" {
		fmt.Fprintf(&sb, " Last dream: %q.", in.LastDreamNote)
	}
	if in.PlanID != "" {
		fmt.Fprintf(&sb, " (Planned wake; plan id %s.)", in.PlanID)
	}
	return sb.String()
}

func tzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return ", " + tz
}
