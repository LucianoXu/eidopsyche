package agentloop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
)

// SpawnMode bundles a SessionMode kind with the UUID it operates on.
// Returned by DecideSessionMode; consumed by Run / SpawnClaude.
type SpawnMode struct {
	Kind SessionMode
	UUID string // empty for SessionNew; caller mints
}

// DecideSessionMode reads session.json + dream-state.json and decides
// whether the upcoming claude spawn should use --session-id (new
// session, isFirstWake=true) or --resume (continuing session,
// isFirstWake=false). The caller passes ontologyDir so the pre-flight
// existence check for the session jsonl file can run; empty
// ontologyDir skips the check.
//
// Mirrors the existing agent_runner.go:228-237 decideSessionMode but
// promoted into agentloop and extended with the on-disk jsonl
// pre-flight (handles operator-nuked CLAUDE_DIR cases).
func DecideSessionMode(sessionStatePath, dreamStatePath, ontologyDir string) (SpawnMode, bool, error) {
	sess, sessErr := sessionstate.Read(sessionStatePath)
	ds, _ := dreamstate.Read(dreamStatePath)

	switch {
	case sessErr != nil, sess.SessionID == "":
		return SpawnMode{Kind: SessionNew}, true, nil
	case ds.LastDreamFinishedAt > sess.SessionStartedAt:
		return SpawnMode{Kind: SessionNew}, true, nil
	}

	// Pre-flight: if the on-disk jsonl is gone, mint fresh.
	if ontologyDir != "" {
		jsonl := SessionJsonlPath(ontologyDir, sess.SessionID)
		if jsonl != "" {
			if _, statErr := os.Stat(jsonl); errors.Is(statErr, fs.ErrNotExist) {
				return SpawnMode{Kind: SessionNew}, true, nil
			}
		}
	}
	return SpawnMode{Kind: SessionResume, UUID: sess.SessionID}, false, nil
}

// RecoverStaleDream synthesizes a dreamstate.End if CurrentlyDreaming
// was left true at startup (typically: previous agent-loop crashed
// between dream begin and dream end). Idempotent — no-op when the
// file is absent or when CurrentlyDreaming is already false.
func RecoverStaleDream(path string, now time.Time) error {
	st, err := dreamstate.Read(path)
	if err != nil {
		return fmt.Errorf("read dream-state: %w", err)
	}
	if !st.CurrentlyDreaming {
		return nil
	}
	note := fmt.Sprintf("interrupted by crash at %s", now.UTC().Format(time.RFC3339))
	if err := dreamstate.End(path, now, note, ""); err != nil {
		return fmt.Errorf("synthesize dream end: %w", err)
	}
	return nil
}
