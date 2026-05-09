//go:build windows

package service

import (
	"strings"
	"testing"
)

// TestBuildImagePathQuotesSpaces verifies that buildImagePath produces a
// command line Windows can parse back into our intended argv. The motivating
// case is `--state-dir "C:\Users\Yingte Xu\.eidos\gate"`: an unquoted space
// would split that path into two argv entries on service start.
func TestBuildImagePathQuotesSpaces(t *testing.T) {
	got := buildImagePath(`C:\Users\Yingte Xu\.local\bin\eidos.exe`, []string{
		"gate", "daemon", "--state-dir", `C:\Users\Yingte Xu\.eidos\gate`,
	})
	for _, want := range []string{
		`"C:\Users\Yingte Xu\.local\bin\eidos.exe"`,
		"gate",
		"daemon",
		"--state-dir",
		`"C:\Users\Yingte Xu\.eidos\gate"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildImagePath output missing %q\nfull: %s", want, got)
		}
	}
}

// TestInstallUnitsFiltersRelay locks in the parity contract with the
// systemd / launchd backends: WithRelay=false drops the relay unit from
// Install / Start, but Stop / Uninstall / Status still iterate units().
func TestInstallUnitsFiltersRelay(t *testing.T) {
	withRelay := &scm{cfg: Config{BinaryPath: "x", WithRelay: true}}
	if got := len(withRelay.installUnits()); got != 2 {
		t.Errorf("WithRelay=true: installUnits len = %d, want 2", got)
	}

	withoutRelay := &scm{cfg: Config{BinaryPath: "x", WithRelay: false}}
	if got := len(withoutRelay.installUnits()); got != 1 {
		t.Errorf("WithRelay=false: installUnits len = %d, want 1", got)
	}
	if name := withoutRelay.installUnits()[0].name; name != DaemonUnitName {
		t.Errorf("WithRelay=false: only unit must be daemon, got %s", name)
	}
	if got := len(withoutRelay.units()); got != 2 {
		t.Errorf("units() must always return both for cleanup paths; got %d", got)
	}
}

// TestUnitArgsCarryStateDir verifies the daemon's --state-dir flag is woven
// into the SCM ImagePath so that a service started by SCM finds the same
// state directory the user passed at install time.
func TestUnitArgsCarryStateDir(t *testing.T) {
	args := daemonArgs(`C:\Users\test\.eidos\gate`)
	if got, want := strings.Join(args, " "), `gate daemon --state-dir C:\Users\test\.eidos\gate`; got != want {
		t.Errorf("daemonArgs: got %q, want %q", got, want)
	}
	args = relayArgs(`C:\Users\test\.eidos\gate`)
	if got, want := strings.Join(args, " "), `gate relay --state-dir C:\Users\test\.eidos\gate`; got != want {
		t.Errorf("relayArgs: got %q, want %q", got, want)
	}
	// Empty state dir → no flag, so SCM falls back to the resolver default.
	if got := daemonArgs(""); strings.Contains(strings.Join(got, " "), "--state-dir") {
		t.Errorf("daemonArgs(\"\") should not include --state-dir; got %v", got)
	}
}
