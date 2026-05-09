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

// TestDaemonUnitArgs verifies daemon args carry --state-dir.
func TestDaemonUnitArgs(t *testing.T) {
	s := &scm{cfg: Config{BinaryPath: "x", StateDir: `C:\Users\test\.eidos\gate`}}
	u := s.daemonUnit()
	if got, want := strings.Join(u.args, " "), `gate daemon --state-dir C:\Users\test\.eidos\gate`; got != want {
		t.Errorf("daemonUnit args: got %q, want %q", got, want)
	}
	// Empty state dir → no flag.
	s2 := &scm{cfg: Config{BinaryPath: "x"}}
	u2 := s2.daemonUnit()
	if strings.Contains(strings.Join(u2.args, " "), "--state-dir") {
		t.Errorf("daemonUnit with empty StateDir should not include --state-dir; got %v", u2.args)
	}
}

// TestRelayUnitArgs verifies relay args use "relay start --dir" (not "gate relay").
func TestRelayUnitArgs(t *testing.T) {
	s := &scm{cfg: Config{BinaryPath: "x"}}
	u := s.relayUnit(`C:\Users\test\.eidos\relay`)
	if got, want := strings.Join(u.args, " "), `relay start --dir C:\Users\test\.eidos\relay`; got != want {
		t.Errorf("relayUnit args: got %q, want %q", got, want)
	}
	if u.name != RelayUnitName {
		t.Errorf("relayUnit name = %q, want %q", u.name, RelayUnitName)
	}
	// RelayUnitName must be "eidos-relay".
	if RelayUnitName != "eidos-relay" {
		t.Errorf("RelayUnitName = %q, want %q", RelayUnitName, "eidos-relay")
	}
}

// TestUnitsAlwaysReturnsBothForStatus confirms units() (used by Status) always
// returns both daemon and relay so residuals stay discoverable.
func TestUnitsAlwaysReturnsBothForStatus(t *testing.T) {
	s := &scm{cfg: Config{BinaryPath: "x"}}
	got := s.units()
	if len(got) != 2 {
		t.Fatalf("units() returned %d entries, want 2", len(got))
	}
	if got[0].name != DaemonUnitName {
		t.Errorf("units()[0].name = %q, want %q", got[0].name, DaemonUnitName)
	}
	if got[1].name != RelayUnitName {
		t.Errorf("units()[1].name = %q, want %q", got[1].name, RelayUnitName)
	}
}
