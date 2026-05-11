// internal/claudeexec/classify.go
//
// Package claudeexec classifies the exit conditions of the `claude` CLI so
// callers (the supervisor's wake handler, the birth handler) can react
// per-kind instead of treating every non-zero exit identically.
//
// The classifier replaces an earlier exit-code-only heuristic that matched
// only codes 47 and 41 — Claude Code now exits 1 on 401, so those checks
// missed real auth failures and the supervisor kept spawning claude every
// heartbeat. This package centralises recognition: a single
// ClassifyClaudeExit invocation per wake, parameterised by exit code and
// captured stderr, returns a typed verdict.
package claudeexec

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// ClaudeErrorKind tags the verdict from classifying a claude exit.
type ClaudeErrorKind int

const (
	// ClaudeOK — claude exited with code 0.
	ClaudeOK ClaudeErrorKind = iota
	// ClaudeAuthRequired — 401 / OAuth invalid / "Please run /login".
	ClaudeAuthRequired
	// ClaudeRateLimit — 429.
	ClaudeRateLimit
	// ClaudeServerError — 5xx.
	ClaudeServerError
	// ClaudeBadRequest — 400; likely an eidos bug (malformed args).
	ClaudeBadRequest
	// ClaudeNetwork — dial/timeout/EOF before any HTTP status.
	ClaudeNetwork
	// ClaudeKilled — terminated by signal (OOM, SIGTERM).
	ClaudeKilled
	// ClaudeUnknown — exit non-zero with no recognised stderr signature.
	ClaudeUnknown
)

// String returns a stable short tag for use in logs / wake-list rendering.
func (k ClaudeErrorKind) String() string {
	switch k {
	case ClaudeOK:
		return "ok"
	case ClaudeAuthRequired:
		return "auth"
	case ClaudeRateLimit:
		return "rate"
	case ClaudeServerError:
		return "server"
	case ClaudeBadRequest:
		return "bad-request"
	case ClaudeNetwork:
		return "network"
	case ClaudeKilled:
		return "killed"
	default:
		return "unknown"
	}
}

// ClaudeVerdict is the classifier's structured output. Snippet holds the
// first matching stderr line for logging; empty when ClaudeOK.
type ClaudeVerdict struct {
	Kind       ClaudeErrorKind
	HTTPStatus int
	Snippet    string
	Retryable  bool
}

// ClassifyClaudeExit inspects err + processstate + captured stderr and
// returns a verdict. err may be nil (success), an *exec.ExitError, or any
// other error type — only the first two yield non-OK kinds.
func ClassifyClaudeExit(err error, st *os.ProcessState, stderr []byte) ClaudeVerdict {
	if err == nil {
		return ClaudeVerdict{Kind: ClaudeOK}
	}

	// signal-terminated.
	if st != nil {
		if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return ClaudeVerdict{Kind: ClaudeKilled, Retryable: true,
				Snippet: "signal " + ws.Signal().String()}
		}
	}

	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ProcessState != nil {
		code = ee.ProcessState.ExitCode()
	} else if st != nil {
		code = st.ExitCode()
	}

	switch code {
	case 47, 41:
		return ClaudeVerdict{Kind: ClaudeAuthRequired}
	}

	s := string(stderr)
	switch {
	case containsAny(s, "401", "Please run /login", "OAuth token is invalid"):
		v := ClaudeVerdict{Kind: ClaudeAuthRequired,
			Snippet: firstMatchingLine(s, "401", "Please run /login", "OAuth")}
		if strings.Contains(s, "401") {
			v.HTTPStatus = 401
		}
		return v
	case containsAny(s, "429", "rate limit"):
		return ClaudeVerdict{Kind: ClaudeRateLimit, HTTPStatus: 429, Retryable: true,
			Snippet: firstMatchingLine(s, "429", "rate limit")}
	case containsAny(s, "500", "502", "503"):
		return ClaudeVerdict{Kind: ClaudeServerError, HTTPStatus: detectStatus(s),
			Retryable: true, Snippet: firstMatchingLine(s, "500", "502", "503")}
	case containsAny(s, "400", "invalid request"):
		return ClaudeVerdict{Kind: ClaudeBadRequest, HTTPStatus: 400,
			Snippet: firstMatchingLine(s, "400", "invalid request")}
	case containsAny(s, "ECONNREFUSED", "i/o timeout", "unexpected EOF", "dial tcp"):
		return ClaudeVerdict{Kind: ClaudeNetwork, Retryable: true,
			Snippet: firstMatchingLine(s, "ECONNREFUSED", "timeout", "EOF", "dial")}
	}
	return ClaudeVerdict{Kind: ClaudeUnknown, Retryable: true}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// firstMatchingLine returns the first stderr line containing any needle,
// capped at 200 bytes so memory-bounded callers don't spill long traces.
func firstMatchingLine(s string, needles ...string) string {
	for _, line := range strings.Split(s, "\n") {
		for _, n := range needles {
			if strings.Contains(line, n) {
				if len(line) > 200 {
					line = line[:200]
				}
				return line
			}
		}
	}
	return ""
}

// detectStatus picks the first 5xx code mentioned in stderr; falls back
// to 0 when no concrete code is present.
func detectStatus(s string) int {
	for _, code := range []int{500, 501, 502, 503, 504} {
		if strings.Contains(s, intStr(code)) {
			return code
		}
	}
	return 0
}

func intStr(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}
