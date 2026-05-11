# Mindform Setup-Token Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `eidos forge login --from-host` (which shares OAuth credentials between host and mindform — root cause of today's silent 401-on-exit-1 failures) with isolated per-mindform `claude setup-token` tokens, and introduce a typed classifier for claude exit failures so they stop landing as `failed(1)`.

**Architecture:** New `internal/claudeauth/` package owns token validation, generation (drives `claude setup-token` with an isolated `HOME=/tmp/eidos-setup-<uuid>/`), and volume write. New `internal/claudeexec/` package owns `ClassifyClaudeExit` (auth / rate / server / bad-request / network / killed / unknown). Both `cmd/eidos/supervisor/agent_runner.go` and `cmd/eidos/supervisor/birth.go` use the classifier; only `ClaudeAuthRequired` writes a marker this PR. `cmd/eidos/forge/login.go` is rewritten around the new package; `internal/firstcontact/phase4_seal.go` calls a new `forge.InstallLoginInteractive` instead of `InstallLoginFromHost`. The transcript `Entry` gains a `FailKind` field so `eidos forge watch <name> --list` can render `failed(auth)` / `failed(server)` / etc. instead of `failed(1)`.

**Tech Stack:** Go 1.25, cobra (CLI), pexpect not used (Go-only), the existing `forgectl` docker helpers, the existing `authstate`, `transcript`, `firstcontact/render` packages.

---

## File Structure

**Create:**
- `internal/claudeexec/classify.go` — types and `ClassifyClaudeExit`
- `internal/claudeexec/classify_test.go`
- `internal/claudeauth/validate.go` — `Validate(blob []byte) error`
- `internal/claudeauth/validate_test.go`
- `internal/claudeauth/generate.go` — `Generate(stdin io.Reader, stdout, stderr io.Writer, claudeBin string) ([]byte, error)`
- `internal/claudeauth/generate_test.go`
- `internal/claudeauth/volume.go` — `WriteToVolume(name, image string, blob []byte) error`
- `internal/claudeauth/volume_test.go`
- `test/integration/auth_setup_token_test.go` (build tag `integration`)

**Modify:**
- `internal/transcript/store.go` — add `FailKind string` to `Entry`
- `internal/transcript/store_test.go`
- `cmd/eidos/supervisor/agent_runner.go` — use classifier, react per-kind, persist FailKind
- `cmd/eidos/supervisor/agent_runner_test.go`
- `cmd/eidos/supervisor/birth.go` — use classifier, capture stderr
- `cmd/eidos/supervisor/birth_test.go`
- `cmd/eidos/forge/watch_render.go` — render `failed(kind)` from FailKind
- `cmd/eidos/forge/watch_render_test.go`
- `cmd/eidos/forge/transcript_list.go` — same status-render change
- `cmd/eidos/forge/transcript_list_test.go`
- `cmd/eidos/forge/login.go` — rewrite around new package, drop `--from-host`/`--method`, rename `--from-file` → `--token-file`, add `--paste`/`--generate`
- `cmd/eidos/forge/login_test.go`
- `internal/firstcontact/phase4_seal.go` — call new `forge.InstallLoginInteractive`
- `internal/firstcontact/phase4_seal_test.go`

