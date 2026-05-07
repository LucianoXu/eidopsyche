package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// This file holds the platform-neutral parts of the macOS launchd manager:
// plist content rendering and on-disk path resolution. They are pure functions
// so the unit tests run on the linux CI host even though the launchctl
// integration lives behind //go:build darwin in launchd_darwin.go.

// launchdPlistDir returns the directory where a LaunchAgent / LaunchDaemon
// .plist for the given Scope belongs. An override is honoured (for tests).
func launchdPlistDir(scope Scope, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if scope == ScopeSystem {
		return "/Library/LaunchDaemons", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// launchdLogDir returns the directory where the unit's stdout/stderr files
// live. We park them under the gate state directory so `eidos gate purge`
// sweeps them away alongside everything else, and so a custom $EIDOS_GATE_HOME
// keeps logs co-located with the data they describe.
func launchdLogDir(stateDir string) string {
	if stateDir == "" {
		// Best-effort fallback; unit content will still be valid but the user
		// loses launchd output. The CLI never builds Configs without a state
		// dir in practice.
		return "/tmp"
	}
	return filepath.Join(stateDir, "logs")
}

// launchdLogPath returns the absolute log file path for a unit by name.
func launchdLogPath(stateDir, label string) string {
	return filepath.Join(launchdLogDir(stateDir), label+".log")
}

// launchdPlist renders the XML content for a single launchd job.
//
// We keep the dict small and explicit:
//
//   - RunAtLoad=true so `launchctl bootstrap` immediately starts the job
//     (mirrors systemd's WantedBy=default.target / `enable --now` semantics).
//   - KeepAlive limited to SuccessfulExit=false: launchd respawns only on
//     non-zero exit, so a clean SIGTERM from `launchctl stop` actually stops
//     the daemon (parallel to systemd Restart=on-failure).
//   - PATH is set explicitly because launchd starts with a near-empty PATH
//     and the daemon may invoke `git`, `curl`, etc. The path list mirrors
//     macOS Sonoma defaults plus /opt/homebrew for Apple-silicon Homebrew.
//   - StandardOutPath / StandardErrorPath both point to the same per-unit
//     file in <stateDir>/logs/. journalctl has no macOS equivalent so the
//     CLI advertises this path to users in the status output.
func launchdPlist(label, binaryPath, stateDir string, programArgs []string) string {
	logPath := launchdLogPath(stateDir, label)

	args := append([]string{binaryPath}, programArgs...)
	var argLines strings.Builder
	for _, a := range args {
		argLines.WriteString("    <string>")
		argLines.WriteString(plistEscape(a))
		argLines.WriteString("</string>\n")
	}

	envVars := ""
	if stateDir != "" {
		envVars += "    <key>EIDOS_GATE_HOME</key>\n    <string>" + plistEscape(stateDir) + "</string>\n"
	}
	envVars += "    <key>PATH</key>\n    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n"

	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + plistEscape(label) + `</string>
  <key>ProgramArguments</key>
  <array>
` + argLines.String() + `  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>EnvironmentVariables</key>
  <dict>
` + envVars + `  </dict>
  <key>StandardOutPath</key>
  <string>` + plistEscape(logPath) + `</string>
  <key>StandardErrorPath</key>
  <string>` + plistEscape(logPath) + `</string>
</dict>
</plist>
`
}

// plistEscape escapes the five XML-significant characters. Conservative: we
// don't trust the shape of paths or labels.
var plistEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&apos;",
)

func plistEscape(s string) string { return plistEscaper.Replace(s) }

// writeUnitIfChanged writes content to path only when it differs from the
// existing file. Avoids gratuitous mtime updates and unit reloads (systemd
// daemon-reload, launchd bootout/bootstrap).
func writeUnitIfChanged(path, content string) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// Sanity assertion: shared service constants don't violate launchd Label
// constraints (no whitespace, no wildcards). The init runs on every platform
// at import time so any future rename of the constants gets caught early.
func init() {
	for _, name := range []string{DaemonUnitName, RelayUnitName} {
		if strings.ContainsAny(name, " \t/?*") {
			panic(errors.New("service unit name contains a character launchd Labels reject: " + name))
		}
	}
}
