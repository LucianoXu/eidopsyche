package forge

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/scheduler"
)

// statusDetailFixture redirects the package-level paths to test-temp
// locations and restores them on cleanup.
func statusDetailFixture(t *testing.T) (plansT, dreamT string) {
	t.Helper()
	prevPlans := plansDir
	prevDream := dreamStatePath
	plansT = t.TempDir()
	dreamT = filepath.Join(t.TempDir(), "dream-state.json")
	plansDir = plansT
	dreamStatePath = dreamT
	t.Cleanup(func() {
		plansDir = prevPlans
		dreamStatePath = prevDream
	})
	return plansT, dreamT
}

func TestStatusDetail_NoPlansNoDreams(t *testing.T) {
	statusDetailFixture(t)
	cmd := newStatusDetailCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "plans:   none") {
		t.Errorf("missing plans-none line: %q", out)
	}
	if !strings.Contains(out, "dreams:  none yet") {
		t.Errorf("missing dreams-none line: %q", out)
	}
}

func TestStatusDetail_WithPlansAndDream(t *testing.T) {
	plansT, dreamT := statusDetailFixture(t)

	now := time.Now()
	if _, err := scheduler.Add(plansT, now, "follow up on bob", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Add(plansT, now.Add(time.Second), "weekly review", now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := dreamstate.End(dreamT, now.Add(-3*time.Hour), "consolidated bob", ""); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusDetailCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 active") {
		t.Errorf("plans count missing: %q", out)
	}
	if !strings.Contains(out, "follow up on bob") {
		t.Errorf("plans hint missing (should be earliest): %q", out)
	}
	if !strings.Contains(out, "1 total") {
		t.Errorf("dreams count missing: %q", out)
	}
	if !strings.Contains(out, "consolidated bob") {
		t.Errorf("dreams note missing: %q", out)
	}
}

func TestStatusDetail_CurrentlyDreaming(t *testing.T) {
	_, dreamT := statusDetailFixture(t)
	if err := dreamstate.Begin(dreamT, time.Now(), "consolidating"); err != nil {
		t.Fatal(err)
	}
	cmd := newStatusDetailCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "currently dreaming") {
		t.Errorf("missing currently-dreaming line: %q", buf.String())
	}
}
