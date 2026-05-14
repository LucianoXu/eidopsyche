//go:build integration

package mcp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEidosMCP_EndToEnd_SetLabel_StateGet boots a real gate daemon on a temp
// socket, starts `eidos mcp` as a child via the SDK's stdio command transport,
// invokes eidos_set_label, then eidos_state_get('identity.label'), and asserts
// the label round-trips.
func TestEidosMCP_EndToEnd_SetLabel_StateGet(t *testing.T) {
	dir := t.TempDir()

	// 1. Build the eidos binary once for both daemon + MCP subprocess.
	bin := filepath.Join(t.TempDir(), "eidos")
	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/eidos")
	// Run from repo root (two levels up from internal/mcp).
	buildCmd.Dir = filepath.Join(mustFindRepoRoot(t), ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build eidos: %v\n%s", err, out)
	}

	// 2. Initialize identity.
	// `eidos gate init` requires --label and --home (ws:// URL).
	// We use a fake home relay — it doesn't need to be reachable for this test;
	// the daemon only needs the URL to be syntactically valid.
	initOut, err := exec.Command(bin,
		"gate", "init",
		"--state-dir", dir,
		"--label", "integration-test-init",
		"--home", "ws://localhost:17777",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("gate init: %v\n%s", err, initOut)
	}
	t.Logf("gate init:\n%s", initOut)

	// 3. Start the daemon.
	daemonCmd := exec.Command(bin, "gate", "daemon", "--state-dir", dir)
	daemonCmd.Stdout = os.Stderr
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Kill()
		_ = daemonCmd.Wait()
	})

	// 4. Wait for the socket to appear (max 8s).
	// Default socket filename is "sock" (config.Defaults().Daemon.Socket).
	socket := filepath.Join(dir, "sock")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fi, err := os.Stat(socket); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("daemon socket %s did not appear within 8s", socket)
	}
	t.Logf("daemon socket ready: %s", socket)

	// 5. Connect to `eidos mcp --state-dir <dir>` via the SDK stdio client.
	mcpCmd := exec.Command(bin, "mcp", "--state-dir", dir)
	transport := &mcp.CommandTransport{Command: mcpCmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	defer session.Close()

	// 6. Sanity-check the tool catalogue includes the two tools we need.
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	need := map[string]bool{"eidos_set_label": true, "eidos_state_get": true}
	for _, tool := range toolsResult.Tools {
		delete(need, tool.Name)
	}
	if len(need) > 0 {
		t.Fatalf("missing tools in catalogue: %v", need)
	}
	t.Logf("tool catalogue: %d tools registered", len(toolsResult.Tools))

	// 7. Call eidos_set_label.
	setRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "eidos_set_label",
		Arguments: map[string]any{"label": "integration-test"},
	})
	if err != nil {
		t.Fatalf("set_label call: %v", err)
	}
	if setRes.IsError {
		t.Fatalf("set_label returned IsError: %+v", setRes.Content)
	}
	t.Logf("set_label ok: %+v", setRes.Content)

	// 8. Call eidos_state_get('identity.label') and verify the label round-trips.
	getRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "eidos_state_get",
		Arguments: map[string]any{"path": "identity.label"},
	})
	if err != nil {
		t.Fatalf("state_get call: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("state_get returned IsError: %+v", getRes.Content)
	}

	// The SDK serialises the structured output as JSON text content.
	// We do a permissive substring check so we're not brittle to the exact
	// JSON envelope shape ({"value":"integration-test"} or similar).
	const wantLabel = "integration-test"
	var found bool
	for _, c := range getRes.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, wantLabel) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("state_get response did not contain %q; got: %+v", wantLabel, getRes.Content)
	}
	t.Logf("state_get round-trip ok: label=%q", wantLabel)
}

// mustFindRepoRoot walks up from the test's working directory until it finds
// the go.work file that marks the monorepo root.
func mustFindRepoRoot(t *testing.T) string {
	t.Helper()
	// exec.Command working directory defaults to the test's package directory.
	// Walk up until go.work is found.
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs .: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.work starting from %s", dir)
		}
		dir = parent
	}
}
