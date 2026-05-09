package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

func TestRender_Shell_ContainsOwnNpub(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("shell", shellData{OwnLabel: "alice", OwnNpub: "npub1self0000"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "npub1self") {
		t.Errorf("shell missing own npub: %s", out)
	}
}

func TestRender_Bubble_FromContact(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{Self: false, From: "Bob", Text: "hello", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("bubble missing text: %s", out)
	}
	if strings.Contains(out, `class="bubble self`) {
		t.Errorf("non-self bubble should not have .self class: %s", out)
	}
}

func TestRender_Bubble_Malformed(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{
		Self:         false,
		From:         "Carol",
		Text:         "garbage content",
		At:           time.Now(),
		Malformed:    true,
		RejectReason: "not_envelope",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "malformed") {
		t.Errorf("malformed bubble missing class/marker: %s", out)
	}
	if !strings.Contains(out, "not_envelope") {
		t.Errorf("malformed bubble missing reason: %s", out)
	}
}

func TestRender_Bubble_StatusSent(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{Self: true, From: "you", Text: "hi", At: time.Now(), Status: "sent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("sent bubble missing ✓: %s", out)
	}
	if strings.Contains(out, "✓✓") {
		t.Errorf("sent bubble should not have ✓✓: %s", out)
	}
}

func TestRender_Bubble_StatusDelivered(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{Self: true, From: "you", Text: "hi", At: time.Now(), Status: "delivered"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓✓") {
		t.Errorf("delivered bubble missing ✓✓: %s", out)
	}
	if strings.Contains(out, "✓✓✓") {
		t.Errorf("delivered bubble has triple check: %s", out)
	}
}

func TestRender_Bubble_StatusEmpty_NoMarker(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("bubble", bubbleData{Self: true, From: "you", Text: "hi", At: time.Now(), Status: ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "✓") {
		t.Errorf("empty-status bubble should not render ✓: %s", out)
	}
}

func TestRender_Sidebar_RendersContacts(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	out, err := r.Render("sidebar", sidebarData{
		Contacts: []sidebarContact{
			{Pubkey: "p1", Label: "Alice", Tier: contacts.TierFriend, LastSeen: &now},
			{Pubkey: "p2", Label: "Bob", Tier: contacts.TierMaster, LastSeen: nil},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "Bob") {
		t.Errorf("sidebar missing contacts: %s", out)
	}
}

func TestRender_MessagesTable_MalformedRowMarked(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("messages", messagesData{
		Rows: []messageRow{
			{Direction: "in", From: "Bob", Preview: "hello", At: time.Now()},
			{Direction: "in", From: "Carol", Preview: "raw", At: time.Now(), Malformed: true, RejectReason: "not_envelope"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "malformed") {
		t.Errorf("messages row missing malformed class: %s", out)
	}
}

func TestSortContactsForSidebar(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)
	in := []sidebarContact{
		{Label: "noseen-friend", Tier: contacts.TierFriend, LastSeen: nil},
		{Label: "recent", Tier: contacts.TierFriend, LastSeen: &now},
		{Label: "earlier", Tier: contacts.TierFriend, LastSeen: &earlier},
		{Label: "noseen-master", Tier: contacts.TierMaster, LastSeen: nil},
	}
	out := sortContactsForSidebar(in)
	if out[0].Label != "recent" || out[1].Label != "earlier" {
		t.Errorf("recent first then earlier; got %v", labels(out))
	}
	if out[2].Label != "noseen-master" {
		t.Errorf("expected master before friend among no-LastSeen; got %v", labels(out))
	}
}

func labels(cs []sidebarContact) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

func TestRender_RelaysStrip_PillVocabulary(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("relays", relaysData{Rows: []relayRow{
		{URL: "ws://relay.example.invalid", Role: "home", State: "connected", LastEventAgo: "0s"},
		{URL: "ws://fallback.example.invalid", Role: "fallback", State: "error", LastError: "tcp dial refused", LastEventAgo: "12m"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Strip frame: leading mono "Relays" label and the inner host the
	// htmx swap renders into.
	for _, want := range []string{
		`class="relay-strip-inner"`,
		`class="strip-label"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("strip frame missing %q in: %s", want, out)
		}
	}
	// Reuses the chip vocabulary from the Settings → Relays pane so the
	// two surfaces stay in step.
	for _, want := range []string{
		`class="role-pill is-home"`,
		`class="role-pill is-fallback"`,
		`class="state-pill is-connected"`,
		`class="state-pill is-error"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("strip missing chip class %q in: %s", want, out)
		}
	}
	// LastError is exposed via the state-pill title (hover tooltip),
	// not as a visible glyph — keeps the strip tidy when relays are
	// unhealthy.
	if !strings.Contains(out, `title="tcp dial refused"`) {
		t.Errorf("strip missing LastError tooltip on error row: %s", out)
	}
	// The confusing column headers from the old <table> must not leak
	// back into this surface; the strip is supposed to be self-
	// describing without a thead row.
	for _, banned := range []string{`<thead`, `<th>role</th>`, `<th>url</th>`, `<th>last event</th>`} {
		if strings.Contains(out, banned) {
			t.Errorf("strip should not contain table-header markup %q in: %s", banned, out)
		}
	}
}

func TestRender_RelaysStrip_EmptyState(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("relays", relaysData{Rows: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="strip-empty"`) {
		t.Errorf("empty strip missing .strip-empty marker: %s", out)
	}
	if !strings.Contains(out, "No relays subscribed yet.") {
		t.Errorf("empty strip missing human copy: %s", out)
	}
}
