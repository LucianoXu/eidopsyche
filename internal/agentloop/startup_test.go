//go:build !windows

package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
)

func TestDecideSessionMode_NoSessionMintsNew(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "session.json")
	dsPath := filepath.Join(tmp, "dream-state.json")

	mode, fresh, err := DecideSessionMode(sessPath, dsPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if mode.Kind != SessionNew || !fresh {
		t.Errorf("expected SessionNew + isFirstWake=true, got %+v fresh=%v", mode, fresh)
	}
}

func TestDecideSessionMode_DreamAfterSessionStartsFresh(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "session.json")
	dsPath := filepath.Join(tmp, "dream-state.json")

	if _, err := sessionstate.Mint(sessPath, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := dreamstate.End(dsPath, time.Unix(2000, 0), "ended", ""); err != nil {
		t.Fatal(err)
	}
	mode, fresh, err := DecideSessionMode(sessPath, dsPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if mode.Kind != SessionNew || !fresh {
		t.Errorf("expected SessionNew (dream-newer-than-session), got %+v", mode)
	}
}

func TestStaleDreamRecovery_EndsAnInterruptedDream(t *testing.T) {
	tmp := t.TempDir()
	dsPath := filepath.Join(tmp, "dream-state.json")

	if err := dreamstate.Begin(dsPath, time.Unix(1000, 0), "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := RecoverStaleDream(dsPath, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(dsPath)
	var st dreamstate.State
	_ = json.Unmarshal(body, &st)
	if st.CurrentlyDreaming {
		t.Errorf("stale-dream recovery should set CurrentlyDreaming=false, got %+v", st)
	}
	if st.LastDreamFinishedAt == 0 {
		t.Errorf("stale-dream recovery should set LastDreamFinishedAt, got 0")
	}
}

func TestStaleDreamRecovery_NoOpWhenNotDreaming(t *testing.T) {
	tmp := t.TempDir()
	dsPath := filepath.Join(tmp, "dream-state.json")
	// No prior Begin → file may not exist; Recover should be no-op.
	if err := RecoverStaleDream(dsPath, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dsPath); err == nil {
		t.Errorf("RecoverStaleDream should not create dream-state.json when no prior dream begin")
	}
}
