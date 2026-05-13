package prompts

import (
	"reflect"
	"testing"
)

func TestParseFrontmatter_HappyPath(t *testing.T) {
	body := `---
label: alice
mindform_npub: npub1n990u
owner_npub: npub14vdmpl
owner_label: Bob
created_date: 2026-05-12
kind: f
prefab: ""
home_relay: wss://relay.example.com
---

I am alice. ...
`
	fm, rest, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"label":         "alice",
		"mindform_npub": "npub1n990u",
		"owner_npub":    "npub14vdmpl",
		"owner_label":   "Bob",
		"created_date":  "2026-05-12",
		"kind":          "f",
		"prefab":        "",
		"home_relay":    "wss://relay.example.com",
	}
	if !reflect.DeepEqual(fm, want) {
		t.Fatalf("frontmatter mismatch:\n got=%#v\nwant=%#v", fm, want)
	}
	if rest == "" {
		t.Fatalf("body should be non-empty")
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	body := "I am a body with no frontmatter.\n"
	fm, rest, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("should not error on body-only input: %v", err)
	}
	if len(fm) != 0 {
		t.Fatalf("expected empty frontmatter, got %#v", fm)
	}
	if rest != body {
		t.Fatalf("body should be returned unchanged")
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	body := `---
label: "alice with spaces"
note: 'single quoted'
---
body
`
	fm, _, err := ParseFrontmatter(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm["label"] != "alice with spaces" {
		t.Fatalf("double-quoted unwrap failed: %q", fm["label"])
	}
	if fm["note"] != "single quoted" {
		t.Fatalf("single-quoted unwrap failed: %q", fm["note"])
	}
}
