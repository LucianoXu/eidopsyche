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
