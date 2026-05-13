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
	_ = f.Model
	_ = f.Effort
	_ = f.OntologyDir
}

func TestFromOntology_ParsesTOML(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	identityBody := `label = "alice"
mindgate_npub = "npub1self"
creator_npub = "npub1owner"
creator_label = "Bob"
created_date = "2026-05-12"
`
	if err := os.WriteFile(filepath.Join(dir, "self", "identity.toml"), []byte(identityBody), 0o644); err != nil {
		t.Fatalf("write identity.toml: %v", err)
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
	}
	if got != want {
		t.Errorf("FromOntology mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}
