package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatCronExpression(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{1 * time.Minute, "*/1 * * * *"},
		{2 * time.Minute, "*/2 * * * *"},
		{5 * time.Minute, "*/5 * * * *"},
		{15 * time.Minute, "*/15 * * * *"},
		{30 * time.Minute, "*/30 * * * *"},
		{1 * time.Hour, "0 * * * *"},
		{2 * time.Hour, "0 */2 * * *"},
		{4 * time.Hour, "0 */4 * * *"},
		{12 * time.Hour, "0 */12 * * *"},
		{24 * time.Hour, "0 0 * * *"},
	}
	for _, c := range cases {
		got, err := HeartbeatCronExpression(c.in)
		if err != nil {
			t.Errorf("HeartbeatCronExpression(%s): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("HeartbeatCronExpression(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHeartbeatCronExpressionRejects(t *testing.T) {
	bad := []time.Duration{
		0,
		7 * time.Minute,
		90 * time.Minute,
		5 * time.Hour,
		25 * time.Hour,
		35 * time.Second,
	}
	for _, d := range bad {
		_, err := HeartbeatCronExpression(d)
		if err == nil {
			t.Errorf("HeartbeatCronExpression(%s): expected error, got nil", d)
			continue
		}
		if !strings.Contains(err.Error(), "supported") {
			t.Errorf("HeartbeatCronExpression(%s) error doesn't mention 'supported': %v", d, err)
		}
	}
}

func TestDefaultHeartbeatIntervalIs2h(t *testing.T) {
	if DefaultHeartbeatInterval != 2*time.Hour {
		t.Errorf("DefaultHeartbeatInterval = %s, want 2h", DefaultHeartbeatInterval)
	}
}

func TestValidateHeartbeatInterval(t *testing.T) {
	if err := ValidateHeartbeatInterval("4h"); err != nil {
		t.Errorf("ValidateHeartbeatInterval(4h): %v", err)
	}
	if err := ValidateHeartbeatInterval("90m"); err == nil {
		t.Error("ValidateHeartbeatInterval(90m) should fail")
	}
	if err := ValidateHeartbeatInterval("not-a-duration"); err == nil {
		t.Error("ValidateHeartbeatInterval(garbage) should fail")
	}
	if err := ValidateHeartbeatInterval(""); err != nil {
		t.Errorf("empty (= use default) should pass, got %v", err)
	}
}

func TestValidateQuietHours(t *testing.T) {
	cases := []struct {
		start, end string
		ok         bool
	}{
		{"", "", true},
		{"22:00", "06:00", true},
		{"08:00", "20:00", true},
		{"22:00", "", false},
		{"", "06:00", false},
		{"25:00", "06:00", false},
		{"22:00", "06:60", false},
		{"22-00", "06:00", false},
	}
	for _, c := range cases {
		err := ValidateQuietHours(c.start, c.end)
		if c.ok && err != nil {
			t.Errorf("ValidateQuietHours(%q,%q) unexpectedly errored: %v", c.start, c.end, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateQuietHours(%q,%q) should have errored", c.start, c.end)
		}
	}
}

func TestValidateTZ(t *testing.T) {
	if err := ValidateTZ(""); err != nil {
		t.Errorf("empty TZ should be ok, got %v", err)
	}
	if err := ValidateTZ("Asia/Shanghai"); err != nil {
		t.Errorf("valid IANA tz: %v", err)
	}
	if err := ValidateTZ("Atlantis/Lemuria"); err == nil {
		t.Error("bogus tz should fail")
	}
}

func TestInQuietHours(t *testing.T) {
	loc := time.UTC
	// same-day window 08:00..20:00
	if !InQuietHours(time.Date(2026, 5, 9, 12, 0, 0, 0, loc), "08:00", "20:00", loc) {
		t.Error("12:00 should be in 08:00..20:00")
	}
	if InQuietHours(time.Date(2026, 5, 9, 21, 0, 0, 0, loc), "08:00", "20:00", loc) {
		t.Error("21:00 should NOT be in 08:00..20:00")
	}
	// wrap-around 22:00..06:00
	if !InQuietHours(time.Date(2026, 5, 9, 23, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("23:00 should be in 22:00..06:00")
	}
	if !InQuietHours(time.Date(2026, 5, 9, 3, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("03:00 should be in 22:00..06:00")
	}
	if InQuietHours(time.Date(2026, 5, 9, 12, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("12:00 should NOT be in 22:00..06:00")
	}
	// unset → never quiet
	if InQuietHours(time.Now(), "", "", loc) {
		t.Error("unset hours should yield false")
	}
}

func TestValidateDreamMinInterval(t *testing.T) {
	if err := ValidateDreamMinInterval(""); err != nil {
		t.Errorf("empty: %v", err)
	}
	if err := ValidateDreamMinInterval("12h"); err != nil {
		t.Errorf("12h: %v", err)
	}
	if err := ValidateDreamMinInterval("30m"); err == nil {
		t.Error("30m should be rejected (< 1h)")
	}
	if err := ValidateDreamMinInterval("garbage"); err == nil {
		t.Error("garbage should be rejected")
	}
}

func TestValidateMindFormConfigRejectsBadInterval(t *testing.T) {
	cfg := Config{
		Heartbeat: HeartbeatConfig{Interval: "90m"},
	}
	if err := ValidateMindFormConfig(cfg); err == nil {
		t.Error("90m interval should be rejected")
	}
}

func TestValidateMindFormConfigRejectsHalfQuietHours(t *testing.T) {
	cfg := Config{
		MindForm: MindFormConfig{QuietStart: "22:00"},
	}
	if err := ValidateMindFormConfig(cfg); err == nil {
		t.Error("half-set quiet hours should be rejected")
	}
}

func TestValidateMindFormConfigOK(t *testing.T) {
	cfg := Config{
		Heartbeat: HeartbeatConfig{Interval: "4h"},
		MindForm: MindFormConfig{
			QuietStart:       "22:00",
			QuietEnd:         "06:00",
			TZ:               "Asia/Shanghai",
			DreamMinInterval: "12h",
		},
	}
	if err := ValidateMindFormConfig(cfg); err != nil {
		t.Errorf("ok config rejected: %v", err)
	}
}

func TestLoadHeartbeatAndQuietHours(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
log_level = "info"

[heartbeat]
interval = "30m"

[mindform]
model = "claude-sonnet-4-7"
quiet_start = "22:00"
quiet_end = "06:00"
tz = "Asia/Shanghai"
dream_min_interval = "8h"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Heartbeat.Interval != "30m" {
		t.Errorf("Heartbeat.Interval = %q", cfg.Heartbeat.Interval)
	}
	if cfg.MindForm.QuietStart != "22:00" || cfg.MindForm.QuietEnd != "06:00" {
		t.Errorf("Quiet = %q..%q", cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd)
	}
	if cfg.MindForm.TZ != "Asia/Shanghai" {
		t.Errorf("TZ = %q", cfg.MindForm.TZ)
	}
	if cfg.MindForm.DreamMinInterval != "8h" {
		t.Errorf("DreamMinInterval = %q", cfg.MindForm.DreamMinInterval)
	}
}
