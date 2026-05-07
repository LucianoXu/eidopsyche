package service

import (
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the platform-neutral plist generation and path
// resolution. They run on every CI host (the linux runner included) so we
// validate macOS support without needing macOS in CI. The launchctl-driven
// branches in launchd_darwin.go are exercised by the manual smoke test on
// an actual Mac.

func TestLaunchdPlistContent(t *testing.T) {
	got := launchdPlist(
		"eidos-gate-daemon",
		"/usr/local/bin/eidos",
		"/Users/alice/.eidos/gate",
		[]string{"gate", "daemon"},
	)

	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist`,
		`<key>Label</key>`,
		`<string>eidos-gate-daemon</string>`,
		`<key>ProgramArguments</key>`,
		`<string>/usr/local/bin/eidos</string>`,
		`<string>gate</string>`,
		`<string>daemon</string>`,
		`<key>RunAtLoad</key>`,
		`<true/>`,
		`<key>KeepAlive</key>`,
		`<key>SuccessfulExit</key>`,
		`<false/>`,
		`<key>EIDOS_GATE_HOME</key>`,
		`<string>/Users/alice/.eidos/gate</string>`,
		`<key>PATH</key>`,
		`/opt/homebrew/bin`, // ensures Apple-silicon Homebrew tools are reachable
		`<key>StandardOutPath</key>`,
		`<string>/Users/alice/.eidos/gate/logs/eidos-gate-daemon.log</string>`,
		`<key>StandardErrorPath</key>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q\nfull:\n%s", want, got)
		}
	}
}

func TestLaunchdPlistOmitsEnvWhenStateDirEmpty(t *testing.T) {
	got := launchdPlist("eidos-gate-relay", "/eidos", "", []string{"gate", "relay"})
	if strings.Contains(got, "EIDOS_GATE_HOME") {
		t.Errorf("empty StateDir should not produce EIDOS_GATE_HOME entry:\n%s", got)
	}
	// PATH is always set so launchd-spawned processes can find git/curl/sh.
	if !strings.Contains(got, "<key>PATH</key>") {
		t.Errorf("PATH must be set even without StateDir:\n%s", got)
	}
}

func TestLaunchdPlistEscapesXMLChars(t *testing.T) {
	got := launchdPlist(
		"label-with-amp&lt-and-quote\"",
		"/path/with spaces/eidos",
		`/state/with"quote`,
		[]string{"gate", "daemon", "--flag", "<value>"},
	)

	// The plist must not embed raw XML-significant characters in <string> bodies.
	for _, banned := range []string{
		"&lt-and-quote\"</string>", // literal " inside string body — must be escaped
		"<value></string>",         // literal < inside string body — must be escaped
	} {
		if strings.Contains(got, banned) {
			t.Errorf("unescaped %q in plist:\n%s", banned, got)
		}
	}
	// And the escaped forms should be present.
	for _, want := range []string{
		"&amp;",
		"&quot;",
		"&lt;value&gt;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing escaped sequence %q in plist:\n%s", want, got)
		}
	}
}

func TestLaunchdPlistDir(t *testing.T) {
	t.Setenv("HOME", "/Users/alice")

	got, err := launchdPlistDir(ScopeUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Users/alice/Library/LaunchAgents" {
		t.Errorf("user: got %q", got)
	}

	got, err = launchdPlistDir(ScopeSystem, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Library/LaunchDaemons" {
		t.Errorf("system: got %q", got)
	}

	got, err = launchdPlistDir(ScopeUser, "/tmp/override")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/override" {
		t.Errorf("override: got %q", got)
	}
}

func TestLaunchdLogPath(t *testing.T) {
	got := launchdLogPath("/state", "eidos-gate-daemon")
	want := filepath.Join("/state", "logs", "eidos-gate-daemon.log")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Empty state dir falls back to /tmp so unit content is still valid even
	// in degenerate Configs (we expect callers to set StateDir, but a missing
	// one must not produce a broken plist).
	got = launchdLogPath("", "eidos-gate-relay")
	if !strings.HasPrefix(got, "/tmp/") {
		t.Errorf("empty stateDir should fall back under /tmp; got %q", got)
	}
}
