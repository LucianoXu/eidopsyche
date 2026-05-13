//go:build !windows

// Package agentloop — Run() orchestrator.
//
// Run wires together all components from Stages 1–10 into a single
// entry point: lock acquisition, stale-state recovery, session decision,
// claude spawn, and a goroutine supervision loop that handles rotation
// requests, claude crashes, and context cancellation.
package agentloop

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// RunOpts wires Run.
type RunOpts struct {
	// ClaudeBin is the path to the claude executable (or stub in tests).
	ClaudeBin string
	// ExtraClaudeArgs are appended after standard args (e.g. stub flags in tests).
	ExtraClaudeArgs []string
	// OntologyDir is the mind-form's ontology root (working directory for claude).
	OntologyDir string
	// ClaudeDir is passed as CLAUDE_DIR in the claude process environment.
	ClaudeDir string
	// SessionStatePath is the path to session.json.
	SessionStatePath string
	// DreamStatePath is the path to dream-state.json.
	DreamStatePath string
	// AgentStatePath is the path to agent-state.json (best-effort observability).
	AgentStatePath string
	// TranscriptsDir is the directory for per-turn transcript ndjson files.
	TranscriptsDir string
	// AgentLockPath is the file used for the single-instance flock.
	AgentLockPath string
	// ConfigPath is the path to config.toml.
	ConfigPath string
	// WakeStdin is the reader from which JSONL wake.Signal lines are read
	// (typically supervisor's pipe; io.Pipe in tests).
	WakeStdin io.Reader
	// IdleWait is the maximum time to wait for the state machine to reach
	// idle before force-closing stdin on rotation.
	IdleWait time.Duration
	// CloseGrace is the maximum time to wait for claude to exit after stdin
	// is closed before escalating to SIGTERM/SIGKILL on rotation.
	CloseGrace time.Duration
}

