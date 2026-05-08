package dashboard

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestRequireConfirm_Match accepts the exact phrase, including with
// surrounding whitespace stripped. requireConfirm runs server-side as
// defense-in-depth against a client that bypasses the modal.
func TestRequireConfirm_Match(t *testing.T) {
	form := url.Values{"confirm": {"  selene  "}}
	req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := requireConfirm(req, "selene"); err != nil {
		t.Fatalf("expected match after trim, got: %v", err)
	}
}

// TestRequireConfirm_Mismatch rejects a non-matching phrase. The error
// message is fed back into the dashboard's inline render so it must be
// non-empty and human-readable.
func TestRequireConfirm_Mismatch(t *testing.T) {
	form := url.Values{"confirm": {"wrong"}}
	req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	err := requireConfirm(req, "selene")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error should mention mismatch, got: %v", err)
	}
}

// TestRequireConfirm_Empty is a separate failure mode from a mismatch:
// the modal inputs are required, so an empty phrase reaching this helper
// signals a CSRF-like bypass attempt. The message reflects that.
func TestRequireConfirm_Empty(t *testing.T) {
	form := url.Values{"confirm": {""}}
	req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	err := requireConfirm(req, "selene")
	if err == nil {
		t.Fatal("expected empty-phrase error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error should mention required, got: %v", err)
	}
}

// TestConfirmModalRenders verifies the shared modal template can be
// rendered from the dashboard's renderer without panic and that the
// expected phrase + danger-button pattern appears. Used as a smoke
// for later phases that consume the same template.
func TestConfirmModalRenders(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("confirm_modal", confirmModalData{
		Action:         "/settings/contacts/abc/remove",
		Target:         "#row-abc",
		Swap:           "outerHTML",
		Title:          "Remove contact",
		Body:           "This is irreversible.",
		Warning:        "All chat history with this contact will remain in your inbox.",
		ExpectedPhrase: "Bob",
		ConfirmLabel:   "Remove",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`hx-post="/settings/contacts/abc/remove"`,
		`hx-target="#row-abc"`,
		`hx-swap="outerHTML"`,
		`Remove contact`,
		`Type <code>Bob</code>`,
		`data-expected="Bob"`,
		`button.danger`,
		`disabled`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("modal missing substring %q\nrendered:\n%s", want, out)
		}
	}
}
