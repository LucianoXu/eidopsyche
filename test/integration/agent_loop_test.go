//go:build integration

package integration

import (
	"path/filepath"
	"testing"
)

// TestAgentLoopHappyPath is the integration scaffold for the always-on
// mind-form runtime. The full container-based harness exercises:
//
//   - Real mind-form container + real claude (subscription auth via
//     $EIDOS_TEST_CLAUDE_TOKEN, same harness as the existing birth
//     integration tests once that lands).
//   - Happy path: spawn container → fire wake → observe agent-state.json
//     transitions through busy → idle → assert transcript file written →
//     assert session.json unchanged.
//   - Multi-turn in one process: fire wake A → wait for result → fire
//     wake B → assert both turns visible in stream-json output with one
//     `system init` (single claude process), separate transcripts per
//     wake.
//   - Crash mid-turn: while claude is busy, `docker exec ... kill -9
//     <claude_pid>` → assert agent-loop detects, respawns with --resume,
//     the in-flight transcript file is recovered via
//     `transcript.Store.Recover` and tagged as interrupted, next wake
//     processed normally.
//   - Dream rotation: trigger `dream begin` + dream-work tool calls +
//     `dream end` via in-container `docker exec` → observe new
//     session.json UUID → observe new transcripts directory under the
//     new session.
//   - Dream while busy: fire dream-end IPC while claude is mid-turn on
//     an unrelated wake → assert agent-loop waits for that turn's
//     result before closing stdin (quiescence gate).
//   - Stale dream recovery: hand-edit dream-state.json to set
//     CurrentlyDreaming=true then restart agent-loop → assert startup
//     logs the recovery, dream-state ends, session is minted fresh.
//   - Mailbox auto-injection probe: dispatch a 5-second
//     Bash{run_in_background:true} from the mind-form via a scripted
//     prompt → wait → assert claude stdout shows a synthetic user-turn
//     carrying the bg-task completion (validates the empirical claim
//     in spec §2 about claude SDK mailbox auto-injection in stream-json
//     input mode).
//   - Burst wakes: enqueue 5 wakes in quick succession → observe 5
//     turns in transcript (claude SDK FIFO ordering), each correctly
//     attributed.
//   - Invocation-flag contract: `ps -ef` inside the container shows the
//     claude command line including --input-format stream-json,
//     --output-format stream-json, --verbose,
//     --include-partial-messages, and exactly one of --session-id /
//     --resume.
//
// Until the docker+claude harness lands (it requires a tagged mind-form
// image with stable claude binary plus a checkout of the subscription
// OAuth token), the agent-loop's end-to-end behavior is covered by the
// in-package unit tests at internal/agentloop/agentloop_test.go which
// use the stub claude binary from internal/agentloop/testfake/.
func TestAgentLoopHappyPath(t *testing.T) {
	stub, err := filepath.Abs("../../internal/agentloop/testfake/cmd/claudestub")
	if err != nil {
		t.Skipf("cannot resolve stub path: %v", err)
	}
	t.Logf("stub claude at %s (will be mounted into the test mindform image once the docker harness lands)", stub)
	t.Skip("Integration scaffold present; full Docker harness lands in a follow-up.")
}