// Run is the agent-loop entry point. It orchestrates lock acquisition,
// stale-state recovery, session decision, claude spawn, and the goroutine
// supervision loop that handles rotation requests, claude crashes, and
// context cancellation.
func Run(ctx context.Context, opts RunOpts) error {
	// ── 1. Acquire the single-instance lock ──────────────────────────────
	lock, err := acquireLock(opts.AgentLockPath)
	if err != nil {
		return fmt.Errorf("acquire %s: %w", opts.AgentLockPath, err)
	}
	defer releaseLock(lock)

	// ── 2. Stale-state recovery ───────────────────────────────────────────
	if err := RecoverStaleDream(opts.DreamStatePath, time.Now()); err != nil {
		fmt.Fprintf(stderr(), "agent-loop: stale dream recovery: %v\n", err)
	}
	store, err := transcript.NewStore(opts.TranscriptsDir)
	if err != nil {
		return fmt.Errorf("transcripts store: %w", err)
	}
	if err := store.Recover(); err != nil {
		fmt.Fprintf(stderr(), "agent-loop: transcripts recover: %v\n", err)
	}

	// ── 3. Session-mode decision ──────────────────────────────────────────
	mode, isFirstWake, err := DecideSessionMode(opts.SessionStatePath, opts.DreamStatePath, opts.OntologyDir)
	if err != nil {
		return fmt.Errorf("decide session: %w", err)
	}
	if mode.Kind == SessionNew {
		fresh, mErr := sessionstate.Mint(opts.SessionStatePath, time.Now())
		if mErr != nil {
			return fmt.Errorf("mint session: %w", mErr)
		}
		mode.UUID = fresh.SessionID
	}

	// ── 4. Load config and initial state ─────────────────────────────────
	cfg, _ := config.Load(opts.ConfigPath)
	ds, _ := dreamstate.Read(opts.DreamStatePath)

	// ── 4b. Assemble system prompt ───────────────────────────────────────
	// FromOntology reads self/identity.toml; Build inlines CLAUDE.md +
	// soul.md plus the framework essentials. Identity facts are surfaced
	// through the rendered template's Info block, not as inlined content.
	// The prompt is rebuilt from disk on every SpawnClaude so the mind-form's
	// own edits propagate on the next wake.
	facts, _ := prompts.FromOntology(opts.OntologyDir)
	facts.Model = cfg.MindForm.Model
	facts.Effort = cfg.MindForm.Effort
	if facts.Effort == "" {
		facts.Effort = config.DefaultEffort
	}
	facts.OntologyDir = opts.OntologyDir
	systemPrompt, err := prompts.Build(ctx, facts, opts.OntologyDir)
	if err != nil {
		return fmt.Errorf("build system prompt: %w", err)
	}

	// ── 5. Counters and per-session flags ─────────────────────────────────
	sm := NewStateMachine(time.Now())
	var outstandingWakes atomic.Int64
	var resultsSeen atomic.Int64
	var wakesInSession atomic.Int64
	var firstWakeFlag atomic.Bool
	firstWakeFlag.Store(isFirstWake)
	wq := newWakeQueue()

	// ── 6. Spawn claude ───────────────────────────────────────────────────
	claude, err := SpawnClaude(SpawnOpts{
		Binary:       opts.ClaudeBin,
		Mode:         mode.Kind,
		SessionUUID:  mode.UUID,
		Model:        cfg.MindForm.Model,
		SystemPrompt: systemPrompt,
		Cwd:          opts.OntologyDir,
		ClaudeDir:    opts.ClaudeDir,
		ExtraArgs:    opts.ExtraClaudeArgs,
	})
	if err != nil {
		return fmt.Errorf("spawn claude: %w", err)
	}

	// ── 7. Dream watcher ──────────────────────────────────────────────────
	rotateCh := make(chan struct{}, 4)
	dw := NewDreamWatcher(DreamWatcherConfig{
		Path:                opts.DreamStatePath,
		OnRotationRequested: func() { rotateCh <- struct{}{} },
	})

	// ── 8. Accessor closures (live reads for agent-state snapshots) ───────
	currentSessionID := func() string {
		st, _ := sessionstate.Read(opts.SessionStatePath)
		return st.SessionID
	}
	currentSessionStartedAt := func() int64 {
		st, _ := sessionstate.Read(opts.SessionStatePath)
		return st.SessionStartedAt
	}

	// ── 9. Drainer factory ────────────────────────────────────────────────
	// makeDrainer returns a fresh *Drainer with zeroed internal state.
	// A new instance is created for each claude spawn so stale
	// curHandle/turnStart/counter from the previous process never leak in.
	makeDrainer := func() *Drainer {
		return NewDrainer(DrainerConfig{
			TranscriptsDir:   opts.TranscriptsDir,
			Store:            store,
			StateMachine:     sm,
			Clock:            time.Now,
			AgentStatePath:   opts.AgentStatePath,
			OutstandingWakes: &outstandingWakes,
			ResultsSeen:      &resultsSeen,
			SessionID:        currentSessionID,
			SessionStartedAt: currentSessionStartedAt,
			WakesInSession:   func() int { return int(wakesInSession.Load()) },
			Dreaming:         dw.Dreaming,
			NextTurn: func() wakeMeta {
				if m, ok := wq.pop(); ok {
					return m
				}
				return wakeMeta{
					id:         fmt.Sprintf("mailbox-%d", time.Now().UnixNano()),
					reason:     "mailbox",
					wakeDriven: false,
				}
			},
			OnTurnEnd: func(wakeDriven bool) {
				if wakeDriven {
					_ = sessionstate.IncrementWake(opts.SessionStatePath)
					wakesInSession.Add(1)
				}
			},
			TranscriptMaxCount: func() int {
				if cfg.MindForm.TranscriptsMaxCount > 0 {
					return cfg.MindForm.TranscriptsMaxCount
				}
				return 0
			},
			TranscriptMaxBytes: func() int64 {
				if cfg.MindForm.TranscriptsMaxBytes != "" {
					if b, err := config.ParseByteSize(cfg.MindForm.TranscriptsMaxBytes); err == nil && b > 0 {
						return b
					}
				}
				return 0
			},
		})
	}
	var d *Drainer = makeDrainer()

	// ── 10. Forwarder ─────────────────────────────────────────────────────
	fw := NewForwarder(ForwarderConfig{
		ClaudeStdin:      claude.Stdin,
		OutstandingWakes: &outstandingWakes,
		Config:           cfg,
		DreamState:       ds,
		IsDreaming:       dw.Dreaming,
		AppendToBacklog:  dw.AppendToBacklog,
		WakeQueue:        wq,
		FirstWakeFlagGetAndClear: func() bool {
			return firstWakeFlag.CompareAndSwap(true, false)
		},
	})

	// ── 11. Initial agent-state.json snapshot ────────────────────────────
	// Written before the first event so observers see "ready" immediately.
	d.writeSnapshot()

	// ── 12. Start goroutines ──────────────────────────────────────────────
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go func() { _ = dw.Run(subCtx) }()
	go func() { _ = d.Run(subCtx, claude.Stdout) }()
	forwardDone := make(chan error, 1)
	go func() { forwardDone <- fw.Run(subCtx, opts.WakeStdin) }()

	// ── 13. Main supervision loop ─────────────────────────────────────────
	// Handles ctx cancel, rotation requests, forwarder EOF, and claude exit.
	claudeP := claude
	// stderrTail captures the last 16 KiB of claude's stderr so
	// ClassifyClaudeExit can inspect it on exit. A new capture is created
	// for each claude spawn (rotation + session-not-found respawn).
	stderrTail := newStderrCapture(16 << 10)
	go func(sc *stderrCapture, r io.ReadCloser) {
		_, _ = io.Copy(io.MultiWriter(os.Stderr, sc), r)
	}(stderrTail, claude.Stderr)
	for {
		select {
		case <-ctx.Done():
			_ = claudeP.Stdin.Close()
			if claudeP.Cmd.Process != nil {
				_ = syscall.Kill(-claudeP.Cmd.Process.Pid, syscall.SIGTERM)
			}
			return ctx.Err()

		case <-rotateCh:
			rc := RotationConfig{
				SessionStatePath: opts.SessionStatePath,
				StateMachine:     sm,
				IdleWait:         opts.IdleWait,
				CloseGrace:       opts.CloseGrace,
				CloseStdin:       func() error { return claudeP.Stdin.Close() },
				WaitForClaudeExit: func(timeout time.Duration) bool {
					select {
					case <-claudeP.WaitErr:
						return true
					case <-time.After(timeout):
						return false
					}
				},
				Kill: func() {
					if claudeP.Cmd.Process != nil {
						_ = syscall.Kill(-claudeP.Cmd.Process.Pid, syscall.SIGTERM)
						time.Sleep(5 * time.Second)
						_ = syscall.Kill(-claudeP.Cmd.Process.Pid, syscall.SIGKILL)
					}
				},
				SpawnNewClaude: func(newUUID string) error {
					nc, err := SpawnClaude(SpawnOpts{
						Binary:       opts.ClaudeBin,
						Mode:         SessionNew,
						SessionUUID:  newUUID,
						Model:        cfg.MindForm.Model,
						SystemPrompt: systemPrompt,
						Cwd:          opts.OntologyDir,
						ClaudeDir:    opts.ClaudeDir,
						ExtraArgs:    opts.ExtraClaudeArgs,
					})
					if err != nil {
						return err
					}
					claudeP = nc
					firstWakeFlag.Store(true)
					outstandingWakes.Store(0)
					resultsSeen.Store(0)
					// Reset the wake queue so stale wake-ids from the dying
					// session don't bleed into the new claude instance.
					wq.reset()
					// Fresh drainer — old instance's goroutine sees EOF on the
					// closed stdout of the previous claude and returns; its
					// stale curHandle/turnStart state never touches the new process.
					d = makeDrainer()
					go func(stdout io.ReadCloser) { _ = d.Run(subCtx, stdout) }(claudeP.Stdout)
					// Fresh stderr capture for the new process.
					stderrTail = newStderrCapture(16 << 10)
					go func(sc *stderrCapture, r io.ReadCloser) {
						_, _ = io.Copy(io.MultiWriter(os.Stderr, sc), r)
					}(stderrTail, claudeP.Stderr)
					// Point the forwarder at the new claude stdin and drain the dream backlog.
					fw.cfg.ClaudeStdin = claudeP.Stdin
					for _, sig := range dw.DrainBacklog() {
						_ = fw.deliverToClaude(sig)
					}
					dw.SetDreamingFalse()
					return nil
				},
			}
			if err := NewRotation(rc).Rotate(); err != nil {
				return fmt.Errorf("rotation: %w", err)
			}

		case err := <-forwardDone:
			if err != nil && err != context.Canceled {
				return fmt.Errorf("forwarder: %w", err)
			}
			// Forwarder is done (EOF on WakeStdin); keep the main loop alive
			// for ctx.Done / rotation / claude exit. Nil out the channel so
			// we don't busy-loop on the closed receive.
			forwardDone = nil

		case waitErr := <-claudeP.WaitErr:
			verdict := claudeexec.ClassifyClaudeExit(waitErr, claudeP.Cmd.ProcessState, []byte(stderrTail.String()))
			if verdict.Kind == claudeexec.ClaudeAuthRequired {
				if werr := authstate.Write(time.Now()); werr != nil {
					fmt.Fprintf(stderr(), "agent-loop: write authstate: %v\n", werr)
				}
				// Exit with code 47 so the supervisor's classify policy halts restarts.
				return cliExitErr(47, fmt.Errorf("claude requires auth: %w", waitErr))
			}
			if matchSessionNotFound(stderrTail.String()) {
				fmt.Fprintf(stderr(), "agent-loop: claude reports session %s not found; minting fresh\n", currentSessionID())
				if cErr := sessionstate.Clear(opts.SessionStatePath); cErr != nil {
					return fmt.Errorf("clear session after stderr fallback: %w", cErr)
				}
				fresh, mErr := sessionstate.Mint(opts.SessionStatePath, time.Now())
				if mErr != nil {
					return fmt.Errorf("mint session after stderr fallback: %w", mErr)
				}
				firstWakeFlag.Store(true)
				outstandingWakes.Store(0)
				resultsSeen.Store(0)
				// Reset the wake queue so stale wake-ids don't bleed into the respawned process.
				wq.reset()
				nc, sErr := SpawnClaude(SpawnOpts{
					Binary:       opts.ClaudeBin,
					Mode:         SessionNew,
					SessionUUID:  fresh.SessionID,
					Model:        cfg.MindForm.Model,
					SystemPrompt: systemPrompt,
					Cwd:          opts.OntologyDir,
					ClaudeDir:    opts.ClaudeDir,
					ExtraArgs:    opts.ExtraClaudeArgs,
				})
				if sErr != nil {
					return fmt.Errorf("respawn claude with fresh session: %w", sErr)
				}
				claudeP = nc
				// Fresh drainer and stderr capture for the respawned process.
				d = makeDrainer()
				go func(stdout io.ReadCloser) { _ = d.Run(subCtx, stdout) }(claudeP.Stdout)
				stderrTail = newStderrCapture(16 << 10)
				go func(sc *stderrCapture, r io.ReadCloser) {
					_, _ = io.Copy(io.MultiWriter(os.Stderr, sc), r)
				}(stderrTail, claudeP.Stderr)
				continue
			}
			return fmt.Errorf("claude exited: %w", waitErr)
		}
	}
}

// acquireLock takes an exclusive non-blocking flock on path. Creates the
// file (and parent dir) if missing. Returns the locked file so the caller
// can defer releaseLock.
func acquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
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

// releaseLock unlocks and closes the file from acquireLock.
func releaseLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
