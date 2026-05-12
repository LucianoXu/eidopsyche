//go:build !windows

package supervisor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/spf13/cobra"
)

// gateConfigPath is the in-container path to the gate's config.toml.
// Hard-coded because the supervisor runs only inside the mind-form
// container; the host's gate config never reaches this code path.
const gateConfigPath = "/eidos/gate/config.toml"

// dreamStateRuntimePath is the in-container dream-state.json path.
// agent-runner reads it before invoking claude so the wake context can
// surface dream-eligibility hints.
//
// var (not const) so tests can substitute a temp file.
var dreamStateRuntimePath = "/eidos/run/dream-state.json"

// agentLockPath is the single-instance lock for the agent process.
// Lives at /eidos/run/agent.lock per spec
// docs/superpowers/specs/2026-05-09-mindforge-v0-design.md (the lock
// is decoupled from the wake directory so a future non-wake-driven
// agent invocation lands on the same singleton).
//
// var (not const) so tests can substitute a temp file.
var agentLockPath = "/eidos/run/agent.lock"

// sessionStatePath is the in-container session.json path. agent-runner
// reads it at wake start to decide between --resume and --session-id;
// `forge dream end` clears it to force a fresh session next wake.
//
// var (not const) so tests can substitute a temp file.
var sessionStatePath = "/eidos/run/session.json"

// transcriptsRuntimeDir is the in-container path to the per-mindform
// transcripts directory; agent-runner writes the per-wake NDJSON file
// and the index there. Must live in the volume so the host's
// `forge watch` (and post-mortem `forge transcript-tail`) can read it.
//
// var (not const) so tests can substitute a temp dir.
var transcriptsRuntimeDir = "/eidos/run/transcripts"

// EXIT_AUTH_REQUIRED is the exit code agent-runner uses when Claude's
// /login token is expired or missing. The supervisor surfaces this state
// via `eidos forge status`.
const EXIT_AUTH_REQUIRED = 47

// claudeMinVersion is the minimum claude CLI version known to emit a
// stream-json schema we understand. Below this, agent-runner falls back
// to plain `-p` mode and skips transcript capture. Var (not const) so
// tests can lower the floor when stubbing claude.
//
// 2.1.0 covers the lineage in which `--include-partial-messages` is
// documented. Older versions either lack the flag or shift event field
// names; we don't try to second-guess them.
var claudeMinVersion = [3]int{2, 1, 0}

// claudeBin names the binary on PATH. Var so tests can substitute a
// shell-script stub.
var claudeBin = "claude"

func newAgentRunnerCmd() *cobra.Command {
	var wakeFile, ontologyDir string
	cmd := &cobra.Command{
		Use:    "agent-runner",
		Short:  "Internal: per-wake harness invoked by supervisor",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if wakeFile == "" || ontologyDir == "" {
				return errors.New("--wake-file and --ontology are required")
			}
			return runAgent(wakeFile, ontologyDir)
		},
	}
	cmd.Flags().StringVar(&wakeFile, "wake-file", "", "path to active.json")
	cmd.Flags().StringVar(&ontologyDir, "ontology", "", "path to /eidos/ontology")
	return cmd
}

