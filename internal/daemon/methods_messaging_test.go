package daemon

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

// TestFormatNoRelaysError_IncludesPerRelayReasons: when no relay accepts the
// publish, the resulting NO_RELAYS_REACHABLE error must carry each relay's
// rejection reason so a mind-form (or operator) can distinguish "relay TCP
// dead" from "relay returned OK false: blocked: spam". The original handler
// only said "publish failed on all N relays" — alice's wake on 2026-05-12
// observed this and (correctly) concluded "the framework is hiding the real
// reason", retried 3 times, and called the relay offline.
func TestFormatNoRelaysError_IncludesPerRelayReasons(t *testing.T) {
	res := []nostr.PublishResult{
		{Relay: "wss://a", OK: false, Reason: "blocked: spam"},
		{Relay: "wss://b", OK: false, Reason: "context deadline exceeded"},
	}
	err := formatNoRelaysError(res, 2)
	if err == nil {
		t.Fatal("got nil, want error")
	}
	if err.Code != ipc.ErrNoRelaysReachable {
		t.Errorf("Code = %q, want %q", err.Code, ipc.ErrNoRelaysReachable)
	}
	for _, want := range []string{
		"publish failed on all 2 relays",
		"wss://a: blocked: spam",
		"wss://b: context deadline exceeded",
	} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("Message = %q, missing substring %q", err.Message, want)
		}
	}
}

// TestFormatNoRelaysError_EmptyReasonFallback: a result with OK=false but
// no Reason string must not produce a colon-with-blank suffix (cosmetics)
// or omit the relay url entirely (debuggability). Use a stable placeholder.
func TestFormatNoRelaysError_EmptyReasonFallback(t *testing.T) {
	res := []nostr.PublishResult{{Relay: "wss://a", OK: false, Reason: ""}}
	err := formatNoRelaysError(res, 1)
	if !strings.Contains(err.Message, "wss://a") {
		t.Errorf("Message = %q, must mention the relay URL", err.Message)
	}
	if strings.Contains(err.Message, "wss://a: \"") || strings.Contains(err.Message, "wss://a: ;") {
		t.Errorf("Message = %q, dangling empty-reason punctuation", err.Message)
	}
}

// TestFormatNoRelaysError_SkipsOKResults: a partial-failure scenario should
// not surface here (caller only calls formatNoRelaysError when len(accepted)
// == 0), but defensively the formatter must skip any OK=true result so an
// inadvertent call still produces a coherent message.
func TestFormatNoRelaysError_SkipsOKResults(t *testing.T) {
	res := []nostr.PublishResult{
		{Relay: "wss://a", OK: true, Reason: ""},
		{Relay: "wss://b", OK: false, Reason: "rate-limited"},
	}
	err := formatNoRelaysError(res, 2)
	if strings.Contains(err.Message, "wss://a") {
		t.Errorf("Message = %q, must not include the OK relay", err.Message)
	}
	if !strings.Contains(err.Message, "wss://b: rate-limited") {
		t.Errorf("Message = %q, missing failing relay reason", err.Message)
	}
}
