// Package agentloop — exit helpers.
//
// cliExitErr and matchSessionNotFound are small utilities used by the main
// supervision loop in agentloop.go to handle classified claude exit events.
package agentloop

import (
	"fmt"
	"os"
	"strings"
)

// cliExitErr logs cause to stderr and exits the process with the given code.
// It is used for hard terminal conditions (e.g. ClaudeAuthRequired) where the
// supervisor must see a specific exit code rather than cobra's default of 1.
//
// Note: calling os.Exit directly is pragmatic here — the alternative is
// plumbing an exit-code back through cobra's PersistentPostRunE, which would
// require touching every caller in the command tree. If that becomes
// burdensome in the future, replace with a typed error that implements
// ExitCode() int, which cobra 1.x honours via its own os.Exit path.
func cliExitErr(code int, cause error) error {
	fmt.Fprintln(os.Stderr, cause.Error())
	os.Exit(code)
	return nil // unreachable; keeps the compiler happy
}

// matchSessionNotFound reports whether the captured stderr text contains a
// known marker indicating that the claude session being resumed is missing on
// the server side.
func matchSessionNotFound(captured string) bool {
	low := strings.ToLower(captured)
	for _, m := range []string{
		"session not found",
		"could not find session",
		"no such session",
	} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}
