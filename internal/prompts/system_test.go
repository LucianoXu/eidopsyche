package prompts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuild_RendersAllSections(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "# 我的纲领\n\nbe kind.\n")
	mustWrite(t, filepath.Join(dir, "self", "soul.md"), "I notice small things.\n")

	facts := IdentityFacts{
		Label:        "alice",
		MindFormNpub: "npub1self",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		CreatedDate:  "2026-05-12",
		Model:        "claude-opus-4-7",
		Effort:       "medium",
		OntologyDir:  "/eidos/ontology",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Identity facts surface through the Info block, not as inlined identity.md.
	for _, want := range []string{
		"Your label: alice",
		"Your npub: npub1self",
		"Your creator's label: Bob",
		"Your creator's npub: npub1owner",
		"Your model: claude-opus-4-7",
		"Your reasoning effort: medium",
		"Created on: 2026-05-12",
		"Working directory: /eidos/ontology",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing identity Info-block content %q", want)
		}
	}

	for _, want := range []string{
		"be kind.",
		"I notice small things.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing file-content %q", want)
		}
	}

	if !strings.Contains(out, "memory/notes/") {
		t.Error("memory contract should reference memory/notes/")
	}
	for _, banned := range []string{
		"Claude Code",
		"/<skill-name>",
		"/eidos/claude/.claude",
		"# auto memory",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("output must not contain %q (Claude-Code preamble leakage)", banned)
		}
	}
}

func TestBuild_MissingFilesRenderAsEmpty(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "principle 1\n")

	facts := IdentityFacts{
		Label: "test", OntologyDir: "/eidos/ontology",
		Model: "claude-opus-4-7", Effort: "medium",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build should not fail when soul missing: %v", err)
	}
	if !strings.Contains(out, "principle 1") {
		t.Error("CLAUDE.md content not rendered")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
