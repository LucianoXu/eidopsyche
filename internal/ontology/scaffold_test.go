package ontology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWritesAllTemplateFiles(t *testing.T) {
	dir := t.TempDir()
	params := Params{
		Label:       "alice",
		OwnerNpub:   "npub1ownertest",
		CreatedDate: "2026-05-09",
	}
	if err := Scaffold(dir, params); err != nil {
		t.Fatal(err)
	}
	required := []string{
		"CLAUDE.md",
		"self/identity.md",
		"self/values.md",
		"memory/mood.md",
		"memory/semantic/.gitkeep",
		"memory/procedural/.gitkeep",
		"memory/episodic/.gitkeep",
		"desk/README.md",
		"drawer/README.md",
		".claude/settings.json",
		".gitignore",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestScaffoldRendersIdentityTemplate(t *testing.T) {
	dir := t.TempDir()
	params := Params{
		Label:       "alice",
		OwnerNpub:   "npub1ownertest",
		CreatedDate: "2026-05-09",
	}
	if err := Scaffold(dir, params); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "self/identity.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"alice", "npub1ownertest", "2026-05-09"} {
		if !strings.Contains(got, want) {
			t.Errorf("identity.md missing %q; got: %s", want, got)
		}
	}
}

func TestScaffoldRefusesIfTargetNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "preexisting"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Scaffold(dir, Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"})
	if err == nil {
		t.Errorf("expected refusal on non-empty target")
	}
}
