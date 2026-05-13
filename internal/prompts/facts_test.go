package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityFactsZeroValueIsRenderableInTemplate(t *testing.T) {
	var f IdentityFacts
	_ = f.Label
	_ = f.MindFormNpub
	_ = f.OwnerNpub
	_ = f.OwnerLabel
	_ = f.CreatedDate
	_ = f.Kind
	_ = f.PrefabID
	_ = f.HomeRelay
	_ = f.Model
	_ = f.OntologyDir
}

func TestFromOntology_ParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	identityBody := `---
label: alice
mindform_npub: npub1self
owner_npub: npub1owner
owner_label: Bob
created_date: 2026-05-12
kind: f
prefab: ""
home_relay: wss://relay.example.com
---

I am alice.
`
	if err := os.WriteFile(filepath.Join(dir, "self", "identity.md"), []byte(identityBody), 0o644); err != nil {
		t.Fatalf("write identity.md: %v", err)
	}

	got, err := FromOntology(dir)
	if err != nil {
		t.Fatalf("FromOntology: %v", err)
	}
	want := IdentityFacts{
		Label:        "alice",
		MindFormNpub: "npub1self",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		CreatedDate:  "2026-05-12",
		Kind:         "f",
		PrefabID:     "",
		HomeRelay:    "wss://relay.example.com",
	}
	if got != want {
		t.Errorf("FromOntology mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}
