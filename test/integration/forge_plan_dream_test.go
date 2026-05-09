//go:build integration

package integration

import (
	"path/filepath"
	"testing"
)

// TestPlanFiresInContainer is a scaffolding stub for the future Docker
// harness that exercises the planner end-to-end against a real mind-form
// container with the bundled claude binary swapped for testdata/claude_stub.sh.
//
// Building that harness requires a tagged mind-form image with the stub
// mounted into /usr/local/bin/claude and a fresh state-dir for the host
// gate. We commit the scaffold so future work has a clear hook; the
// operator-runnable end-to-end is deploy-test/test_script_heartbeat_plans_dreams.sh.
func TestPlanFiresInContainer(t *testing.T) {
	stub, err := filepath.Abs("testdata/claude_stub.sh")
	if err != nil {
		t.Skipf("cannot resolve claude stub path: %v", err)
	}
	t.Logf("claude stub at %s (will be mounted into the test mindform image once the harness lands)", stub)
	t.Skip("Integration scaffold present; full Docker harness lands in a follow-up. " +
		"Use deploy-test/test_script_heartbeat_plans_dreams.sh for end-to-end coverage.")
}
