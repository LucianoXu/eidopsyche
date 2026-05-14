//go:build !windows

// Package agentloop — SpawnClaude helper.
//
// SpawnClaude builds claude's argv, starts the process in a new
// process group (so SIGKILL/SIGTERM can be sent to the whole group),
// exposes stdin/stdout/stderr pipes, and a WaitErr channel that
// receives the process's Wait() error exactly once.
package agentloop

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
)

// SessionMode selects --session-id (new session) vs --resume (existing).
type SessionMode int

const (
	// SessionNew starts a brand-new claude session with --session-id.
	SessionNew SessionMode = iota
	// SessionResume continues an existing session with --resume.
	SessionResume
)

// SpawnOpts is the input to SpawnClaude.
type SpawnOpts struct {
	// Binary is the path to the claude executable (or stub in tests).
	Binary string
	// Mode selects new-session vs resume.
	Mode SessionMode
	// SessionUUID is the session UUID passed via --session-id or --resume.
	SessionUUID string
	// Model, if non-empty, is passed as --model. Empty → omitted (claude picks default).
	Model string
	// Effort, if non-empty, is passed as --effort (one of
	// low|medium|high|xhigh|max). Empty → omitted (claude picks
	// default). Validated upstream by config.ValidateEffort.
	Effort string
	// SystemPrompt is the full --system-prompt payload assembled by
	// internal/prompts.Build. Replaces the previous identity-only
	// --append-system-prompt; the mind-form no longer sees Claude
	// Code's default preamble.
	SystemPrompt string
	// Cwd is the working directory for the claude process (ontology root).
	Cwd string
	// ClaudeDir is the value to inject as CLAUDE_DIR in the process env.
	ClaudeDir string
	// ExtraArgs are appended after the standard args. Used by tests to pass
	// stub-specific flags (e.g. --mode, --at-n). Production callers pass nil.
	ExtraArgs []string
}

// SpawnedClaude is the handle returned by SpawnClaude. Callers own the
// pipes and must drain Stdout (and optionally Stderr). Call Wait() or
// receive from WaitErr to observe process exit.
type SpawnedClaude struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	// WaitErr receives the process Wait() error exactly once, then is closed.
	WaitErr chan error

	waitOnce   sync.Once
	waitResult error
}

// Wait blocks until the underlying process exits and returns the
// Wait() error. Idempotent: subsequent calls return the same value
// without re-reading the channel.
func (s *SpawnedClaude) Wait() error {
	s.waitOnce.Do(func() { s.waitResult = <-s.WaitErr })
	return s.waitResult
}

// SpawnClaude starts a claude subprocess in stream-json input/output mode and
// returns its three pipes plus a WaitErr channel.
//
// The process is started in its own process group (Setpgid=true) so that
// the caller can kill the whole group on shutdown without touching the parent.
func SpawnClaude(opts SpawnOpts) (*SpawnedClaude, error) {
	args := buildClaudeArgs(opts)

	cmd := exec.Command(opts.Binary, args...) //nolint:gosec
	cmd.Dir = opts.Cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = claudeSpawnEnv(opts.ClaudeDir)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
		close(waitErr)
	}()

	return &SpawnedClaude{
		Cmd:     cmd,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		WaitErr: waitErr,
	}, nil
}

// buildClaudeArgs constructs the argv slice for the claude subprocess.
func buildClaudeArgs(opts SpawnOpts) []string {
	args := []string{
		"--system-prompt", opts.SystemPrompt,
		"--dangerously-skip-permissions",
	}
	switch opts.Mode {
	case SessionNew:
		args = append(args, "--session-id", opts.SessionUUID)
	case SessionResume:
		args = append(args, "--resume", opts.SessionUUID)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	args = append(args,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--tools", claudeexec.ToolsArg(),
		"-p", "",
	)
	args = append(args, opts.ExtraArgs...)
	return args
}

// claudeSpawnEnv constructs the environment for the claude subprocess:
// the current process's env plus CLAUDE_DIR, and the optional OAuth
// token injected from $HOME/setup_token (see internal/claudeauth).
// Read errors from the token file are logged but do not abort startup —
// the .credentials.json fallback path inside claude may still succeed.
func claudeSpawnEnv(claudeDir string) []string {
	env := append(os.Environ(), "CLAUDE_DIR="+claudeDir)
	env, err := claudeauth.InjectSetupTokenEnv(env, os.Getenv("HOME"))
	if err != nil {
		fmt.Fprintf(stderr(), "agent-loop: spawn: read setup-token: %v (falling through)\n", err)
	}
	return env
}

// stderrCapture is a thread-safe ring buffer that holds the last maxSize bytes
// of claude's stderr for ClassifyClaudeExit consumption.
type stderrCapture struct {
	mu      sync.Mutex
	buf     []byte
	maxSize int
}

func newStderrCapture(maxSize int) *stderrCapture {
	return &stderrCapture{maxSize: maxSize}
}

// Write appends p to the buffer, trimming the oldest bytes when the buffer
// exceeds maxSize. Safe for concurrent use.
func (c *stderrCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.maxSize {
		c.buf = c.buf[len(c.buf)-c.maxSize:]
	}
	return len(p), nil
}

// String returns a snapshot of the buffer as a string. Safe for concurrent use.
func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// EncodeCWD replicates Claude Code's encoded-CWD scheme for the
// projects/<encoded>/ session jsonl path. Non-alphanumeric runes are
// replaced with '-'.
//
// Used by SessionJsonlPath and by the startup pre-flight check (Stage 9.1).
func EncodeCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	b := make([]byte, 0, len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b = append(b, byte(r))
		default:
			b = append(b, '-')
		}
	}
	return string(b)
}

// SessionJsonlPath returns the absolute path Claude Code uses to store a
// session's JSONL transcript:
//
//	<ontologyDir>/.claude/projects/<encoded-cwd>/<sessionUUID>.jsonl
//
// Returns "" when ontologyDir is empty.
func SessionJsonlPath(ontologyDir, sessionUUID string) string {
	if ontologyDir == "" {
		return ""
	}
	claudeDir := filepath.Join(ontologyDir, ".claude")
	encoded := EncodeCWD(ontologyDir)
	return filepath.Join(claudeDir, "projects", encoded, sessionUUID+".jsonl")
}