func runAgent(wakeFile, ontologyDir string) error {
	// Self-gate on the auth_required marker BEFORE doing any other work.
	// If a previous wake hit a claude auth failure, we exit immediately
	// with EXIT_AUTH_REQUIRED so the supervisor doesn't repeatedly invoke
	// claude (and bill the user for nothing) until `eidos forge login`
	// clears the marker.
	if state, err := authstate.Read(); err == nil && state != nil {
		fmt.Fprintf(os.Stderr,
			"agent-runner: claude auth required (since %s); skipping wake until `eidos forge login` clears %s\n",
			time.Unix(state.Since, 0).UTC().Format(time.RFC3339), authstate.Path)
		os.Exit(EXIT_AUTH_REQUIRED)
	}

	body, err := os.ReadFile(wakeFile)
	if err != nil {
		return fmt.Errorf("read wake file: %w", err)
	}
	var sig wake.Signal
	if err := json.Unmarshal(body, &sig); err != nil {
		return fmt.Errorf("decode wake file: %w", err)
	}

	lock, err := acquireAgentLock(agentLockPath)
	if err != nil {
		return fmt.Errorf("agent lock: %w", err)
	}
	defer releaseAgentLock(lock)

	cfg, _ := config.Load(gateConfigPath)
	ds, _ := dreamstate.Read(dreamStateRuntimePath)
	sig.Context = computeContext(sig, cfg, ds, time.Now())

	identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))

	sess, sessErr := sessionstate.Read(sessionStatePath)
	if sessErr != nil {
		log.Printf("agent-runner: session.json corrupt (%v); minting new", sessErr)
	}
	mode, isFirstWake := decideSessionMode(sess, sessErr, ds)
	sessionUUID := mode.UUID
	if isFirstWake {
		if sessErr == nil && sess.SessionID != "" {
			// dream-end didn't get a chance to clear; do it now.
			if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
				log.Printf("agent-runner: session-state clear before mint: %v", cErr)
			}
		}
		fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
		if mErr != nil {
			return fmt.Errorf("session-state mint: %w", mErr)
		}
		sessionUUID = fresh.SessionID
		mode.UUID = fresh.SessionID
	} else {
		// Pre-flight: if the on-disk jsonl is gone (ontology import lost
		// it, operator manually nuked CLAUDE_DIR, etc.) Claude --resume
		// will fail. Detect the common case here and reset proactively.
		if jsonl := sessionJsonlPath(ontologyDir, sessionUUID); jsonl != "" {
			if _, statErr := os.Stat(jsonl); errors.Is(statErr, fs.ErrNotExist) {
				log.Printf("agent-runner: session jsonl for %s missing; resetting and retrying as new session", sessionUUID)
				if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
					log.Printf("agent-runner: session-state clear before mint: %v", cErr)
				}
				fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
				if mErr != nil {
					return fmt.Errorf("session-state mint after fallback: %w", mErr)
				}
				sessionUUID = fresh.SessionID
				isFirstWake = true
				mode = SessionMode{Kind: SessionNew, UUID: sessionUUID}
			}
		}
	}

	msg := rebuildWakeMessage(sig, cfg, ds, isFirstWake)

	streamJSON := claudeSupportsStreamJSON(claudeBin)
	if !streamJSON {
		log.Printf("agent-runner: stream-json unsupported (claude version below %d.%d.%d); transcripts disabled for this wake",
			claudeMinVersion[0], claudeMinVersion[1], claudeMinVersion[2])
	}

	args := buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, mode)

	var runErr error
	if streamJSON {
		runErr = runWithTranscript(sig, ontologyDir, args, sessionUUID)
	} else {
		runErr = runWithoutTranscript(ontologyDir, args)
	}
	if runErr != nil && errors.Is(runErr, errSessionNotFound) && !isFirstWake {
		log.Printf("agent-runner: claude reports session %s not found; resetting and retrying", sessionUUID)
		if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
			log.Printf("agent-runner: session-state clear after stderr fallback: %v", cErr)
		}
		fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
		if mErr != nil {
			return fmt.Errorf("session-state mint after stderr-fallback: %w", mErr)
		}
		sessionUUID = fresh.SessionID
		isFirstWake = true
		mode = SessionMode{Kind: SessionNew, UUID: sessionUUID}
		msg = rebuildWakeMessage(sig, cfg, ds, true)
		args = buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, mode)
		if streamJSON {
			runErr = runWithTranscript(sig, ontologyDir, args, sessionUUID)
		} else {
			runErr = runWithoutTranscript(ontologyDir, args)
		}
	}
	if runErr != nil {
		return runErr
	}

	if err := sessionstate.IncrementWake(sessionStatePath); err != nil {
		log.Printf("agent-runner: IncrementWake: %v", err)
	}
	return nil
}

// decideSessionMode picks NEW vs RESUME based on already-read state.
// Pure: no I/O. The returned mode.UUID is empty when isFirstWake — the
// caller mints a fresh UUID and fills it in.
func decideSessionMode(sess sessionstate.State, sessReadErr error, ds dreamstate.State) (mode SessionMode, isFirstWake bool) {
	switch {
	case sessReadErr != nil, sess.SessionID == "":
		return SessionMode{Kind: SessionNew}, true
	case ds.LastDreamFinishedAt > sess.SessionStartedAt:
		return SessionMode{Kind: SessionNew}, true
	default:
		return SessionMode{Kind: SessionResume, UUID: sess.SessionID}, false
	}
}

