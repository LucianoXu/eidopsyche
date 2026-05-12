package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildStubClaude builds the testdata/stubclaude binary from
// internal/promptcapture so the in-container worker can exercise the
// full proxy → spawn → envelope flow without a real claude.
func buildStubClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "stubclaude")
	cmd := exec.Command("go", "build", "-o", out,
		"github.com/LucianoXu/eidopsyche/internal/promptcapture/testdata/stubclaude")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, combined)
	}
	return out
}

func TestRunPromptDumpInContainer_PopulatesCapturedFrom(t *testing.T) {
	stub := buildStubClaude(t)
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	if err := os.MkdirAll(filepath.Join(ontologyDir, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ontologyDir, "self", "identity.md"),
		[]byte("# Alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "config.toml"),
		[]byte(`[mindform]`+"\n"+`model = "sonnet"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin:      stub,
		OntologyRoot:   ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice",
		Prompt:         "ping",
		Bare:           false,
		Stdout:         &stdout,
		Stderr:         os.Stderr,
	})
	if err != nil {
		t.Fatalf("runPromptDumpInContainer: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout.String())
	}
	cf, _ := env["captured_from"].(map[string]any)
	if cf == nil {
		t.Fatalf("captured_from missing")
	}
	if cf["mindform"] != "alice" {
		t.Errorf("mindform=%v", cf["mindform"])
	}
	if cf["model"] != "sonnet" {
		t.Errorf("model=%v", cf["model"])
	}
	if cf["identity_path"] != "self/identity.md" {
		t.Errorf("identity_path=%v", cf["identity_path"])
	}
	if cf["bare"] != false {
		t.Errorf("bare=%v", cf["bare"])
	}
}

func TestRunPromptDumpInContainer_BareSkipsIdentity(t *testing.T) {
	stub := buildStubClaude(t)
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	_ = os.MkdirAll(filepath.Join(ontologyDir, "self"), 0o755)
	_ = os.MkdirAll(gateDir, 0o755)
	_ = os.WriteFile(filepath.Join(ontologyDir, "self", "identity.md"),
		[]byte("# Should not appear\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gateDir, "config.toml"), []byte(""), 0o644)

	var stdout bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin: stub, OntologyRoot: ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice", Bare: true, Stdout: &stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &env)
	cf, _ := env["captured_from"].(map[string]any)
	if cf["bare"] != true {
		t.Errorf("bare=%v", cf["bare"])
	}
	args, _ := env["claude_args"].([]any)
	for _, a := range args {
		if s, _ := a.(string); s == "--append-system-prompt" {
			t.Fatalf("claude_args contains --append-system-prompt under --bare")
		}
	}
}

func TestRunPromptDumpInContainer_MissingIdentityProceedsWithEmpty(t *testing.T) {
	stub := buildStubClaude(t)
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	_ = os.MkdirAll(ontologyDir, 0o755) // no self/ dir
	_ = os.MkdirAll(gateDir, 0o755)
	_ = os.WriteFile(filepath.Join(gateDir, "config.toml"), []byte(""), 0o644)

	var stdout, stderr bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin: stub, OntologyRoot: ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice", Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &env)
	cf, _ := env["captured_from"].(map[string]any)
	if cf["identity_path"] != "" {
		t.Errorf("identity_path should be empty, got %v", cf["identity_path"])
	}
	if !bytes.Contains(stderr.Bytes(), []byte("identity not found")) {
		t.Errorf("expected identity-not-found note on stderr, got: %s", stderr.String())
	}
}
