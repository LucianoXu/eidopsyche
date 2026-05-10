//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// Wake-message construction is now covered by
// internal/prompts/wake_test.go. This file keeps tests for the
// supervisor's session-mode decision, argv assembly, and context
// computation.

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
	got := buildClaudeArgs("identity-text", "wake-msg", cfgPath, false, SessionMode{})
	want := []string{
		"--append-system-prompt", "identity-text",
		"--dangerously-skip-permissions",
		"-p", "wake-msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_StreamJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("log_level = \"info\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildClaudeArgs("ident", "msg", cfgPath, true, SessionMode{})
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"-p", "msg",
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
	got := buildClaudeArgs("identity", "msg", cfgPath, false, SessionMode{})
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
	got := buildClaudeArgs("ident", "m", "/nonexistent/path/config.toml", false, SessionMode{})
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"-p", "m",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestBuildClaudeArgs_SessionNew(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	uuid := "00000000-1111-2222-3333-444444444444"
	got := buildClaudeArgs("ident", "msg", cfgPath, false, SessionMode{Kind: SessionNew, UUID: uuid})
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"--session-id", uuid,
		"-p", "msg",
	}
	if !slicesEqualStr(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestMatchSessionNotFound(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"Error: session not found\n", true},
		{"Could not find session abc-123\n", true},
		{"no such session\n", true},
		{"some other error\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := matchSessionNotFound(c.stderr); got != c.want {
			t.Errorf("matchSessionNotFound(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
}

func TestEncodeCWD(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/eidos/ontology", "-eidos-ontology"},
		{"abc123", "abc123"},
		{"/path/with-dash", "-path-with-dash"},
	}
	for _, c := range cases {
		if got := encodeCWD(c.in); got != c.want {
			t.Errorf("encodeCWD(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSessionJsonlPath(t *testing.T) {
	got := sessionJsonlPath("/eidos/ontology", "abc-uuid")
	want := "/eidos/ontology/.claude/projects/-eidos-ontology/abc-uuid.jsonl"
	if got != want {
		t.Errorf("sessionJsonlPath: got %q, want %q", got, want)
	}
	if got := sessionJsonlPath("", "abc-uuid"); got != "" {
		t.Errorf("sessionJsonlPath with empty ontology: got %q, want empty", got)
	}
}

func TestDecideSessionMode_NewWhenAbsent(t *testing.T) {
	mode, first := decideSessionMode(sessionstate.State{}, nil, dreamstate.State{})
	if !first || mode.Kind != SessionNew {
		t.Fatalf("absent state → NEW; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_NewWhenCorrupt(t *testing.T) {
	mode, first := decideSessionMode(sessionstate.State{}, errors.New("corrupt"), dreamstate.State{})
	if !first || mode.Kind != SessionNew {
		t.Fatalf("corrupt state → NEW; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_ResumeWhenSessionFresh(t *testing.T) {
	sess := sessionstate.State{SessionID: "u-1", SessionStartedAt: 200}
	ds := dreamstate.State{LastDreamFinishedAt: 100}
	mode, first := decideSessionMode(sess, nil, ds)
	if first || mode.Kind != SessionResume || mode.UUID != "u-1" {
		t.Fatalf("fresh-session+old-dream → RESUME; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_NewWhenDreamFinishedAfterSession(t *testing.T) {
	sess := sessionstate.State{SessionID: "u-1", SessionStartedAt: 100}
	ds := dreamstate.State{LastDreamFinishedAt: 200}
	mode, first := decideSessionMode(sess, nil, ds)
	if !first || mode.Kind != SessionNew {
		t.Fatalf("stale-session+new-dream → NEW; got %+v first=%v", mode, first)
	}
}

func TestBuildClaudeArgs_SessionResume(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	uuid := "00000000-1111-2222-3333-444444444444"
	got := buildClaudeArgs("ident", "msg", cfgPath, true, SessionMode{Kind: SessionResume, UUID: uuid})
	want := []string{
		"--append-system-prompt", "ident",
		"--dangerously-skip-permissions",
		"--resume", uuid,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"-p", "msg",
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
