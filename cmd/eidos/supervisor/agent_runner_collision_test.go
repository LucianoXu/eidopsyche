//go:build !windows

package supervisor

import (
	"os"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// TestRunWithTranscript_RetriesOnIDCollision exercises the same-second
// duplicate-ID guard: agent-runner observes EEXIST from Store.Open and
// retries with a `-dup-<pid>-<nanos>` suffix instead of silently
// disabling transcript capture for the entire wake.
func TestRunWithTranscript_RetriesOnIDCollision(t *testing.T) {
	trDir := streamFixture(t, `printf '%s\n' \
  '{"type":"system","subtype":"init"}' \
  '{"type":"result","is_error":false}'
exit 0`)

	// Pre-create a transcript file with the same id the test wake will use,
	// forcing Store.Open to hit EEXIST on the first attempt.
	store, _ := transcript.NewStore(trDir)
	f, err := store.Open("colliding")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	sig := wake.Signal{V: 1, ID: "colliding", Reason: wake.ReasonHeartBeat, TriggeredAt: 1}
	if err := runWithTranscript(sig, t.TempDir(), []string{"-p", "x"}, ""); err != nil {
		t.Fatalf("runWithTranscript: %v", err)
	}

	// The retry path should have produced a *different* transcript file
	// alongside the pre-existing one.
	idx, _ := store.ReadIndex()
	if len(idx.Wakes) == 0 {
		t.Fatal("retry produced no index entry; transcript capture was lost")
	}
	var retried bool
	for _, w := range idx.Wakes {
		if strings.HasPrefix(w.ID, "colliding-dup-") {
			retried = true
			if _, err := os.Stat(store.WakePath(w.ID)); err != nil {
				t.Errorf("retried transcript file missing: %v", err)
			}
		}
	}
	if !retried {
		t.Errorf("expected an entry with `colliding-dup-...` ID, got %+v", idx.Wakes)
	}
}
