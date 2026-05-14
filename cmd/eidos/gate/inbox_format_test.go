package gate

import (
	"strings"
	"testing"
)

func TestFormatInboxRow_PrefersLabel(t *testing.T) {
	row := map[string]any{
		"received_at": float64(1_700_000_000),
		"from":        "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"label":       "alice",
		"content":     "hello",
	}
	got := formatInboxRow(row)
	if !strings.Contains(got, "alice") {
		t.Errorf("expected label rendered, got %q", got)
	}
	if strings.Contains(got, "abc1234567") {
		t.Errorf("did not expect raw hex when label present, got %q", got)
	}
}

func TestFormatInboxRow_FallsBackToShortHex(t *testing.T) {
	row := map[string]any{
		"received_at": float64(1_700_000_000),
		"from":        "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"content":     "hi",
	}
	got := formatInboxRow(row)
	if !strings.Contains(got, "abc1234…") {
		t.Errorf("expected short-hex with ellipsis, got %q", got)
	}
}

func TestFormatOutboxRow_PrefersLabel(t *testing.T) {
	row := map[string]any{
		"sent_at": float64(1_700_000_000),
		"to":      "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"label":   "bob",
		"content": "yo",
	}
	got := formatOutboxRow(row)
	if !strings.Contains(got, "bob") {
		t.Errorf("expected label rendered, got %q", got)
	}
	if strings.Contains(got, "abc1234567") {
		t.Errorf("did not expect raw hex when label present, got %q", got)
	}
}

func TestFormatOutboxRow_FallsBackToShortHex(t *testing.T) {
	row := map[string]any{
		"sent_at": float64(1_700_000_000),
		"to":      "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"content": "yo",
	}
	got := formatOutboxRow(row)
	if !strings.Contains(got, "abc1234…") {
		t.Errorf("expected short-hex with ellipsis, got %q", got)
	}
}

func TestFormatInboxRow_PendingTagged(t *testing.T) {
	// A row with Pending=true and no label renders the "(pending)" tag
	// followed by the short-hex so the operator can copy it into
	// `eidos gate add-contact` directly.
	row := map[string]any{
		"received_at": float64(1_700_000_000),
		"from":        "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"content":     "first contact",
		"pending":     true,
	}
	got := formatInboxRow(row)
	if !strings.Contains(got, "(pending)") {
		t.Errorf("expected (pending) tag, got %q", got)
	}
	if !strings.Contains(got, "abc1234…") {
		t.Errorf("expected short-hex after pending tag, got %q", got)
	}
}

func TestFormatInboxRow_PendingButLabelKnownStillLabel(t *testing.T) {
	// If Pending=true (e.g. the sender is TierBlocked) but a label
	// exists, prefer the label — the operator already chose a name
	// for this peer.
	row := map[string]any{
		"received_at": float64(1_700_000_000),
		"from":        "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc",
		"label":       "mallory",
		"content":     "msg",
		"pending":     true,
	}
	got := formatInboxRow(row)
	if !strings.Contains(got, "mallory") {
		t.Errorf("expected label preferred for known-but-blocked, got %q", got)
	}
	if strings.Contains(got, "(pending)") {
		t.Errorf("did not expect (pending) tag when label set, got %q", got)
	}
}
