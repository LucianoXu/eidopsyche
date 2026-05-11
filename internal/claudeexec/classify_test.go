// internal/claudeexec/classify_test.go
package claudeexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// fakeExitErr returns a *exec.ExitError-equivalent that reports the given code.
func fakeExitErr(code int) *exec.ExitError {
	cmd := exec.Command("sh", "-c", "exit "+itoa(code))
	_ = cmd.Run() // populates ProcessState; we don't care about the actual exit
	return &exec.ExitError{ProcessState: cmd.ProcessState}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func TestClassifyClaudeExit(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		stderr   string
		want     ClaudeErrorKind
		wantHTTP int
	}{
		{"nil-err-is-OK", 0, "", ClaudeOK, 0},
		{"exit-47-auth", 47, "", ClaudeAuthRequired, 0},
		{"exit-41-auth", 41, "", ClaudeAuthRequired, 0},
		{"exit-1-401", 1, "API Error: 401 Invalid authentication credentials", ClaudeAuthRequired, 401},
		{"exit-1-please-run-login", 1, "Please run /login", ClaudeAuthRequired, 0},
		{"exit-1-oauth-invalid", 1, "OAuth token is invalid", ClaudeAuthRequired, 0},
		{"exit-1-429-rate", 1, "API Error: 429 rate limit reached", ClaudeRateLimit, 429},
		{"exit-1-500-server", 1, "API Error: 500 Internal", ClaudeServerError, 500},
		{"exit-1-502-server", 1, "API Error: 502 Bad Gateway", ClaudeServerError, 502},
		{"exit-1-503-server", 1, "API Error: 503 Service Unavailable", ClaudeServerError, 503},
		{"exit-1-400-bad-request", 1, "API Error: 400 invalid request", ClaudeBadRequest, 400},
		{"exit-1-ECONNREFUSED", 1, "dial tcp: ECONNREFUSED", ClaudeNetwork, 0},
		{"exit-1-io-timeout", 1, "i/o timeout", ClaudeNetwork, 0},
		{"exit-1-unexpected-EOF", 1, "unexpected EOF reading response", ClaudeNetwork, 0},
		{"exit-1-unknown", 1, "some bizarre new failure mode", ClaudeUnknown, 0},
		{"exit-2-unknown", 2, "", ClaudeUnknown, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				err error
				ps  *os.ProcessState
			)
			if tc.code != 0 {
				ee := fakeExitErr(tc.code)
				err = ee
				ps = ee.ProcessState
			}
			v := ClassifyClaudeExit(err, ps, []byte(tc.stderr))
			if v.Kind != tc.want {
				t.Errorf("Kind = %v, want %v", v.Kind, tc.want)
			}
			if tc.wantHTTP != 0 && v.HTTPStatus != tc.wantHTTP {
				t.Errorf("HTTPStatus = %d, want %d", v.HTTPStatus, tc.wantHTTP)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	cases := map[ClaudeErrorKind]string{
		ClaudeOK:           "ok",
		ClaudeAuthRequired: "auth",
		ClaudeRateLimit:    "rate",
		ClaudeServerError:  "server",
		ClaudeBadRequest:   "bad-request",
		ClaudeNetwork:      "network",
		ClaudeKilled:       "killed",
		ClaudeUnknown:      "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// Keep imports stable.
var _ = errors.New
