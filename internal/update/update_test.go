package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkipReason_DevBuild(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config dir
	if got := SkipReason(BuildInfo{Version: "dev"}); got != "dev build" {
		t.Errorf("dev: got %q, want %q", got, "dev build")
	}
	if got := SkipReason(BuildInfo{Version: ""}); got != "dev build" {
		t.Errorf("empty: got %q, want %q", got, "dev build")
	}
}

func TestSkipReason_EnvVar(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "1")
	t.Setenv("CI", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got != "EIDOS_NO_UPDATE_CHECK" {
		t.Errorf("env=1: got %q", got)
	}

	// Falsy values do NOT skip.
	for _, v := range []string{"", "0", "false", "no", "off"} {
		t.Setenv("EIDOS_NO_UPDATE_CHECK", v)
		if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got == "EIDOS_NO_UPDATE_CHECK" {
			t.Errorf("env=%q should not skip via env", v)
		}
	}
}

func TestSkipReason_CI(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "true")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got != "CI" {
		t.Errorf("CI=true: got %q", got)
	}
}

func TestSkipReason_ConfigDisabled(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "eidos")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[update]\ncheck = false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got != "config" {
		t.Errorf("config disabled: got %q", got)
	}

	// check = true → no skip.
	cfg = "[update]\ncheck = true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got != "" {
		t.Errorf("config check=true: got %q, want empty", got)
	}
}

func TestSkipReason_ProductionAllowsCheck(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config dir
	if got := SkipReason(BuildInfo{Version: "v0.1.0"}); got != "" {
		t.Errorf("production: got %q, want empty", got)
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.1.0", "v0.1.1", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.2.0", "v0.1.0", false},
		{"0.1.0", "v0.2.0", true}, // current missing 'v' is normalized
		{"v0.1.0", "0.2.0", true}, // latest missing 'v' is normalized
		{"dev", "v0.1.0", false},  // dev sentinel never compares as older
		{"", "v0.1.0", false},     // empty never compares
		{"v0.1.0", "", false},     // empty latest never prompts
	}
	for _, tc := range cases {
		if got := isNewerVersion(tc.current, tc.latest); got != tc.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if c, err := loadCache(); err != nil || c != nil {
		t.Fatalf("missing file: got (%v, %v), want (nil, nil)", c, err)
	}

	want := &Cache{
		CheckedAt:      time.Date(2026, 5, 7, 3, 42, 11, 0, time.UTC),
		LatestVersion:  "v0.2.0",
		CurrentVersion: "v0.1.0",
		LastShownAt:    time.Date(2026, 5, 7, 4, 0, 0, 0, time.UTC),
	}
	if err := saveCache(want); err != nil {
		t.Fatalf("saveCache: %v", err)
	}
	got, err := loadCache()
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if got == nil {
		t.Fatal("loadCache returned nil after save")
	}
	if !got.CheckedAt.Equal(want.CheckedAt) ||
		got.LatestVersion != want.LatestVersion ||
		got.CurrentVersion != want.CurrentVersion ||
		!got.LastShownAt.Equal(want.LastShownAt) {
		t.Errorf("round-trip mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestMaybePrompt_FormatsExpectedLine(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed cache: no prior prompt shown, latest > current.
	if err := saveCache(&Cache{
		CheckedAt:     time.Now(),
		LatestVersion: "v0.2.0",
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	MaybePrompt(BuildInfo{Version: "v0.1.0"}, &buf, false)
	out := buf.String()
	for _, want := range []string{
		"A new release of eidos is available: v0.1.0 → v0.2.0",
		"To upgrade, run: eidos self-update",
		"https://github.com/LucianoXu/eidopsyche/releases/tag/v0.2.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestMaybePrompt_CooldownSuppressesSecondCall(t *testing.T) {
	t.Setenv("EIDOS_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if err := saveCache(&Cache{
		CheckedAt:     time.Now(),
		LatestVersion: "v0.2.0",
	}); err != nil {
		t.Fatal(err)
	}

	var first, second bytes.Buffer
	b := BuildInfo{Version: "v0.1.0"}
	MaybePrompt(b, &first, false)
	MaybePrompt(b, &second, false)

	if first.Len() == 0 {
		t.Fatal("first prompt should have written output")
	}
	if second.Len() != 0 {
		t.Errorf("second prompt should be silent due to cooldown; got %q", second.String())
	}

	// force=true bypasses cooldown.
	var forced bytes.Buffer
	MaybePrompt(b, &forced, true)
	if forced.Len() == 0 {
		t.Error("force=true should bypass cooldown")
	}
}

func TestMaybePrompt_SkipsOnDevBuild(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := saveCache(&Cache{
		CheckedAt:     time.Now(),
		LatestVersion: "v0.2.0",
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	MaybePrompt(BuildInfo{Version: "dev"}, &buf, true) // even with force=true
	if buf.Len() != 0 {
		t.Errorf("dev build should not prompt; got %q", buf.String())
	}
}
