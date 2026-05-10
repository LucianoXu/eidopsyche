//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

func TestRenderCrontabFromConfig_Default(t *testing.T) {
	cfg := config.Config{} // interval empty → default 4h
	got, err := renderCrontab(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderCrontabFromConfig_30m(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "30m"}}
	got, err := renderCrontab(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "*/30 * * * * ") {
		t.Errorf("expected */30 prefix, got %q", got)
	}
}

func TestRenderCrontabFromConfig_24h(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "24h"}}
	got, err := renderCrontab(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "0 0 * * * ") {
		t.Errorf("expected 24h pattern, got %q", got)
	}
}

func TestRenderCrontabFromConfig_BadIntervalFallsBack(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "90m"}}
	got, err := renderCrontab(cfg)
	if err == nil {
		t.Error("renderCrontab should return error on unsupported interval")
	}
	want := "0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if got != want {
		t.Errorf("fallback got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderCrontabFromConfig_GarbageFallsBack(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "not-a-duration"}}
	got, err := renderCrontab(cfg)
	if err == nil {
		t.Error("expected parse error")
	}
	want := "0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if got != want {
		t.Errorf("fallback got:\n%s\nwant:\n%s", got, want)
	}
}

func TestInstallCrontabWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crontabs", "eidos")
	if err := installCrontab(path, "*/5 * * * * /bin/true\n"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "*/5 * * * * /bin/true\n" {
		t.Errorf("body = %q", string(body))
	}
}
