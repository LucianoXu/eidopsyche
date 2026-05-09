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

// TestTarStream_JournalAndEssenceDirsExist locks in that journal/ and
// essence/ are carried by the tar stream so the new MindForm volume has
// the file-as-essence slots the First Contact wizard expects.
func TestTarStream_JournalAndEssenceDirsExist(t *testing.T) {
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	seen := tarEntryNames(t, buf.Bytes())
	for _, want := range []string{
		"journal/", "journal/.gitkeep",
		"essence/", "essence/.gitkeep",
	} {
		if !seen[want] {
			t.Errorf("tar missing %q (got: %v)", want, seen)
		}
	}
}

// TestTarStream_JournalEntryProducesLiteralFile verifies the wizard's
// summoning book lands in the tar bytes-for-bytes — `{{` literals must
// survive (they would otherwise be eaten by text/template).
func TestTarStream_JournalEntryProducesLiteralFile(t *testing.T) {
	const body = "# 召唤书\n\n签者：alice\n\nThis has {{.Literal}} that should NOT be expanded.\n"
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d", JournalEntry: body}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	got := tarEntryBody(t, buf.Bytes(), "journal/0000-summoning.md")
	if got != body {
		t.Errorf("entry body = %q, want %q", got, body)
	}
}

func TestTarStream_EmptyJournalEntryOmitsFile(t *testing.T) {
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	seen := tarEntryNames(t, buf.Bytes())
	if seen["journal/0000-summoning.md"] {
		t.Errorf("empty JournalEntry should not produce 0000-summoning.md")
	}
}

func TestTarStream_GitignoreCarriesEssenceSecret(t *testing.T) {
	params := Params{Label: "alice", OwnerNpub: "n", CreatedDate: "d"}
	var buf bytes.Buffer
	if err := TarStream(&buf, params); err != nil {
		t.Fatal(err)
	}
	got := tarEntryBody(t, buf.Bytes(), ".gitignore")
	if !strings.Contains(got, "essence/secret.md") {
		t.Errorf(".gitignore missing essence/secret.md; got: %q", got)
	}
}

func tarEntryNames(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(body))
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		out[h.Name] = true
	}
	return out
}

func tarEntryBody(t *testing.T, body []byte, name string) string {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(body))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if h.Name == name {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read %q: %v", name, err)
			}
			return string(b)
		}
	}
	t.Fatalf("tar entry %q not found", name)
	return ""
}
