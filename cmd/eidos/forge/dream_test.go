package forge

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
)

func TestNewDreamCmdHasBeginAndEnd(t *testing.T) {
	cmd := newDreamCmd()
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	if !got["begin"] || !got["end"] {
		t.Errorf("dream missing begin/end: %v", got)
	}
}

func TestDreamBeginEcho_FirstEver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	out, err := dreamBeginEcho(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dreaming since") {
		t.Errorf("dreamBeginEcho msg: %q", out)
	}
	if !strings.Contains(out, "no prior dream") {
		t.Errorf("first-ever dream should say so: %q", out)
	}
}

func TestDreamBeginEcho_SubsequentShowsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Inject a prior finished dream.
	t0 := time.Now().Add(-10 * time.Hour)
	if err := dreamstate.End(path, t0, "prev", ""); err != nil {
		t.Fatal(err)
	}
	out, err := dreamBeginEcho(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "since last dream") {
		t.Errorf("subsequent dream should report gap: %q", out)
	}
}

func TestDreamEndEcho_RequiresNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if _, err := dreamEndEcho(path, "", ""); err == nil {
		t.Error("dreamEndEcho with empty note should fail")
	}
}

func TestDreamEndEcho_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if _, err := dreamBeginEcho(path, "intent"); err != nil {
		t.Fatal(err)
	}
	out, err := dreamEndEcho(path, "summary", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dream ended") {
		t.Errorf("dreamEndEcho msg: %q", out)
	}
	if !strings.Contains(out, "#1") {
		t.Errorf("expected dream count #1: %q", out)
	}
	st, _ := dreamstate.Read(path)
	if st.LastDreamNote != "summary" {
		t.Errorf("LastDreamNote = %q", st.LastDreamNote)
	}
}

func TestDreamEndEcho_WithoutBegin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	out, err := dreamEndEcho(path, "recovered", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no prior begin") {
		t.Errorf("expected 'no prior begin' note: %q", out)
	}
}
