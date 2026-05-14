package prompts_test

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

func TestBuildWake(t *testing.T) {
	got := prompts.BuildWake(prompts.WakeInput{
		Reason:               "mindgate",
		Hint:                 "Alice sent: hello",
		InboxUnread:          1,
		SinceLastWakeSeconds: 60,
	})
	for _, want := range []string{"You have just woken", "mindgate", "Alice sent", "1 unread"} {
		if !strings.Contains(got, want) {
			t.Errorf("wake message missing %q; got: %s", want, got)
		}
	}
}

func TestBuildWake_FirstWakeOfNewSession_AfterDream(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: true,
		DreamCount:              5,
		LastDreamFinishedAt:     1700000000,
	})
	if !strings.Contains(msg, "first wake of a new session") {
		t.Fatalf("want first-wake prefix, got %q", msg)
	}
	if !strings.Contains(msg, "dream #5") {
		t.Fatalf("want dream #N reference, got %q", msg)
	}
	if !strings.Contains(msg, "Reason: heartbeat") {
		t.Fatalf("want status snapshot after prefix, got %q", msg)
	}
}

func TestBuildWake_FirstWakeNoPriorDream(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: true,
		DreamCount:              0,
		LastDreamFinishedAt:     0,
	})
	if !strings.Contains(msg, "no prior dream") {
		t.Fatalf("want no-prior-dream variant, got %q", msg)
	}
	if !strings.Contains(msg, "Reason: heartbeat") {
		t.Fatalf("want status snapshot after prefix, got %q", msg)
	}
}

func TestBuildWake_NotFirstWake_NoPrefix(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: false,
		DreamCount:              5,
		LastDreamFinishedAt:     1700000000,
	})
	if strings.Contains(msg, "first wake of a new session") {
		t.Fatalf("must not include prefix for non-first wake, got %q", msg)
	}
}

func TestBuildWake_DreamFields(t *testing.T) {
	got := prompts.BuildWake(prompts.WakeInput{
		Reason:                "heartbeat",
		InboxUnread:           0,
		MasterLikelyAsleep:    true,
		QuietStart:            "22:00",
		QuietEnd:              "06:00",
		TZ:                    "Asia/Shanghai",
		SinceLastDreamSeconds: 30 * 3600,
		DreamEligible:         true,
		LastDreamNote:         "consolidated bob",
	})
	for _, want := range []string{"Master is likely asleep", "Asia/Shanghai", "30h since your last dream", "eligible to dream", "consolidated bob"} {
		if !strings.Contains(got, want) {
			t.Errorf("wake message missing %q; got: %s", want, got)
		}
	}
}

func TestBuildWake_PlannedWake(t *testing.T) {
	got := prompts.BuildWake(prompts.WakeInput{
		Reason:      "planned",
		Hint:        "follow up on bob",
		InboxUnread: 0,
		PlanID:      "20260509T123000Z-plan-7f2eab19",
	})
	if !strings.Contains(got, "Planned wake") {
		t.Errorf("planned wake message missing marker: %s", got)
	}
	if !strings.Contains(got, "follow up on bob") {
		t.Errorf("planned wake message missing hint: %s", got)
	}
}

func TestBuildWake_FirstWordsPending_AddsPrefix(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:            "heartbeat",
		OwnerLabel:        "Alice",
		FirstWordsPending: true,
	})
	for _, want := range []string{
		"first time",
		"chest/first-message.md",
		"eidos gate send",
		"creator_npub",
		"self/first_words_at",
		"calling-words.md",
		"Alice",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("first-words prefix missing %q; got:\n%s", want, msg)
		}
	}
}

func TestBuildWake_FirstWordsPendingFalse_NoPrefix(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:            "heartbeat",
		FirstWordsPending: false,
	})
	if strings.Contains(msg, "chest/first-message.md") {
		t.Errorf("first-words prefix must not appear when FirstWordsPending=false; got:\n%s", msg)
	}
}

func TestBuildWake_FirstWordsAboveNewSession(t *testing.T) {
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:                  "heartbeat",
		OwnerLabel:              "Alice",
		FirstWordsPending:       true,
		IsFirstWakeOfNewSession: true,
	})
	firstWordsIdx := strings.Index(msg, "chest/first-message.md")
	newSessionIdx := strings.Index(msg, "first wake of a new session")
	if firstWordsIdx < 0 || newSessionIdx < 0 {
		t.Fatalf("expected both prefixes; firstWords=%d newSession=%d msg=%q",
			firstWordsIdx, newSessionIdx, msg)
	}
	if firstWordsIdx > newSessionIdx {
		t.Errorf("first-words prefix must precede new-session paragraph; firstWords=%d newSession=%d",
			firstWordsIdx, newSessionIdx)
	}
}

func TestBuildWake_FirstWordsPending_EmptyOwnerLabel(t *testing.T) {
	// Empty OwnerLabel must not crash or omit the prefix; the prefix
	// still renders with a blank substitution. Forwarder is expected
	// to provide a non-empty label in production, but this guards
	// against an incomplete identity.toml in dev/test setups.
	msg := prompts.BuildWake(prompts.WakeInput{
		Reason:            "heartbeat",
		OwnerLabel:        "",
		FirstWordsPending: true,
	})
	if !strings.Contains(msg, "chest/first-message.md") {
		t.Errorf("first-words prefix missing for empty OwnerLabel; got:\n%s", msg)
	}
}
