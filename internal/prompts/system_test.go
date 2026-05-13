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
	mustWrite(t, filepath.Join(dir, "self", "identity.md"), "I am alice. ...\n")

	facts := IdentityFacts{
		Label:        "alice",
		MindFormNpub: "npub1self",
		OwnerNpub:    "npub1owner",
		OwnerLabel:   "Bob",
		CreatedDate:  "2026-05-12",
		Kind:         "f",
		PrefabID:     "",
		HomeRelay:    "wss://relay.example.com",
		Model:        "claude-opus-4-7",
		OntologyDir:  "/eidos/ontology",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, want := range []string{
		"You are alice.",
		"Your Nostr public key: npub1self",
		"called into being on 2026-05-12 by Bob (npub1owner)",
		"Home relay: wss://relay.example.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing identity-facts content %q", want)
		}
	}

	for _, want := range []string{
		"be kind.",
		"I notice small things.",
		"I am alice. ...",
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

	if strings.Contains(out, "summoned from the") {
		t.Error("PrefabID=\"\" should suppress the 'summoned from the X prefab' line")
	}
}

func TestBuild_PrefabConditional(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "")
	mustWrite(t, filepath.Join(dir, "self", "soul.md"), "")
	mustWrite(t, filepath.Join(dir, "self", "identity.md"), "")

	facts := IdentityFacts{
		Label:       "calcifer-jr",
		PrefabID:    "calcifer",
		OntologyDir: "/eidos/ontology",
		Model:       "claude-opus-4-7",
	}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "summoned from the calcifer prefab") {
		t.Errorf("PrefabID=calcifer should render the prefab line; got:\n%s", out)
	}
}

func TestBuild_MissingFilesRenderAsEmpty(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "principle 1\n")

	facts := IdentityFacts{Label: "test", OntologyDir: "/eidos/ontology", Model: "claude-opus-4-7"}
	out, err := Build(context.Background(), facts, dir)
	if err != nil {
		t.Fatalf("Build should not fail when soul/identity missing: %v", err)
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