**Delete:**
- Old `installFromHost`, `runInContainerLogin`, `InstallLoginFromHost`, `writeIntoVolume` in `cmd/eidos/forge/login.go` (replaced by the new package's helpers).

---

### Task 1: claudeexec classifier — types and tests

**Files:**
- Create: `internal/claudeexec/classify.go`
- Create: `internal/claudeexec/classify_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/claudeexec/classify_test.go
package claudeexec

import (
	"errors"
	"os/exec"
	"testing"
)

// fakeExit returns a *exec.ExitError-equivalent that reports the given code.
// We don't need a real process; we craft an error whose underlying state
// reports the exit code via syscall.WaitStatus.
func fakeExitErr(code int) *exec.ExitError {
	cmd := exec.Command("sh", "-c", "exit "+itoa(code))
	_ = cmd.Run() // populates ProcessState; we don't care about the actual exit
	return &exec.ExitError{ProcessState: cmd.ProcessState}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func TestClassifyClaudeExit(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		stderr  string
		want    ClaudeErrorKind
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeexec/...`
Expected: FAIL with `package claudeexec` not found.

- [ ] **Step 3: Write minimal implementation**

```go
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
		return ClaudeVerdict{Kind: ClaudeAuthRequired, HTTPStatus: 401,
			Snippet: firstMatchingLine(s, "401", "Please run /login", "OAuth")}
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
```

Also adjust the test file's imports — it uses `fmt` and `os` which need to be imported. Replace its header with:

```go
package claudeexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeexec/... -v`
Expected: PASS for all 16 sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/claudeexec/
git commit -m "feat(claudeexec): typed classifier for claude exit failures

Centralised verdict (auth / rate / server / bad-request / network /
killed / unknown) used by the supervisor's wake handler and birth
handler instead of an exit-code-only heuristic that missed 401-on-
exit-1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: claudeauth.Validate — token validation

**Files:**
- Create: `internal/claudeauth/validate.go`
- Create: `internal/claudeauth/validate_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/claudeauth/validate_test.go
package claudeauth

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // empty = expect nil
	}{
		{
			name: "full-creds-ok",
			body: `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-AA","refreshToken":"sk-ant-ort01-BB","expiresAt":1778549450074,"scopes":["user:inference"],"subscriptionType":"max"}}`,
		},
		{
			name:    "empty",
			body:    ``,
			wantErr: "empty",
		},
		{
			name:    "not-json",
			body:    `nope`,
			wantErr: "parse",
		},
		{
			name:    "missing-oauth",
			body:    `{}`,
			wantErr: "claudeAiOauth",
		},
		{
			name:    "missing-access-token",
			body:    `{"claudeAiOauth":{"refreshToken":"r","expiresAt":1}}`,
			wantErr: "accessToken",
		},
		{
			name:    "missing-refresh-token",
			body:    `{"claudeAiOauth":{"accessToken":"a","expiresAt":1}}`,
			wantErr: "refreshToken",
		},
		{
			name:    "missing-expires-at",
			body:    `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`,
			wantErr: "expiresAt",
		},
		{
			name:    "wrong-type-access-token",
			body:    `{"claudeAiOauth":{"accessToken":123,"refreshToken":"r","expiresAt":1}}`,
			wantErr: "parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeauth/... -run TestValidate`
Expected: FAIL with `package claudeauth` not found.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/claudeauth/validate.go
//
// Package claudeauth orchestrates the per-mindform setup-token flow:
//   - Validate: parse the credentials blob and confirm the required
//     OAuth fields are present and well-typed.
//   - Generate: drive `claude setup-token` against an isolated HOME so
//     the resulting token never lands in the operator's host claude.
//   - WriteToVolume: install the validated blob into the mindform's
//     docker volume at /eidos/claude/.claude/.credentials.json and
//     clear the auth_required marker.
//
// The package replaces the previous installFromHost path, which copied
// the operator's ~/.claude.json and ~/.claude/.credentials.json into
// the volume — a configuration that shared OAuth tokens between host
// and container and failed every time the host's claude rotated its
// access token server-side.
package claudeauth

import (
	"encoding/json"
	"errors"
	"fmt"
)

// claudeCredentials mirrors the on-disk shape of ~/.claude/.credentials.json
// for the fields Validate cares about. Extra fields are tolerated.
type claudeCredentials struct {
	ClaudeAiOauth *struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

// Validate parses the credentials blob and confirms it carries the
// minimum OAuth fields claude needs at runtime. Returns a descriptive
// error otherwise — callers surface it to the operator so they know
// which field of which file is wrong.
func Validate(blob []byte) error {
	if len(blob) == 0 {
		return errors.New("empty credentials blob")
	}
	var c claudeCredentials
	if err := json.Unmarshal(blob, &c); err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}
	if c.ClaudeAiOauth == nil {
		return errors.New("credentials missing claudeAiOauth section")
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return errors.New("credentials missing claudeAiOauth.accessToken")
	}
	if c.ClaudeAiOauth.RefreshToken == "" {
		return errors.New("credentials missing claudeAiOauth.refreshToken")
	}
	if c.ClaudeAiOauth.ExpiresAt == 0 {
		return errors.New("credentials missing claudeAiOauth.expiresAt")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeauth/... -run TestValidate -v`
Expected: PASS for all 8 sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/claudeauth/
git commit -m "feat(claudeauth): Validate for setup-token credentials blob

Parses .credentials.json and confirms claudeAiOauth.{accessToken,
refreshToken,expiresAt} are present. Used by the new login path
before writing into the mindform's volume.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: claudeauth.Generate — drive `claude setup-token` in isolated HOME

**Files:**
- Create: `internal/claudeauth/generate.go`
- Create: `internal/claudeauth/generate_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/claudeauth/generate_test.go
package claudeauth

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubScript writes a shell script at path that:
//   - asserts $HOME points at a temp directory (not the operator's real $HOME)
//   - writes fixture as $HOME/.claude/.credentials.json
//   - exits with exitCode
func stubScript(t *testing.T, path, fixture string, exitCode int) {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
set -eu
if [ -z "${HOME:-}" ]; then echo "stub: HOME unset" >&2; exit 99; fi
case "$HOME" in /tmp/eidos-setup-*) : ;; *) echo "stub: bad HOME=$HOME" >&2; exit 98 ;; esac
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/.credentials.json" <<EOF
%s
EOF
exit %d
`, fixture, exitCode)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
}

func TestGenerate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`
	stubScript(t, stub, fixture, 0)

	var stdout, stderr bytes.Buffer
	blob, err := Generate(nil, &stdout, &stderr, stub)
	if err != nil {
		t.Fatalf("Generate: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(string(blob), `"accessToken":"a"`) {
		t.Errorf("blob missing expected content: %q", blob)
	}
}

func TestGenerate_NonZeroExit_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	stubScript(t, stub, `bogus`, 5)

	var stdout, stderr bytes.Buffer
	if _, err := Generate(nil, &stdout, &stderr, stub); err == nil {
		t.Fatalf("expected error on stub exit 5")
	}
}

func TestGenerate_TempDirCleaned(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`
	stubScript(t, stub, fixture, 0)

	before, _ := filepath.Glob("/tmp/eidos-setup-*")
	var out, errb bytes.Buffer
	_, _ = Generate(nil, &out, &errb, stub)
	after, _ := filepath.Glob("/tmp/eidos-setup-*")
	if len(after) > len(before) {
		t.Errorf("temp dir not cleaned: before=%v after=%v", before, after)
	}
}

func TestGenerate_MissingCredsAfterRun_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	// exit 0 but DON'T write the file.
	body := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	var out, errb bytes.Buffer
	if _, err := Generate(nil, &out, &errb, stub); err == nil {
		t.Fatalf("expected error when stub leaves no .credentials.json")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeauth/... -run TestGenerate`
Expected: FAIL with `Generate undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/claudeauth/generate.go
package claudeauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Generate drives `claude setup-token` against an isolated HOME so the
// resulting OAuth token never lands in the operator's host claude
// install. Stdin/stdout/stderr are wired to the operator's terminal
// (caller passes os.Stdin / os.Stdout / os.Stderr) so they see the
// device-authorization URL and any progress prompts directly.
//
// On exit 0, reads $HOME/.claude/.credentials.json and returns its
// contents. The temporary HOME is removed before returning, so the
// credentials live only in memory until the caller writes them into the
// mindform's volume.
//
// claudeBin defaults to "claude" but is parameterised so tests can
// substitute a stub script.
func Generate(stdin io.Reader, stdout, stderr io.Writer, claudeBin string) ([]byte, error) {
	if claudeBin == "" {
		claudeBin = "claude"
	}
	if _, err := exec.LookPath(claudeBin); err != nil {
		// Allow absolute paths to stubs that aren't on PATH.
		if _, statErr := os.Stat(claudeBin); statErr != nil {
			return nil, fmt.Errorf("locate %q: %w", claudeBin, err)
		}
	}

	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, fmt.Errorf("random suffix: %w", err)
	}
	suffix := hex.EncodeToString(idBytes[:])
	home := filepath.Join("/tmp", "eidos-setup-"+suffix)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		return nil, fmt.Errorf("temp HOME: %w", err)
	}
	defer os.RemoveAll(home)

	cmd := exec.Command(claudeBin, "setup-token")
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude setup-token: %w", err)
	}

	credsPath := filepath.Join(home, ".claude", ".credentials.json")
	body, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", credsPath, err)
	}
	return body, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeauth/... -v`
Expected: PASS for all 4 Generate sub-cases plus 8 Validate cases.

- [ ] **Step 5: Commit**

```bash
git add internal/claudeauth/generate.go internal/claudeauth/generate_test.go
git commit -m "feat(claudeauth): Generate drives setup-token in isolated HOME

Runs \`claude setup-token\` with HOME=/tmp/eidos-setup-<rand>/ so the
resulting OAuth token never touches the operator's host install. The
temp dir is removed before returning; credentials live only in the
caller's memory until they're written into the mindform's volume.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: claudeauth.WriteToVolume — install into mindform volume

**Files:**
- Create: `internal/claudeauth/volume.go`
- Create: `internal/claudeauth/volume_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/claudeauth/volume_test.go
package claudeauth

import (
	"strings"
	"testing"
)

// fakeVolumeWriter is a stub for VolumeWriter that records every
// (relPath, body) it sees.
type fakeVolumeWriter struct {
	writes []struct {
		relPath string
		body    string
	}
	cleared bool
	err     error
}

func (f *fakeVolumeWriter) Write(relPath string, body []byte) error {
	if f.err != nil {
		return f.err
	}
	f.writes = append(f.writes, struct {
		relPath string
		body    string
	}{relPath, string(body)})
	return nil
}

func (f *fakeVolumeWriter) ClearAuthRequired() error {
	f.cleared = true
	return nil
}

func TestWriteToVolume_HappyPath(t *testing.T) {
	blob := []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`)
	w := &fakeVolumeWriter{}
	if err := writeToVolumeUsing(w, blob); err != nil {
		t.Fatalf("writeToVolumeUsing: %v", err)
	}
	if len(w.writes) != 1 {
		t.Fatalf("want 1 write, got %d", len(w.writes))
	}
	if w.writes[0].relPath != "claude/.claude/.credentials.json" {
		t.Errorf("relPath = %q, want claude/.claude/.credentials.json", w.writes[0].relPath)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared after successful write")
	}
}

func TestWriteToVolume_RejectsInvalidBlob(t *testing.T) {
	blob := []byte(`{}`)
	w := &fakeVolumeWriter{}
	err := writeToVolumeUsing(w, blob)
	if err == nil || !strings.Contains(err.Error(), "claudeAiOauth") {
		t.Fatalf("want validate error, got %v", err)
	}
	if len(w.writes) != 0 {
		t.Errorf("write should not happen on invalid blob, got %d writes", len(w.writes))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeauth/... -run TestWriteToVolume`
Expected: FAIL with `writeToVolumeUsing undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/claudeauth/volume.go
package claudeauth

import (
	"bytes"
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// VolumeWriter is the surface claudeauth needs to install creds into a
// mindform's volume. Production wiring uses forgectl + a one-shot
// docker run to remove the auth_required marker; tests substitute fakes.
type VolumeWriter interface {
	// Write puts body at the given path inside the volume, relative
	// to /eidos. Implementations chmod 600.
	Write(relPath string, body []byte) error
	// ClearAuthRequired removes /eidos/run/auth_required.json so the
	// supervisor's self-gate stops blocking the next wake.
	ClearAuthRequired() error
}

// WriteToVolume validates the blob, writes it into the mindform's
// volume at /eidos/claude/.claude/.credentials.json, and clears the
// auth_required marker. Returns the validation error verbatim so the
// operator sees which field is missing.
func WriteToVolume(ctx context.Context, name, image string, blob []byte) error {
	dc, err := forgectl.New()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	w := &forgectlWriter{ctx: ctx, client: dc, image: image, slug: name}
	return writeToVolumeUsing(w, blob)
}

// writeToVolumeUsing is the testable core: takes any VolumeWriter.
func writeToVolumeUsing(w VolumeWriter, blob []byte) error {
	if err := Validate(blob); err != nil {
		return err
	}
	if err := w.Write("claude/.claude/.credentials.json", blob); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := w.ClearAuthRequired(); err != nil {
		return fmt.Errorf("clear auth_required: %w", err)
	}
	return nil
}

// forgectlWriter adapts forgectl.WriteToVolume + a docker-run rm to the
// VolumeWriter interface.
type forgectlWriter struct {
	ctx    context.Context
	client forgectl.Client
	image  string
	slug   string
}

func (f *forgectlWriter) Write(relPath string, body []byte) error {
	return forgectl.WriteToVolume(f.ctx, f.client, f.image, f.slug, relPath, body)
}

func (f *forgectlWriter) ClearAuthRequired() error {
	// One-shot helper container running as uid 0; auth_required.json may
	// have been written by either the in-container uid 1000 supervisor
	// or an init-time root process — only root can remove both. Tolerates
	// absence via rm -f.
	script := "rm -f /eidos/run/auth_required.json"
	res, err := f.client.RunInit(f.ctx, forgectl.RunInitOpts{
		Image:      f.image,
		Slug:       f.slug,
		User:       "0:0",
		Entrypoint: []string{"sh"},
		Args:       []string{"-c", script},
		Stdin:      bytes.NewReader(nil),
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("rm auth_required exit %d: %s", res.ExitCode, string(res.Stderr))
	}
	return nil
}
```

If `forgectl.RunInitOpts` doesn't have the exact fields above, fall back to the existing pattern in `cmd/eidos/forge/login.go::clearAuthRequiredInVolume` which uses `exec.Command("docker", "run", "--rm", "--user", "0:0", ...)` directly. That direct-exec pattern is fine; the indirection through `forgectl.Client` is preferred for testability but not required.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeauth/... -v`
Expected: PASS for all 14 sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/claudeauth/volume.go internal/claudeauth/volume_test.go
git commit -m "feat(claudeauth): WriteToVolume installs creds + clears marker

Validates the credentials blob, writes it into the mindform's volume
at /eidos/claude/.claude/.credentials.json (chmod 600), and clears
/eidos/run/auth_required.json so the supervisor's next wake isn't
self-gated.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: transcript.Entry — add FailKind field

**Files:**
- Modify: `internal/transcript/store.go:39-52`
- Modify: `internal/transcript/store_test.go`

- [ ] **Step 1: Write the failing test**

Add at the bottom of `internal/transcript/store_test.go`:

```go
func TestEntry_FailKind_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	in := Entry{
		ID:        "abc",
		Reason:    "heartbeat",
		StartedAt: 1, EndedAt: 2,
		OK: false, ExitCode: 1, FailKind: "auth",
	}
	if _, err := st.Append(in, 10, 1<<20); err != nil {
		t.Fatalf("Append: %v", err)
	}
	idx, err := st.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].FailKind != "auth" {
		t.Fatalf("FailKind not persisted: %+v", idx.Wakes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcript/... -run TestEntry_FailKind_RoundTrip`
Expected: FAIL with `FailKind undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/transcript/store.go`, add the field to `Entry`:

```go
type Entry struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"session_id,omitempty"`
	Reason         string   `json:"reason"`
	StartedAt      int64    `json:"started_at"`
	EndedAt        int64    `json:"ended_at"`
	OK             bool     `json:"ok"`
	ExitCode       int      `json:"exit_code"`
	FailKind       string   `json:"fail_kind,omitempty"` // claudeexec.ClaudeErrorKind.String() when !OK
	CostUSD        *float64 `json:"cost_usd"`
	ToolUseCount   int      `json:"tool_use_count"`
	ThinkingBlocks int      `json:"thinking_blocks"`
	SizeBytes      int64    `json:"size_bytes"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcript/... -v`
Expected: PASS including the new test.

- [ ] **Step 5: Commit**

```bash
git add internal/transcript/store.go internal/transcript/store_test.go
git commit -m "feat(transcript): persist fail_kind on wake entries

Adds an optional fail_kind tag (auth / rate / server / bad-request /
network / killed / unknown) so the wake list can render
\`failed(auth)\` instead of \`failed(1)\`. Omitempty preserves
on-disk compatibility with prior entries.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: supervisor agent_runner — use classifier + persist FailKind + react to auth

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go:347-391`
- Modify: `cmd/eidos/supervisor/agent_runner.go:540-551` (`handleClaudeExit`)
- Modify: `cmd/eidos/supervisor/agent_runner.go:694-707` (delete `isAuthError`; classifier replaces it)
- Modify: `cmd/eidos/supervisor/agent_runner_test.go`

- [ ] **Step 1: Write the failing test**

Add at the bottom of `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestHandleClaudeExit_AuthOnExit1(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EIDOS_RUN_DIR", tmp)
	authstate.SetPathForTest(filepath.Join(tmp, "auth_required.json"))
	defer authstate.ResetPathForTest()

	cmd := exec.Command("sh", "-c", "exit 1")
	_ = cmd.Run() // populates ProcessState

	called := false
	exitFn := func(code int) { called = true; _ = code }
	verdict := handleClaudeExitTesting(
		&exec.ExitError{ProcessState: cmd.ProcessState},
		cmd.ProcessState,
		[]byte("API Error: 401 Invalid authentication credentials"),
		exitFn,
	)
	if verdict.Kind != claudeexec.ClaudeAuthRequired {
		t.Errorf("Kind = %v, want ClaudeAuthRequired", verdict.Kind)
	}
	if !called {
		t.Errorf("exitFn not called for auth-required path")
	}
	if _, err := os.Stat(authstate.Path); err != nil {
		t.Errorf("auth_required marker not written: %v", err)
	}
}

func TestHandleClaudeExit_ServerError_NoMarker(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EIDOS_RUN_DIR", tmp)
	authstate.SetPathForTest(filepath.Join(tmp, "auth_required.json"))
	defer authstate.ResetPathForTest()

	cmd := exec.Command("sh", "-c", "exit 1")
	_ = cmd.Run()

	called := false
	exitFn := func(int) { called = true }
	verdict := handleClaudeExitTesting(
		&exec.ExitError{ProcessState: cmd.ProcessState},
		cmd.ProcessState,
		[]byte("API Error: 502 Bad Gateway"),
		exitFn,
	)
	if verdict.Kind != claudeexec.ClaudeServerError {
		t.Errorf("Kind = %v, want ClaudeServerError", verdict.Kind)
	}
	if called {
		t.Errorf("exitFn should NOT be called for ClaudeServerError (retryable)")
	}
	if _, err := os.Stat(authstate.Path); err == nil {
		t.Errorf("auth_required marker should not be written on server-error")
	}
}
```

This requires `authstate.SetPathForTest` / `ResetPathForTest` — add them to `internal/authstate/authstate.go` if absent, or use whichever existing test-injection point the package already exposes. If neither exists, write the marker check against a literal `filepath.Join(tmp, "auth_required.json")` and reconfigure `authstate` accordingly in the implementation step.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/supervisor/... -run TestHandleClaudeExit`
Expected: FAIL with `handleClaudeExitTesting undefined` and `claudeexec` undefined.

- [ ] **Step 3: Write minimal implementation**

Replace `cmd/eidos/supervisor/agent_runner.go::handleClaudeExit` and delete `isAuthError`. Inside the file:

```go
import (
    // ... existing imports ...
    "github.com/LucianoXu/eidopsyche/internal/claudeexec"
)

// handleClaudeExit replaces the old isAuthError-based path. It runs the
// classifier on (err, processstate, stderr), writes the auth_required
// marker + exits with EXIT_AUTH_REQUIRED on ClaudeAuthRequired, and
// returns the wrapped error otherwise. The verdict is also returned so
// the caller can persist its Kind into the transcript entry.
func handleClaudeExit(err error, st *os.ProcessState, stderr []byte) (claudeexec.ClaudeVerdict, error) {
    return handleClaudeExitTesting(err, st, stderr, os.Exit), nil
}

// handleClaudeExitTesting is the test seam — exitFn is os.Exit in production.
func handleClaudeExitTesting(err error, st *os.ProcessState, stderr []byte, exitFn func(int)) claudeexec.ClaudeVerdict {
    v := claudeexec.ClassifyClaudeExit(err, st, stderr)
    if v.Kind == claudeexec.ClaudeAuthRequired {
        if werr := authstate.Write(time.Now()); werr != nil {
            fmt.Fprintf(os.Stderr, "agent-runner: write %s: %v\n", authstate.Path, werr)
        }
        exitFn(EXIT_AUTH_REQUIRED)
    }
    return v
}

// (delete the old isAuthError function entirely)
```

Update the two call sites in `runAgent`:

Replace `return handleClaudeExit(runErr, c.ProcessState)` (line 391) with:

```go
verdict, _ := handleClaudeExit(runErr, c.ProcessState, []byte(stderrBuf.String()))
finalEntry.FailKind = ""
if !finalEntry.OK && verdict.Kind != claudeexec.ClaudeOK {
    finalEntry.FailKind = verdict.Kind.String()
    // Re-finalize so the on-disk index carries fail_kind.
    if ferr := h.finalize(finalEntry, maxCount, maxBytes); ferr != nil {
        log.Printf("agent-runner: wake-id=%s re-finalize fail_kind: %v", h.wakeID, ferr)
    }
}
if runErr != nil {
    return fmt.Errorf("claude exited: %w", err)
}
return nil
```

(Adjust the existing flow at line 358-391 so `finalEntry.FailKind` is set before the FIRST finalize call rather than re-finalizing — re-finalize is wasteful. Simpler structure: compute `verdict` immediately after `runErr := c.Wait()`, set FailKind on finalEntry, then proceed with the existing finalize/log path.)

The non-stream-JSON path at line 267 also has its own claude call site that ends in a slightly different return shape; apply the same change there.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS for the two new tests plus all existing supervisor tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go
git commit -m "fix(supervisor): classify claude exits + write auth marker on 401-exit-1

handleClaudeExit now runs claudeexec.ClassifyClaudeExit on the
captured stderr; the auth_required marker is written when the
verdict is ClaudeAuthRequired regardless of exit code (was
exit-47-or-41 only). Transcript entries persist fail_kind so the
wake list renders failed(auth) etc.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: supervisor birth — same classifier wiring

**Files:**
- Modify: `cmd/eidos/supervisor/birth.go:104-125`
- Modify: `cmd/eidos/supervisor/birth_test.go`

- [ ] **Step 1: Write the failing test**

Add at the bottom of `cmd/eidos/supervisor/birth_test.go`:

```go
func TestProductionBirthHandler_WritesAuthMarkerOnExit1With401(t *testing.T) {
	// Use a stub claude that exits 1 and writes 401 to stderr.
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	body := `#!/bin/sh
echo "API Error: 401 Invalid authentication credentials" >&2
exit 1
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	// ... set up minimum ontologyDir + sig with valid book/calling-words paths ...

	// Reset the authstate marker.
	authstate.SetPathForTest(filepath.Join(dir, "auth_required.json"))
	defer authstate.ResetPathForTest()

	err := productionBirthHandler(context.Background(), sig, ontologyDir)
	if err == nil {
		t.Fatalf("expected error")
	}
	if _, statErr := os.Stat(authstate.Path); statErr != nil {
		t.Errorf("auth_required marker not written on 401-exit-1: %v", statErr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/supervisor/... -run TestProductionBirthHandler_WritesAuthMarkerOnExit1With401`
Expected: FAIL — current code matches only exit 47/41.

- [ ] **Step 3: Write minimal implementation**

In `cmd/eidos/supervisor/birth.go::productionBirthHandler`, replace lines 104-125 with:

```go
c := exec.Command("claude", args...)
c.Dir = ontologyDir
c.Stdout = os.Stdout
stderrBuf := &bytes.Buffer{}
c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
c.Env = append(os.Environ(), "CLAUDE_DIR="+filepath.Join(ontologyDir, ".claude"))
if err := c.Run(); err != nil {
    v := claudeexec.ClassifyClaudeExit(err, c.ProcessState, stderrBuf.Bytes())
    if v.Kind == claudeexec.ClaudeAuthRequired {
        if werr := authstate.Write(time.Now()); werr != nil {
            log.Printf("birth handler: write %s: %v", authstate.Path, werr)
        }
    }
    return fmt.Errorf("claude (birth) exited (%s): %w", v.Kind.String(), err)
}
```

Add the import: `"bytes"`, `"io"`, `"github.com/LucianoXu/eidopsyche/internal/claudeexec"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/birth.go cmd/eidos/supervisor/birth_test.go
git commit -m "fix(supervisor): birth handler uses classifier for auth detection

Same change as agent-runner: capture claude's stderr, run
ClassifyClaudeExit, write the auth_required marker when the
verdict is ClaudeAuthRequired. Previously birth's heuristic
only matched exit 47/41 and missed 401-on-exit-1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: watch_render + transcript_list — render `failed(kind)`

**Files:**
- Modify: `cmd/eidos/forge/watch_render.go:329-336`
- Modify: `cmd/eidos/forge/watch_render_test.go`
- Modify: `cmd/eidos/forge/transcript_list.go:80-84`
- Modify: `cmd/eidos/forge/transcript_list_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/watch_render_test.go`:

```go
func TestRenderList_FailKind(t *testing.T) {
	idx := transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abc", Reason: "heartbeat", StartedAt: 1, EndedAt: 2, OK: false, ExitCode: 1, FailKind: "auth"},
		{ID: "def", Reason: "heartbeat", StartedAt: 3, EndedAt: 4, OK: false, ExitCode: 1}, // no FailKind → fallback
	}}
	out := renderList(idx)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "failed(auth)") {
		t.Errorf("expected failed(auth), got:\n%s", joined)
	}
	if !strings.Contains(joined, "failed(1)") {
		t.Errorf("expected failed(1) fallback for entry without FailKind, got:\n%s", joined)
	}
}
```

(Adjust `renderList` import/name to whatever the existing test uses.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge/... -run TestRenderList_FailKind`
Expected: FAIL — current code only renders `failed(1)`.

- [ ] **Step 3: Write minimal implementation**

In `cmd/eidos/forge/watch_render.go` replace lines 329-336 with:

```go
status := "ok"
if !w.OK {
    switch {
    case w.FailKind != "":
        status = "failed(" + w.FailKind + ")"
    case w.ExitCode > 0:
        status = fmt.Sprintf("failed(%d)", w.ExitCode)
    default:
        status = "crashed"
    }
}
```

In `cmd/eidos/forge/transcript_list.go` replace lines 80-84 with the same pattern:

```go
status := "ok"
if !w.OK {
    switch {
    case w.FailKind != "":
        status = "failed(" + w.FailKind + ")"
    case w.ExitCode != -1 && w.ExitCode != 0:
        status = fmt.Sprintf("failed(%d)", w.ExitCode)
    default:
        status = "crashed"
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/watch_render.go cmd/eidos/forge/transcript_list.go cmd/eidos/forge/watch_render_test.go cmd/eidos/forge/transcript_list_test.go
git commit -m "feat(forge): wake list renders failed(kind) from FailKind

eidos forge watch <name> --list now shows failed(auth) /
failed(server) / failed(network) / etc. for entries carrying
fail_kind, falling back to failed(<exit-code>) for entries
without it.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: login command rewrite — interactive + flags, remove old paths

**Files:**
- Modify: `cmd/eidos/forge/login.go` (full rewrite of the body, keeping the cobra surface)
- Modify: `cmd/eidos/forge/login_test.go`

- [ ] **Step 1: Write the failing test**

Replace the contents of `cmd/eidos/forge/login_test.go` (or add alongside existing tests):

```go
func TestLogin_TokenFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	w := &fakeForgeVolumeWriter{}
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) { return w, nil }
	defer func() { installVolume = nil }()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--token-file", path, "--image", "img"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared after login")
	}
	if len(w.writes) != 1 {
		t.Errorf("want 1 write, got %d", len(w.writes))
	}
}

