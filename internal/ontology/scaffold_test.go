package ontology

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
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

func TestTarStreamProducesAllEntries(t *testing.T) {
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = true
	}
	for _, want := range []string{"CLAUDE.md", "self/identity.md", "memory/mood.md", ".gitignore"} {
		if !seen[want] {
			t.Errorf("tar missing %q", want)
		}
	}
}

func TestTarStreamUsesForwardSlashes(t *testing.T) {
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(h.Name, "\\") {
			t.Errorf("tar entry %q contains backslash; tar names must use forward slashes", h.Name)
		}
	}
}

type errWriter struct{ failNow bool }

func (e *errWriter) Write(p []byte) (int, error) {
	if e.failNow {
		return 0, errors.New("simulated write failure")
	}
	return len(p), nil
}

func TestTarStreamPropagatesWriteError(t *testing.T) {
	err := TarStream(&errWriter{failNow: true}, Params{Label: "a", OwnerNpub: "n", CreatedDate: "d"})
	if err == nil {
		t.Errorf("expected error from failing writer, got nil")
	}
}
