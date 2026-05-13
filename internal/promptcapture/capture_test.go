package promptcapture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func buildStubClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "stubclaude")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/stubclaude")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, combined)
	}
	return out
}

func TestRunCapturesEnvelopeViaStub(t *testing.T) {
	stub := buildStubClaude(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := Run(ctx, Opts{
		ClaudeBin:    stub,
		Cwd:          t.TempDir(),
		SystemPrompt: "test identity",
		Model:        "sonnet",
		Prompt:       "ping",
		CapturedFrom: CapturedFrom{
			Mindform:     "alice",
			OntologyRoot: "/eidos/ontology",
			Model:        "sonnet",
			IdentityPath: "self/identity.toml",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	req, _ := env["request"].(map[string]any)
	if req == nil {
		t.Fatalf("no request in envelope: %#v", env)
	}
	if req["model"] != "claude-stub" {
		t.Fatalf("request.model=%v", req["model"])
	}
	cf, _ := env["captured_from"].(map[string]any)
	if cf["mindform"] != "alice" {
		t.Fatalf("captured_from.mindform=%v", cf["mindform"])
	}
}

func TestRunReturnsTimeoutWhenStubPostsNothing(t *testing.T) {
	// Build a stub that just exits without POSTing.
	dir := t.TempDir()
	stub := filepath.Join(dir, "silentclaude")
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if out, err := exec.Command("go", "build", "-o", stub, src).CombinedOutput(); err != nil {
		t.Fatalf("build silent: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Run(ctx, Opts{
		ClaudeBin:   stub,
		Cwd:         t.TempDir(),
		Prompt:      "ping",
		NoPOSTAfter: 500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
