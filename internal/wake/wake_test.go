package wake

import (
	"encoding/json"
	"testing"
)

func TestSignalJSONRoundTrip(t *testing.T) {
	in := Signal{
		V:           1,
		ID:          "20260509T103045Z-mindgate-7f2e",
		Reason:      ReasonMindGate,
		TriggeredAt: 1715251845,
		Context: Context{
			InboxUnread:          3,
			FirstUnreadFromNpub:  "npub1alice",
			FirstUnreadSummary:   "hello",
			SinceLastWakeSeconds: 14400,
			LastWakeReason:       ReasonHeartBeat,
			Scheduled:            false,
		},
		CoalescedCount: 0,
		CoalescedFrom:  []Reason{},
		Hint:           "Alice sent a message",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Signal
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Reason != in.Reason || out.Context.InboxUnread != 3 {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestReasonValues(t *testing.T) {
	if string(ReasonMindGate) != "mindgate" || string(ReasonHeartBeat) != "heartbeat" || string(ReasonManual) != "manual" {
		t.Errorf("reason constants drifted")
	}
}