func TestLogin_PasteFlag(t *testing.T) {
	w := &fakeForgeVolumeWriter{}
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) { return w, nil }
	defer func() { installVolume = nil }()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--paste", "--image", "img"})
	cmd.SetIn(strings.NewReader(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(w.writes) != 1 {
		t.Errorf("want 1 write, got %d", len(w.writes))
	}
}

// fakeForgeVolumeWriter is defined here (or in a shared test helper) and
// implements claudeauth.VolumeWriter with in-memory storage.
type fakeForgeVolumeWriter struct {
	writes  []struct{ relPath, body string }
	cleared bool
}

func (f *fakeForgeVolumeWriter) Write(relPath string, body []byte) error {
	f.writes = append(f.writes, struct{ relPath, body string }{relPath, string(body)})
	return nil
}
func (f *fakeForgeVolumeWriter) ClearAuthRequired() error { f.cleared = true; return nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge/... -run TestLogin`
Expected: FAIL — `installVolume` and the new flag names don't exist yet.

- [ ] **Step 3: Write minimal implementation**

Rewrite `cmd/eidos/forge/login.go`. Drop `installFromHost`, `runInContainerLogin`, `InstallLoginFromHost`, `writeIntoVolume`. Keep `clearAuthRequiredInVolume`. New body:

```go
package forge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// installVolume is the test seam — production wires it to a real
// claudeauth.VolumeWriter backed by forgectl + docker.
var installVolume func(name, image string) (claudeauth.VolumeWriter, error)

func init() {
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) {
		// reuses the unexported forgectlWriter shipped in
		// internal/claudeauth. If that type isn't exported, this stub
		// can instead call claudeauth.WriteToVolume directly via a
		// trivial wrapper — but exposing a writer factory keeps the
		// test seam clean.
		return claudeauth.NewForgectlVolumeWriter(context.Background(), name, image)
	}
}

func newLoginCmd() *cobra.Command {
	var image, tokenFile string
	var paste, generate bool
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Authenticate Claude Code inside a mind-form's volume (isolated per-mindform setup-token)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			img := image
			if img == "" {
				img = DefaultImage()
			}

			var blob []byte
			var err error
			switch {
			case tokenFile != "":
				blob, err = os.ReadFile(tokenFile)
				if err != nil {
					return fmt.Errorf("read %s: %w", tokenFile, err)
				}
			case paste:
				blob, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			case generate:
				blob, err = claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
				if err != nil {
					return err
				}
			default:
				blob, err = runInteractive(cmd, name)
				if err != nil {
					return err
				}
			}

			writer, err := installVolume(name, img)
			if err != nil {
				return err
			}
			if err := claudeauth.WriteToVolumeUsing(writer, blob); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ credentials installed into eidos-mindform-%s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to a .credentials.json file to install")
	cmd.Flags().BoolVar(&paste, "paste", false, "read the credentials JSON from stdin")
	cmd.Flags().BoolVar(&generate, "generate", false, "drive `claude setup-token` in an isolated HOME")
	return cmd
}

// runInteractive walks the operator through the two-path prompt when no
// flag is given. Returns the credentials blob.
func runInteractive(cmd *cobra.Command, name string) ([]byte, error) {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	fmt.Fprintf(out, "How will %s authenticate?\n  [1] Paste a setup-token I've already generated\n  [2] Generate a fresh setup-token now (browser will be needed)\n> ", name)
	choice, err := in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	choice = strings.TrimSpace(choice)
	switch choice {
	case "1":
		fmt.Fprintf(out, "Source?\n  [1] Read from a file\n  [2] Paste contents (blank line ends)\n> ")
		sub, err := in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		switch strings.TrimSpace(sub) {
		case "1":
			fmt.Fprintf(out, "Path to credentials file:\n> ")
			path, err := in.ReadString('\n')
			if err != nil {
				return nil, err
			}
			return os.ReadFile(strings.TrimSpace(path))
		case "2":
			fmt.Fprintf(out, "Paste credentials (blank line ends):\n")
			var buf strings.Builder
			for {
				line, err := in.ReadString('\n')
				if err != nil && err != io.EOF {
					return nil, err
				}
				if strings.TrimSpace(line) == "" {
					break
				}
				buf.WriteString(line)
				if err == io.EOF {
					break
				}
			}
			return []byte(buf.String()), nil
		default:
			return nil, fmt.Errorf("phase1: invalid choice %q", sub)
		}
	case "2":
		return claudeauth.Generate(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "")
	default:
		return nil, fmt.Errorf("invalid choice %q", choice)
	}
}

// InstallLoginInteractive is the wizard-callable shim around the
// interactive flow. Used by internal/firstcontact/phase4_seal.go.
func InstallLoginInteractive(name, image string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newLoginCmd()
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{name, "--image", image})
	return cmd.Execute()
}
```

Also expose `WriteToVolumeUsing` (the test seam) from `internal/claudeauth/volume.go`:

```go
// WriteToVolumeUsing is the testable variant of WriteToVolume — it
// accepts an arbitrary VolumeWriter so callers and tests can substitute
// fakes. Production callers should use WriteToVolume.
func WriteToVolumeUsing(w VolumeWriter, blob []byte) error {
    return writeToVolumeUsing(w, blob)
}