// rebuildWakeMessage assembles the wake-message from already-read state.
// Used by runAgent and (later) by the resume-fallback path that needs to
// re-build the message after flipping IsFirstWakeOfNewSession.
func rebuildWakeMessage(sig wake.Signal, cfg config.Config, ds dreamstate.State, isFirstWake bool) string {
	return prompts.BuildWake(prompts.WakeInput{
		Reason:                  string(sig.Reason),
		Hint:                    sig.Hint,
		InboxUnread:             sig.Context.InboxUnread,
		SinceLastWakeSeconds:    sig.Context.SinceLastWakeSeconds,
		MasterLikelyAsleep:      sig.Context.MasterLikelyAsleep,
		QuietStart:              cfg.MindForm.QuietStart,
		QuietEnd:                cfg.MindForm.QuietEnd,
		TZ:                      cfg.MindForm.TZ,
		SinceLastDreamSeconds:   sig.Context.SinceLastDreamSeconds,
		DreamEligible:           sig.Context.DreamEligible,
		LastDreamNote:           ds.LastDreamNote,
		PlanID:                  sig.Context.PlanID,
		IsFirstWakeOfNewSession: isFirstWake,
		DreamCount:              ds.DreamCount,
		LastDreamFinishedAt:     ds.LastDreamFinishedAt,
	})
}

// runWithoutTranscript runs claude with stdout wired straight through
// to docker logs (the legacy behaviour). Used when the claude version
// pre-dates stream-json support.
func runWithoutTranscript(ontologyDir string, args []string) error {
	c := exec.Command(claudeBin, args...) //nolint:gosec
	c.Dir = ontologyDir
	c.Stdout = os.Stdout
	stderrBuf := &strings.Builder{}
	c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
	c.Env = claudeSpawnEnv(filepath.Join(ontologyDir, ".claude"))
	runErr := c.Run()
	if runErr != nil && matchSessionNotFound(stderrBuf.String()) {
		return errors.Join(errSessionNotFound, runErr)
	}
	// Side-effects: write marker + maybe exit on ClaudeAuthRequired.
	v := handleClaudeExit(runErr, c.ProcessState, []byte(stderrBuf.String()))
	_ = v // no transcript entry to stamp here (legacy path)
	if runErr != nil {
		return fmt.Errorf("claude exited: %w", runErr)
	}
	return nil
}

// errSessionNotFound is returned when claude exited non-zero and stderr
// indicated the resumed session is missing or unreadable. runAgent
// catches this once and retries with a fresh session UUID.
var errSessionNotFound = errors.New("claude reports session not found")

