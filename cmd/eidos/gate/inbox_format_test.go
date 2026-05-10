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