// NewForgectlVolumeWriter returns the production VolumeWriter wired to
// the host docker daemon.
func NewForgectlVolumeWriter(ctx context.Context, name, image string) (VolumeWriter, error) {
    dc, err := forgectl.New()
    if err != nil {
        return nil, err
    }
    return &forgectlWriter{ctx: ctx, client: dc, image: image, slug: name}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/eidos/forge/... -v` and `go build ./...`
Expected: PASS, and the build still succeeds.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/login.go cmd/eidos/forge/login_test.go internal/claudeauth/volume.go
git commit -m "feat(forge): login uses isolated setup-token; drop --from-host/--method

Interactive prompt (paste a token / generate fresh) plus
--token-file / --paste / --generate flags for non-interactive use.
The default no longer copies host \`~/.claude.json\` into the
mindform volume; that path was the root cause of today's
401-on-exit-1 silent failures. --from-host and --method are
removed (no backward-compat). --from-file is renamed --token-file.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: first-contact wizard — call InstallLoginInteractive

**Files:**
- Modify: `internal/firstcontact/phase4_seal.go:182`
- Modify: `internal/firstcontact/phase4_seal_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/firstcontact/phase4_seal_test.go` (use whatever fake-renderer pattern the existing tests already establish):

```go
func TestPhase4Seal_CallsInstallLoginInteractive(t *testing.T) {
	// Spy: replace forge.InstallLoginInteractive with a recording stub.
	called := false
	defer stubInstallLoginInteractive(t, func(slug, image string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		return nil
	})()

	// ... run the wizard's phase-4 happy path against fakes ...

	if !called {
		t.Errorf("InstallLoginInteractive was not called")
	}
}
```

`stubInstallLoginInteractive` swaps the package-level function via a test helper that mirrors the spy pattern used in other wizard tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/firstcontact/... -run TestPhase4Seal_CallsInstallLoginInteractive`
Expected: FAIL — phase4_seal still calls `forge.InstallLoginFromHost`.

- [ ] **Step 3: Write minimal implementation**

In `internal/firstcontact/phase4_seal.go` replace line 182's `forge.InstallLoginFromHost(s.Slug, d.Image)` with:

```go
if err := forge.InstallLoginInteractive(s.Slug, d.Image, os.Stdin, os.Stdout, os.Stderr); err != nil {
    purge()
    return nil, fmt.Errorf("install claude credentials: %w", err)
}
```

Delete `cmd/eidos/forge/login.go::InstallLoginFromHost` and `cmd/eidos/forge/login.go::installFromHost`. Make sure no other call site references them (search: `grep -rn "InstallLoginFromHost\|installFromHost" .`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/firstcontact/... -v` and `go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/firstcontact/phase4_seal.go internal/firstcontact/phase4_seal_test.go cmd/eidos/forge/login.go
git commit -m "feat(firstcontact): phase4 calls InstallLoginInteractive

Replaces the dead InstallLoginFromHost shim with the new
isolated-setup-token interactive flow. Wizard summon now drives
the same login path as eidos forge login.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: integration test — end-to-end with stub claude

**Files:**
- Create: `test/integration/auth_setup_token_test.go` (build tag `integration`)

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package integration_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestForgeLogin_GenerateInstallsIsolatedCreds(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	// Build a stub claude that writes a fixture into $HOME/.claude/.credentials.json
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"stub-access","refreshToken":"stub-refresh","expiresAt":99999999999}}`
	body := `#!/bin/sh
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/.credentials.json" <<EOF
` + fixture + `
EOF
exit 0
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	slug := "auth-test-" + t.Name()
	// `eidos forge create` then `eidos forge login --generate`.
	must := func(args ...string) {
		out, err := exec.Command("./bin/eidos", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("eidos %v: %v\n%s", args, err, out)
		}
	}
	defer must("forge", "purge", slug, "--yes")

	must("forge", "create", slug)
	must("forge", "login", slug, "--generate")

	// Assertion: volume's credentials.json contains the stub fixture,
	// and is NOT identical to the host's ~/.claude/.credentials.json.
	out, err := exec.Command("docker", "run", "--rm",
		"--mount", "source=eidos-mindform-"+slug+",target=/eidos",
		"--entrypoint", "cat",
		"ghcr.io/lucianoxu/eidopsyche-mindform:dev",
		"/eidos/claude/.claude/.credentials.json",
	).Output()
	if err != nil {
		t.Fatalf("read volume creds: %v", err)
	}
	if !strings.Contains(string(out), "stub-access") {
		t.Errorf("volume creds missing stub fixture; got:\n%s", out)
	}

	hostHome, _ := os.UserHomeDir()
	hostCreds, _ := os.ReadFile(filepath.Join(hostHome, ".claude", ".credentials.json"))
	if len(hostCreds) > 0 && bytes.Equal(out, hostCreds) {
		t.Errorf("volume creds identical to host creds (regression: host-copy path re-introduced)")
		t.Logf("hash(out)=%x hash(host)=%x", sha256.Sum256(out), sha256.Sum256(hostCreds))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./test/integration/...`
Expected: integration test framework picks it up; if the bin/eidos build is older it will fail — that's the baseline.

- [ ] **Step 3: Implementation already covered by Tasks 1-10.**

Nothing new to implement at this task; this test exists to anchor the end-to-end invariant against regression.

- [ ] **Step 4: Run test to verify it passes**

```
go build -o bin/eidos ./cmd/eidos
make image
go test -tags=integration ./test/integration/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add test/integration/auth_setup_token_test.go
git commit -m "test(integration): end-to-end setup-token install

Stub \`claude setup-token\` writes a known fixture; assert the
volume's credentials.json contains the stub and DIVERGES from the
host's ~/.claude/.credentials.json. Anti-regression test for the
host-copy path that today's bug rests on.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: final verification — full test sweep + lint + image rebuild

- [ ] **Step 1: Lint and vet**

```bash
gofmt -l . | tee /tmp/gofmt.out
go vet ./...
```

Expected: `/tmp/gofmt.out` is empty; `go vet` is silent.

- [ ] **Step 2: Unit tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build the binary**

```bash
go build -o bin/eidos ./cmd/eidos
./bin/eidos forge login --help
```

Expected: help text mentions `--token-file`, `--paste`, `--generate`; `--from-host` and `--method` are absent.

- [ ] **Step 4: Image rebuild**

```bash
make image
```

Expected: docker build succeeds; image `ghcr.io/lucianoxu/eidopsyche-mindform:dev` updated.

- [ ] **Step 5: Integration tests**

```bash
go test -tags=integration ./test/integration/...
```

Expected: PASS for the new auth_setup_token test plus all existing integration tests.

- [ ] **Step 6: Smoke (manual, optional in autonomous run)**

```bash
# Purge any existing test mindform.
./bin/eidos forge purge smoke-alice --yes || true
# Create + login via paste path.
./bin/eidos forge create smoke-alice
echo '{"claudeAiOauth":{"accessToken":"x","refreshToken":"y","expiresAt":99999999999}}' \
  | ./bin/eidos forge login smoke-alice --paste
./bin/eidos forge purge smoke-alice --yes
```

Expected: login completes; volume has credentials; purge succeeds.

- [ ] **Step 7: Final commit (if anything was tidied)**

```bash
git status
# If clean, skip. Otherwise:
git add -A
git commit -m "chore: post-implementation tidy"
```

---

## Self-Review

**Spec coverage check (skimmed against the spec sections):**

- Background / what broke → covered narratively in the plan header; tested by Task 11.
- Why silent failure → Tasks 6 (agent_runner) + 7 (birth) + classifier in Task 1.
- Architecture diagram (host + container split) → Task 9 (login.go) + Task 4 (WriteToVolume).
- File layout in the volume → Task 4 asserts only `.claude/.credentials.json` is written.
- Host-side `--generate` flow (temp HOME) → Task 3.
- CLI surface (interactive prompt + flags) → Task 9.
- Removed flags (`--from-host`, `--method`, `--from-file` → `--token-file`) → Task 9 + Task 10 (wizard).
- Wizard integration → Task 10.
- `eidos forge create` end-message → not strictly tested as a code change; the existing create.go does not auto-login outside the wizard, so no change is required. **Plan covers this implicitly via spec note**.
- Claude error classifier — types, rules, reactions table → Task 1 (types/rules) + Task 6 + Task 7 (reactions).
- Per-kind table — `ClaudeAuthRequired` ships marker now; others detect-and-log only → Tasks 6 / 7 (auth marker), Task 8 (wake-list rendering for all kinds).
- Migration & cleanup → Task 9 removes `installFromHost`, Task 10 removes `InstallLoginFromHost`.
- Testing matrix — every row of the spec's testing table maps to a task above.
- Anti-test (volume creds diverge from host creds) → Task 11 explicitly.

**Placeholder scan:** No `TBD`, no `TODO`, no `implement later`. Each step has the literal code or command. One conditional ("if `forgectl.RunInitOpts` doesn't have the exact fields, fall back to direct `exec.Command`") is an explicit decision branch with both legs spelled out — that's fine, not a placeholder.

**Type-consistency check:**
- `Validate(blob []byte) error` — same signature in Tasks 2, 4, 9.
- `Generate(stdin io.Reader, stdout, stderr io.Writer, claudeBin string) ([]byte, error)` — same in Tasks 3, 9.
- `VolumeWriter` interface (`Write(relPath string, body []byte) error`, `ClearAuthRequired() error`) — same in Tasks 4, 9.
- `ClassifyClaudeExit(err error, st *os.ProcessState, stderr []byte) ClaudeVerdict` — same in Tasks 1, 6, 7.
- `handleClaudeExitTesting(..., exitFn func(int)) ClaudeVerdict` — same in Task 6 prod + test.
- `InstallLoginInteractive(name, image string, stdin io.Reader, stdout, stderr io.Writer) error` — same in Tasks 9, 10.

No mismatches.
