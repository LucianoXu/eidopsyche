package config

import (
	"strings"
	"testing"
)

// Sanity tests for the keys registry. Most behaviour is exercised by
// cmd/eidos/gate/config_test.go (CLI surface) and by the dashboard
// handler tests; this file covers a handful of regressions that bit
// the dashboard rollout, namely the dashboard.listen loopback rule.

func TestKeyByPath_DashboardListen_Loopback(t *testing.T) {
	cfg := Defaults()
	k, ok := KeyByPath("dashboard.listen")
	if !ok {
		t.Fatal("dashboard.listen should be registered")
	}
	for _, v := range []string{"127.0.0.1:22893", "[::1]:22893", "localhost:22893"} {
		if err := k.Set(&cfg, v); err != nil {
			t.Errorf("loopback %s should be accepted, got: %v", v, err)
		}
	}
}

// TestKeyByPath_DashboardListen_NonLoopback rejects values that
// dashboard.Run would silently skip (non-loopback bind), so the
// operator never persists a config that disappears the dashboard
// after restart.
func TestKeyByPath_DashboardListen_NonLoopback(t *testing.T) {
	cfg := Defaults()
	k, _ := KeyByPath("dashboard.listen")
	for _, v := range []string{
		"0.0.0.0:22893",
		":22893",               // bare port = bind-all = non-loopback
		"157.180.52.174:22893", // a public IP
		"yingte.io:22893",      // a hostname that's not localhost
	} {
		err := k.Set(&cfg, v)
		if err == nil {
			t.Errorf("non-loopback %s should be rejected, but Set succeeded", v)
			continue
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Errorf("error for %s should mention loopback, got: %v", v, err)
		}
	}
}

func TestKeyByPath_DashboardListen_BadHostPort(t *testing.T) {
	cfg := Defaults()
	k, _ := KeyByPath("dashboard.listen")
	if err := k.Set(&cfg, "not-a-host-port"); err == nil {
		t.Error("malformed host:port should be rejected")
	}
}

func TestKeyByPath_DashboardListen_Empty(t *testing.T) {
	cfg := Defaults()
	k, _ := KeyByPath("dashboard.listen")
	err := k.Set(&cfg, "")
	if err == nil {
		t.Error("empty value should be rejected")
	}
	if !strings.Contains(err.Error(), "dashboard.enabled") {
		t.Errorf("empty error should redirect to dashboard.enabled toggle, got: %v", err)
	}
}
