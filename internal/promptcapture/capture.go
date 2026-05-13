package promptcapture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Opts configures one capture run. Callers fill in the fields they
// know; defaults are applied inside Run.
type Opts struct {
	// ClaudeBin is the path to the claude executable (or stub in tests).
	// Required.
	ClaudeBin string
	// Cwd is the working directory for the spawned claude. When set,
	// claude picks up any ancestor CLAUDE.md it finds from there.
	Cwd string
	// SystemPrompt is passed via --system-prompt (full takeover of
	// Claude Code's default preamble). Empty means omit the flag
	// (equivalent to --bare).
	SystemPrompt string
	// Model, if non-empty, is passed as --model. Empty → omitted.
	Model string
	// Prompt is the -p argument. Default "ping" when empty.
	Prompt string
	// ExtraArgs are appended after the standard args. Used by
	// utils/promptdump to pass through user-supplied flags after `--`.
	ExtraArgs []string
	// ClaudeDir, if non-empty, is set as CLAUDE_DIR in the spawn env.
	// Used in-container to match the agent-loop's claude data dir.
	ClaudeDir string
	// CapturedFrom is stamped onto the envelope. Zero-valued for the
	// utils/promptdump shim; populated by the in-container worker.
	CapturedFrom CapturedFrom
	// NoPOSTAfter caps how long Run waits for claude to POST a request.
	// Default 10s.
	NoPOSTAfter time.Duration
	// Verbose mirrors `-v`: log proxy traffic + claude stderr to os.Stderr.
	Verbose bool
}

// Run executes one full capture cycle and returns the assembled
// envelope as a JSON-normalized map. The map is what BuildEnvelopeMap
// produces — callers serialize to JSON or render to Markdown as needed.
func Run(ctx context.Context, opts Opts) (map[string]any, error) {
	if opts.ClaudeBin == "" {
		return nil, fmt.Errorf("promptcapture: ClaudeBin required")
	}
	if opts.Prompt == "" {
		opts.Prompt = "ping"
	}
	if opts.NoPOSTAfter == 0 {
		opts.NoPOSTAfter = 10 * time.Second
	}
	claudeVer := probeClaudeVersion(ctx, opts.ClaudeBin)

	srv := newCaptureServer()
	srv.verbose = opts.Verbose
	baseURL, shutdownProxy, err := srv.listen()
	if err != nil {
		return nil, err
	}
	defer shutdownProxy(context.Background())

	args := buildClaudeArgs(opts)

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "promptcapture: spawning %s %s\n", opts.ClaudeBin, strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "promptcapture: proxy at %s\n", baseURL)
	}

	cmd := exec.CommandContext(ctx, opts.ClaudeBin, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildSpawnEnv(baseURL, opts.ClaudeDir)
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", opts.ClaudeBin, err)
	}

	noPOSTTimer := time.NewTimer(opts.NoPOSTAfter)
	defer noPOSTTimer.Stop()

	select {
	case <-srv.done:
		grace := time.NewTimer(3 * time.Second)
		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()
		select {
		case <-waitCh:
		case <-grace.C:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			<-waitCh
		}
		grace.Stop()
	case <-noPOSTTimer.C:
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return nil, fmt.Errorf("claude did not POST /v1/messages within %s; stderr:\n%s",
			opts.NoPOSTAfter, stderrBuf.String())
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return nil, ctx.Err()
	}

	if opts.Verbose && stderrBuf.Len() > 0 {
		fmt.Fprintf(os.Stderr, "promptcapture: claude stderr:\n%s\n", stderrBuf.String())
	}

	cwd, _ := os.Getwd()
	meta := EnvelopeMeta{
		CapturedAt:    time.Now().UTC(),
		ClaudeVersion: claudeVer,
		ClaudePath:    opts.ClaudeBin,
		ClaudeArgs:    args,
		Host:          HostInfo{Platform: runtime.GOOS, CWD: cwd},
		CapturedFrom:  opts.CapturedFrom,
	}
	return BuildEnvelopeMap(meta, srv.captured)
}

// buildClaudeArgs constructs the argv slice for the spawned process.
func buildClaudeArgs(opts Opts) []string {
	var args []string
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, "--dangerously-skip-permissions")
	args = append(args, opts.ExtraArgs...)
	args = append(args, "-p", opts.Prompt)
	return args
}

// buildSpawnEnv strips any pre-existing ANTHROPIC_*/CLAUDE_DIR from the
// inherited env (POSIX execve allows duplicate keys and most libc
// implementations resolve to the first occurrence, which would let a
// developer's existing values bypass our proxy or claude_dir).
func buildSpawnEnv(baseURL, claudeDir string) []string {
	env := filterEnv(os.Environ(), "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_DIR")
	env = append(env,
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY=sk-dummy-promptdump",
	)
	if claudeDir != "" {
		env = append(env, "CLAUDE_DIR="+claudeDir)
	}
	return env
}

func filterEnv(env []string, names ...string) []string {
	skip := make(map[string]struct{}, len(names))
	for _, n := range names {
		skip[n] = struct{}{}
	}
	out := env[:0:0]
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func probeClaudeVersion(ctx context.Context, claudePath string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, claudePath, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
