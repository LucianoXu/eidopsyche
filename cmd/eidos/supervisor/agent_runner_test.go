//go:build !windows

package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestBuildWakeMessage(t *testing.T) {
	got := buildWakeMessage(wakePromptInput{
		Reason:               "mindgate",
		Hint:                 "Alice sent: hello",
		InboxUnread:          1,
		SinceLastWakeSeconds: 60,
	})
	for _, want := range []string{"You have just woken", "mindgate", "Alice sent", "1 unread"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("wake message missing %q; got: %s", want, got)
		}
	}
}

func TestAcquireLockExcludes(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "agent.lock")
	first, err := acquireAgentLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := acquireAgentLock(lockPath); err == nil {
		t.Errorf("second acquire should fail (locked)")
	}
	_ = os.Remove(lockPath)
}

func TestBuildClaudeArgs_NoModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("log_level = \"info\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildClaudeArgs("identity-text", "wake-msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity-text",
		"--dangerously-skip-permissions",
		"-p", "wake-msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_WithModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := "[mindform]\nmodel = \"claude-sonnet-4-7\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildClaudeArgs("identity", "msg", cfgPath)
	want := []string{
		"--append-system-prompt", "identity",
		"--dangerously-skip-permissions",
		"--model", "claude-sonnet-4-7",
		"-p", "msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_MissingConfig(t *testing.T) {
	got := buildClaudeArgs("ident", "m", "/nonexistent/path/config.toml")
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"-p", "m",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func slicesEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestComputeContext_QuietHours(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{
			QuietStart: "22:00",
			QuietEnd:   "06:00",
			TZ:         "UTC",
		},
	}
	now := time.Date(2026, 5, 9, 23, 0, 0, 0, time.UTC)
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, dreamstate.State{}, now)
	if !ctx.MasterLikelyAsleep {
		t.Error("23:00 UTC should be in 22:00..06:00 UTC quiet window")
	}
}

func TestComputeContext_QuietHoursOutsideWindow(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{
			QuietStart: "22:00",
			QuietEnd:   "06:00",
			TZ:         "UTC",
		},
	}
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, dreamstate.State{}, now)
	if ctx.MasterLikelyAsleep {
		t.Error("12:00 UTC should NOT be in 22:00..06:00 UTC quiet window")
	}
}

func TestComputeContext_DreamEligibleWhenNeverDreamt(t *testing.T) {
	cfg := config.Config{}
	now := time.Now()
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, dreamstate.State{}, now)
	if !ctx.DreamEligible {
		t.Error("never dreamt → DreamEligible should be true")
	}
	if ctx.SinceLastDreamSeconds != 0 {
		t.Errorf("SinceLastDreamSeconds = %d", ctx.SinceLastDreamSeconds)
	}
}

func TestComputeContext_DreamNotEligibleRecently(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{DreamMinInterval: "12h"},
	}
	now := time.Now()
	ds := dreamstate.State{LastDreamFinishedAt: now.Add(-3 * time.Hour).Unix()}
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, ds, now)
	if ctx.DreamEligible {
		t.Error("3h since last dream + 12h floor → not eligible")
	}
	if ctx.SinceLastDreamSeconds < 3*3600-2 || ctx.SinceLastDreamSeconds > 3*3600+2 {
		t.Errorf("SinceLastDreamSeconds = %d", ctx.SinceLastDreamSeconds)
	}
}

func TestComputeContext_DreamEligibleAfterFloor(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{DreamMinInterval: "12h"},
	}
	now := time.Now()
	ds := dreamstate.State{LastDreamFinishedAt: now.Add(-13 * time.Hour).Unix()}
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, ds, now)
	if !ctx.DreamEligible {
		t.Error("13h since last dream > 12h floor → should be eligible")
	}
}

func TestComputeContext_PlanIDPropagated(t *testing.T) {
	now := time.Now()
	sig := wake.Signal{
		Reason: wake.ReasonPlanned,
		Context: wake.Context{
			PlanID: "20260509T123000Z-plan-7f2eab19",
		},
	}
	ctx := computeContext(sig, config.Config{}, dreamstate.State{}, now)
	if ctx.PlanID != "20260509T123000Z-plan-7f2eab19" {
		t.Errorf("PlanID = %q", ctx.PlanID)
	}
}

func TestBuildWakeMessage_DreamFields(t *testing.T) {
	got := buildWakeMessage(wakePromptInput{
		Reason:                "heartbeat",
		InboxUnread:           0,
		MasterLikelyAsleep:    true,
		QuietStart:            "22:00",
		QuietEnd:              "06:00",
		TZ:                    "Asia/Shanghai",
		SinceLastDreamSeconds: 30 * 3600,
		DreamEligible:         true,
		LastDreamNote:         "consolidated bob",
	})
	for _, want := range []string{"Master is likely asleep", "Asia/Shanghai", "30h since your last dream", "eligible to dream", "consolidated bob"} {
		if !strings.Contains(got, want) {
			t.Errorf("wake message missing %q; got: %s", want, got)
		}
	}
}

func TestBuildWakeMessage_PlannedWake(t *testing.T) {
	got := buildWakeMessage(wakePromptInput{
		Reason:      "planned",
		Hint:        "follow up on bob",
		InboxUnread: 0,
		PlanID:      "20260509T123000Z-plan-7f2eab19",
	})
	if !strings.Contains(got, "Planned wake") {
		t.Errorf("planned wake message missing marker: %s", got)
	}
	if !strings.Contains(got, "follow up on bob") {
		t.Errorf("planned wake message missing hint: %s", got)
	}
}
