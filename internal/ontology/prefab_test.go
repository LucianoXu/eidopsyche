package ontology

import (
	"archive/tar"
	"bytes"
	"io"
	"sort"
	"strings"
	"testing"
)

func TestList_SkipsUnderscorePrefixedDirs(t *testing.T) {
	metas, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "_") {
			t.Errorf("List returned underscore-prefixed prefab %q; should be hidden", m.ID)
		}
	}
}

func TestMetaFor_TestFixture(t *testing.T) {
	m, err := MetaFor("_test_fixture")
	if err != nil {
		t.Fatalf("MetaFor: %v", err)
	}
	if m.ID != "_test_fixture" {
		t.Errorf("ID = %q, want %q", m.ID, "_test_fixture")
	}
	if m.Kind != "spirit" {
		t.Errorf("Kind = %q, want %q", m.Kind, "spirit")
	}
	if m.Display["en"] != "Test Spirit" {
		t.Errorf("Display.en = %q, want %q", m.Display["en"], "Test Spirit")
	}
	if !strings.Contains(m.Preview["en"], "placeholder") {
		t.Errorf("Preview.en missing placeholder marker; got %q", m.Preview["en"])
	}
}

func TestMetaFor_NotFound(t *testing.T) {
	_, err := MetaFor("nonexistent-id")
	if err == nil {
		t.Errorf("expected error for nonexistent prefab, got nil")
	}
}

func TestTarStreamPrefab_FixtureRoundTrip(t *testing.T) {
	params := Params{
		Label:        "Lyra",
		OwnerNpub:    "npub1master",
		OwnerLabel:   "alice",
		MindFormNpub: "npub1mindform",
		HomeRelay:    "wss://relay.example",
		CreatedDate:  "2026-05-10",
	}
	var buf bytes.Buffer
	if err := TarStreamPrefab(&buf, "_test_fixture", params); err != nil {
		t.Fatalf("TarStreamPrefab: %v", err)
	}
	seen := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			seen[h.Name] = ""
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = string(body)
	}
	if _, ok := seen["prefab.toml"]; ok {
		t.Errorf("prefab.toml leaked into tar")
	}
	if _, ok := seen["self/identity.md.tpl"]; ok {
		t.Errorf("identity.md.tpl should have been rendered to identity.md")
	}
	id, ok := seen["self/identity.md"]
	if !ok {
		t.Fatalf("missing self/identity.md in tar; got %v", keysOf(seen))
	}
	for _, want := range []string{"Lyra", "alice", "npub1master", "npub1mindform", "wss://relay.example", "2026-05-10"} {
		if !strings.Contains(id, want) {
			t.Errorf("identity.md missing %q; got: %s", want, id)
		}
	}
	cw, ok := seen["essence/calling-words.md"]
	if !ok {
		t.Fatalf("missing essence/calling-words.md")
	}
	if !strings.Contains(cw, "Lyra") {
		t.Errorf("calling-words missing label substitution; got %q", cw)
	}
}

// TestTarStreamPrefab_JournalEntryProducesLiteralFile pins the
// contract documented in top-level CLAUDE.md: the wizard's rendered
// summoning book is delivered via params.JournalEntry as a literal
// file at journal/0000-summoning.md, regardless of which scaffold
// path produced the volume. Prior to this fix the prefab path
// silently dropped JournalEntry, leaving the supervisor's birth
// handler retrying forever for the missing summoning book.
func TestTarStreamPrefab_JournalEntryProducesLiteralFile(t *testing.T) {
	const body = "## seal book {{ literal }}\nfrom prefab fixture\n"
	var buf bytes.Buffer
	params := Params{
		Label:        "x",
		OwnerNpub:    "n",
		CreatedDate:  "d",
		OwnerLabel:   "ol",
		MindFormNpub: "mf",
		HomeRelay:    "hr",
		JournalEntry: body,
	}
	if err := TarStreamPrefab(&buf, "_test_fixture", params); err != nil {
		t.Fatalf("TarStreamPrefab: %v", err)
	}
	got := tarEntryBody(t, buf.Bytes(), "journal/0000-summoning.md")
	if got != body {
		t.Errorf("journal/0000-summoning.md = %q, want %q", got, body)
	}
}

// TestTarStreamPrefab_EmptyJournalEntryOmitsFile keeps the test
// fixture's prefab tree clean: when JournalEntry is empty (today's
// non-wizard paths), TarStreamPrefab should not write the file.
func TestTarStreamPrefab_EmptyJournalEntryOmitsFile(t *testing.T) {
	var buf bytes.Buffer
	if err := TarStreamPrefab(&buf, "_test_fixture", Params{
		Label: "x", OwnerNpub: "n", CreatedDate: "d",
		OwnerLabel: "ol", MindFormNpub: "mf", HomeRelay: "hr",
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	tr := tar.NewReader(&buf)
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
	if seen["journal/0000-summoning.md"] {
		t.Errorf("empty JournalEntry should not produce journal/0000-summoning.md")
	}
}

func TestTarStreamPrefab_UsesForwardSlashes(t *testing.T) {
	var buf bytes.Buffer
	if err := TarStreamPrefab(&buf, "_test_fixture", Params{
		Label: "x", OwnerNpub: "n", CreatedDate: "d",
		OwnerLabel: "ol", MindFormNpub: "mf", HomeRelay: "hr",
	}); err != nil {
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
			t.Errorf("tar entry %q contains backslash", h.Name)
		}
	}
}

func TestTarStreamPrefab_MissingPrefab(t *testing.T) {
	var buf bytes.Buffer
	err := TarStreamPrefab(&buf, "nonexistent", Params{Label: "x", OwnerNpub: "n", CreatedDate: "d"})
	if err == nil {
		t.Errorf("expected error for nonexistent prefab")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