// matchSessionNotFound is a case-insensitive substring scan over claude's
// stderr for any marker we know it uses to signal a missing / unreadable
// session file. Tolerant on additions — substring match.
func matchSessionNotFound(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, m := range []string{"session not found", "could not find session", "no such session"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// runWithTranscript runs claude with --output-format stream-json and tees
// its stdout into the per-wake NDJSON file under transcripts/. Lifecycle
// summaries are emitted to log.Printf (docker logs). sessionUUID is
// stamped onto the wake's index Entry so the watch surface can group
// wakes into sessions; pass "" when no session is active.
func runWithTranscript(sig wake.Signal, ontologyDir string, args []string, sessionUUID string) error {
	startedAt := time.Now().Unix()

	wakeID := sig.ID
	if wakeID == "" {
		wakeID = fmt.Sprintf("%d-%s", startedAt, sig.Reason)
	}

	h, err := openTranscriptHandle(transcriptsRuntimeDir, wakeID)
	if err != nil {
		log.Printf("agent-runner: %v; falling back to plain stdout", err)
		return runWithoutTranscript(ontologyDir, plainClaudeArgs(args))
	}
	defer h.close()

	c := exec.Command(claudeBin, args...) //nolint:gosec
	c.Dir = ontologyDir
	stderrBuf := &strings.Builder{}
	c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
	c.Env = claudeSpawnEnv(filepath.Join(ontologyDir, ".claude"))

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	log.Printf("agent-runner: wake-id=%s reason=%s starting", h.wakeID, sig.Reason)
	if err := c.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	counter := &transcript.Counter{}
	drainErr := make(chan error, 1)
	go func() { drainErr <- drainStreamJSON(stdoutPipe, h, counter, h.wakeID) }()

	// Per os/exec.Cmd.StdoutPipe: must drain BEFORE Wait, otherwise
	// Wait closes the pipe and the drain races into "file already
	// closed" instead of clean EOF.
	derr := <-drainErr
	runErr := c.Wait()
	h.close() // flush before Finalize.
	if derr != nil {
		log.Printf("agent-runner: wake-id=%s drain: %v", h.wakeID, derr)
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	verdict := claudeexec.ClassifyClaudeExit(runErr, c.ProcessState, []byte(stderrBuf.String()))

	endedAt := time.Now().Unix()
	cfg, _ := config.Load(gateConfigPath)
	maxCount, maxBytes := transcriptLimits(cfg)
	finalEntry := transcript.Entry{
		ID:             h.wakeID,
		SessionID:      sessionUUID,
		Reason:         string(sig.Reason),
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		OK:             runErr == nil,
		ExitCode:       exitCode,
		ToolUseCount:   counter.ToolUseCount,
		ThinkingBlocks: counter.ThinkingBlocks,
	}
	if runErr != nil && verdict.Kind != claudeexec.ClaudeOK {
		finalEntry.FailKind = verdict.Kind.String()
	}
	if counter.Result != nil {
		finalEntry.OK = runErr == nil && counter.Result.OK
		finalEntry.CostUSD = counter.Result.TotalCostUSD
	}
	if ferr := h.finalize(finalEntry, maxCount, maxBytes); ferr != nil {
		log.Printf("agent-runner: wake-id=%s finalize: %v", h.wakeID, ferr)
	}

	statusStr := "ok"
	if !finalEntry.OK {
		if finalEntry.FailKind != "" {
			statusStr = "failed(" + finalEntry.FailKind + ")"
		} else {
			statusStr = fmt.Sprintf("failed(%d)", exitCode)
		}
	}
	costStr := "-"
	if finalEntry.CostUSD != nil && *finalEntry.CostUSD > 0 {
		costStr = fmt.Sprintf("$%.4f", *finalEntry.CostUSD)
	}
	log.Printf("agent-runner: wake-id=%s completed cost=%s dur_s=%d tools=%d %s",
		h.wakeID, costStr, endedAt-startedAt, counter.ToolUseCount, statusStr)

	if runErr != nil && matchSessionNotFound(stderrBuf.String()) {
		return errors.Join(errSessionNotFound, runErr)
	}

	// Side-effects: write marker + maybe exit on ClaudeAuthRequired.
	if verdict.Kind == claudeexec.ClaudeAuthRequired {
		handleClaudeExit(runErr, c.ProcessState, []byte(stderrBuf.String()))
	}
	if runErr != nil {
		return fmt.Errorf("claude exited: %w", runErr)
	}
	return nil
}

// transcriptLimits resolves the per-mindform transcripts caps from
// config, falling back to package defaults when unset.
func transcriptLimits(cfg config.Config) (maxCount int, maxBytes int64) {
	maxCount = transcript.DefaultMaxCount
	maxBytes = transcript.DefaultMaxBytes
	if cfg.MindForm.TranscriptsMaxCount > 0 {
		maxCount = cfg.MindForm.TranscriptsMaxCount
	}
	if cfg.MindForm.TranscriptsMaxBytes != "" {
		if b, err := config.ParseByteSize(cfg.MindForm.TranscriptsMaxBytes); err == nil && b > 0 {
			maxBytes = b
		}
	}
	return maxCount, maxBytes
}

// drainStreamJSON tees src to dst line-by-line while folding each event
// into counter and emitting a per-event lifecycle summary to log.Printf.
// Uses bufio.Reader.ReadSlice in a loop so individual NDJSON lines have
// no upper size limit (a 1 MiB tool_result is normal).
func drainStreamJSON(src io.Reader, dst io.Writer, counter *transcript.Counter, wakeID string) error {
	r := bufio.NewReaderSize(src, 1<<20) // 1 MiB starting buffer
	for {
		line, err := readLineUnbounded(r)
		if len(line) > 0 {
			if _, werr := dst.Write(line); werr != nil {
				return werr
			}
			if ev, perr := transcript.ParseEvent(line); perr == nil {
				counter.Observe(ev)
				emitLifecycle(wakeID, ev)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLineUnbounded reads up to and including the next '\n' from r,
// stitching together as many ReadSlice chunks as needed. A trailing
// partial line at EOF is returned with (line, io.EOF).
func readLineUnbounded(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

// emitLifecycle writes one summarised log line per interesting event so
// the operator scrolling docker logs can follow what claude is doing
// without reading raw NDJSON.
func emitLifecycle(wakeID string, ev transcript.Event) {
	switch ev.Type {
	case transcript.TypeSystem:
		if ev.Subtype == "init" {
			log.Printf("agent-runner: wake-id=%s init model=%q tools=%d mcp=%d",
				wakeID, ev.Model, len(ev.Tools), len(ev.MCPServers))
		}
	case transcript.TypeAssistant:
		if ev.Message == nil {
			return
		}
		for _, b := range ev.Message.Content {
			if b.Type == transcript.BlockToolUse {
				log.Printf("agent-runner: wake-id=%s tool=%s", wakeID, b.Name)
			}
		}
	case transcript.TypeResult:
		// The big "completed" line is emitted by runWithTranscript after
		// we know the process exit code; here we keep silent to avoid
		// duplicate noise.
	}
}

// claudeSupportsStreamJSON probes claude's --version output. Returns
// true when the parsed semver is at or above claudeMinVersion. Any
// failure (binary missing, exec error, unparseable output) yields false
// → caller falls back to plain `-p` mode.
func claudeSupportsStreamJSON(bin string) bool {
	out, err := exec.Command(bin, "--version").CombinedOutput() //nolint:gosec
	if err != nil {
		return false
	}
	return parseClaudeVersion(string(out))
}

// parseClaudeVersion scans s for a `MAJOR.MINOR.PATCH` token and reports
// whether it is at or above claudeMinVersion. Pre-release suffixes
// (`-beta1`) are stripped before comparison.
func parseClaudeVersion(s string) bool {
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimPrefix(tok, "v")
		if i := strings.Index(tok, "-"); i > 0 {
			tok = tok[:i]
		}
		var maj, min, pat int
		if n, err := fmt.Sscanf(tok, "%d.%d.%d", &maj, &min, &pat); err == nil && n == 3 {
			return semverGE([3]int{maj, min, pat}, claudeMinVersion)
		}
	}
	return false
}

func semverGE(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

// plainClaudeArgs strips stream-json flags from args, restoring the
// `-p msg` form. Used when stream-json setup fails after the version
// probe was optimistic.
func plainClaudeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch a {
		case "--output-format":
			skipNext = true
			continue
		case "--verbose", "--include-partial-messages":
			continue
		}
		out = append(out, a)
	}
	return out
}

// handleClaudeExit runs the classifier on (err, processstate, stderr).
// Returns the verdict so the caller can persist Kind.String() into the
// transcript entry's FailKind. When Kind == ClaudeAuthRequired, writes
// the auth_required marker and exits with EXIT_AUTH_REQUIRED so the
// supervisor's self-gate stops billing the operator for failed wakes.
func handleClaudeExit(err error, st *os.ProcessState, stderr []byte) claudeexec.ClaudeVerdict {
	return handleClaudeExitTesting(err, st, stderr, os.Exit)
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

// computeContext folds quiet-hours, dream-state, and plan-id into the
// signal's Context. Pure function so it's testable without IO.
func computeContext(sig wake.Signal, cfg config.Config, ds dreamstate.State, now time.Time) wake.Context {
	ctx := sig.Context

	if cfg.MindForm.QuietStart != "" && cfg.MindForm.QuietEnd != "" {
		tz := time.UTC
		if cfg.MindForm.TZ != "" {
			if loc, err := time.LoadLocation(cfg.MindForm.TZ); err == nil {
				tz = loc
			}
		}
		ctx.MasterLikelyAsleep = config.InQuietHours(now, cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd, tz)
	}

	if ds.LastDreamFinishedAt > 0 {
		ctx.SinceLastDreamSeconds = now.Unix() - ds.LastDreamFinishedAt
		floor := config.DefaultDreamMinInterval
		if s := cfg.MindForm.DreamMinInterval; s != "" {
			if d, err := time.ParseDuration(s); err == nil {
				floor = d
			}
		}
		ctx.DreamEligible = ctx.SinceLastDreamSeconds >= int64(floor.Seconds())
	} else {
		ctx.DreamEligible = true
	}

	// PlanID flows through unchanged from the wake signal.
	return ctx
}

func acquireAgentLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return f, nil
}

func releaseAgentLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// encodeCWD replaces every non-alphanumeric char with '-', per Claude
// Code's documented session-storage scheme. Idempotent.
func encodeCWD(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// sessionJsonlPath is the absolute path Claude Code uses for a session
// jsonl: <CLAUDE_DIR>/projects/<encoded-cwd>/<uuid>.jsonl with
// CLAUDE_DIR = <ontologyDir>/.claude (matches the env we export for
// the claude subprocess). Returns "" when ontologyDir is empty.
func sessionJsonlPath(ontologyDir, uuid string) string {
	if ontologyDir == "" {
		return ""
	}
	claudeDir := filepath.Join(ontologyDir, ".claude")
	return filepath.Join(claudeDir, "projects", encodeCWD(ontologyDir), uuid+".jsonl")
}

// SessionKind selects how a wake's claude invocation is bound to a
// Claude Code session.
type SessionKind int

const (
	// SessionNew creates a new session with the given UUID via
	// `--session-id <UUID>`. Used for the first wake of a session
	// (fresh ontology, post-purge, or first wake after dream-end).
	SessionNew SessionKind = iota
	// SessionResume continues an existing session via
	// `--resume <UUID>`. Used for every wake within a session.
	SessionResume
)

// SessionMode describes how the upcoming claude invocation should bind
// to a Claude Code session. UUID is required for both kinds.
type SessionMode struct {
	Kind SessionKind
	UUID string
}

// buildClaudeArgs constructs the argv passed to `claude` for one wake.
// Reads the mind-form config to pick up an optional model pin.
//
// Config-load errors are tolerated: a missing or malformed config.toml
// drops us back to claude's subscription default rather than bricking
// the wake. Once a config loads, the model id is passed through
// verbatim — host-side commands (forge create / forge config) validate
// the id; a stale / hand-edited config with an unknown id surfaces at
// the next wake when claude itself rejects it.
//
// streamJSON=true appends `--output-format stream-json --verbose
// --include-partial-messages` so the supervisor can capture the
// structured event stream into the per-wake transcript file.
//
// sess controls session continuity: SessionNew creates a session with
// the given UUID via `--session-id`; SessionResume picks up an existing
// one via `--resume`. An empty UUID emits no session flag (legacy
// behaviour, for tests / pre-flag callers).
func buildClaudeArgs(identity, msg, configPath string, streamJSON bool, sess SessionMode) []string {
	args := []string{
		"--append-system-prompt", identity,
		"--dangerously-skip-permissions",
	}
	if sess.UUID != "" {
		switch sess.Kind {
		case SessionNew:
			args = append(args, "--session-id", sess.UUID)
		case SessionResume:
			args = append(args, "--resume", sess.UUID)
		}
	}
	if cfg, err := config.Load(configPath); err == nil {
		if model := cfg.MindForm.Model; model != "" {
			args = append(args, "--model", model)
		}
	}
	if streamJSON {
		args = append(args, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	}
	args = append(args, "-p", msg)
	return args
}

// claudeSpawnEnv returns the env slice every claude spawn site should
// pass to exec.Cmd.Env. Starts from os.Environ() (so HOME, PATH, model
// hints from `eidos forge config` survive), pins CLAUDE_DIR to the
// ontology's .claude tree, and — if a per-mindform setup-token file is
// present at $HOME/setup_token — appends CLAUDE_CODE_OAUTH_TOKEN so
// claude reads its credentials from the env-var path instead of the
// .credentials.json file.
//
// Read errors from the setup-token file are logged but do not abort
// the spawn: the credentials.json fallback may still succeed and the
// operator sees the failure in supervisor logs + can re-run
// `eidos forge login` to remediate.
func claudeSpawnEnv(claudeDir string) []string {
	env := append(os.Environ(), "CLAUDE_DIR="+claudeDir)
	env, err := claudeauth.InjectSetupTokenEnv(env, os.Getenv("HOME"))
	if err != nil {
		log.Printf("agent-runner: read setup-token: %v (falling through to .credentials.json)", err)
	}
	return env
}
