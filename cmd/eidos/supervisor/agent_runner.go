//go:build !windows

package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
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
const dreamStateRuntimePath = "/eidos/run/dream-state.json"

// EXIT_AUTH_REQUIRED is the exit code agent-runner uses when Claude's
// /login token is expired or missing. The supervisor surfaces this state
// via `eidos forge status`.
const EXIT_AUTH_REQUIRED = 47

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
	body, err := os.ReadFile(wakeFile)
	if err != nil {
		return fmt.Errorf("read wake file: %w", err)
	}
	var sig wake.Signal
	if err := json.Unmarshal(body, &sig); err != nil {
		return fmt.Errorf("decode wake file: %w", err)
	}

	lockPath := filepath.Join(filepath.Dir(wakeFile), "agent.lock")
	lock, err := acquireAgentLock(lockPath)
	if err != nil {
		return fmt.Errorf("agent lock: %w", err)
	}
	defer releaseAgentLock(lock)

	cfg, _ := config.Load(gateConfigPath)
	ds, _ := dreamstate.Read(dreamStateRuntimePath)
	sig.Context = computeContext(sig, cfg, ds, time.Now())

	identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))
	msg := buildWakeMessage(wakePromptInput{
		Reason:                string(sig.Reason),
		Hint:                  sig.Hint,
		InboxUnread:           sig.Context.InboxUnread,
		SinceLastWakeSeconds:  sig.Context.SinceLastWakeSeconds,
		MasterLikelyAsleep:    sig.Context.MasterLikelyAsleep,
		QuietStart:            cfg.MindForm.QuietStart,
		QuietEnd:              cfg.MindForm.QuietEnd,
		TZ:                    cfg.MindForm.TZ,
		SinceLastDreamSeconds: sig.Context.SinceLastDreamSeconds,
		DreamEligible:         sig.Context.DreamEligible,
		LastDreamNote:         ds.LastDreamNote,
		PlanID:                sig.Context.PlanID,
	})

	// The container is the trust boundary; --dangerously-skip-permissions
	// is the documented sandbox path. Optional --model pins the model
	// id when the operator has set mindform.model. See
	// docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md.
	args := buildClaudeArgs(string(identity), msg, gateConfigPath)
	c := exec.Command("claude", args...)
	c.Dir = ontologyDir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = append(os.Environ(), "CLAUDE_DIR="+filepath.Join(ontologyDir, ".claude"))
	if err := c.Run(); err != nil {
		if isAuthError(err, c.ProcessState) {
			os.Exit(EXIT_AUTH_REQUIRED)
		}
		return fmt.Errorf("claude exited: %w", err)
	}
	return nil
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

type wakePromptInput struct {
	Reason                string
	Hint                  string
	InboxUnread           int
	SinceLastWakeSeconds  int64
	MasterLikelyAsleep    bool
	QuietStart            string
	QuietEnd              string
	TZ                    string
	SinceLastDreamSeconds int64
	DreamEligible         bool
	LastDreamNote         string
	PlanID                string
}

func buildWakeMessage(in wakePromptInput) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You have just woken. Reason: %s.", in.Reason)
	if in.Hint != "" {
		fmt.Fprintf(&sb, " %s.", in.Hint)
	}
	fmt.Fprintf(&sb, " Inbox has %d unread message(s).", in.InboxUnread)
	if in.SinceLastWakeSeconds > 0 {
		fmt.Fprintf(&sb, " %ds since last wake.", in.SinceLastWakeSeconds)
	}
	if in.MasterLikelyAsleep && in.QuietStart != "" {
		fmt.Fprintf(&sb, " Master is likely asleep (quiet hours %s–%s%s).",
			in.QuietStart, in.QuietEnd, tzSuffix(in.TZ))
	}
	if in.SinceLastDreamSeconds > 0 {
		hours := in.SinceLastDreamSeconds / 3600
		fmt.Fprintf(&sb, " %dh since your last dream.", hours)
	}
	if in.DreamEligible && in.SinceLastDreamSeconds > 0 {
		fmt.Fprintf(&sb, " You are eligible to dream now.")
	}
	if in.LastDreamNote != "" {
		fmt.Fprintf(&sb, " Last dream: %q.", in.LastDreamNote)
	}
	if in.PlanID != "" {
		fmt.Fprintf(&sb, " (Planned wake; plan id %s.)", in.PlanID)
	}
	return sb.String()
}

func tzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return ", " + tz
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

// buildClaudeArgs constructs the argv passed to `claude` for one wake.
// Reads the mind-form config to pick up an optional model pin.
//
// Config-load errors are tolerated: a missing or malformed config.toml
// drops us back to claude's subscription default rather than bricking
// the wake. Once a config loads, the model id is passed through
// verbatim — host-side commands (forge create / forge config) validate
// the id; a stale / hand-edited config with an unknown id surfaces at
// the next wake when claude itself rejects it.
func buildClaudeArgs(identity, msg, configPath string) []string {
	args := []string{
		"--append-system-prompt", identity,
		"--dangerously-skip-permissions",
	}
	if cfg, err := config.Load(configPath); err == nil {
		if model := cfg.MindForm.Model; model != "" {
			args = append(args, "--model", model)
		}
	}
	args = append(args, "-p", msg)
	return args
}

// isAuthError detects Claude's auth-required exit conditions. Heuristic:
// match exit codes commonly associated with credential failures. This will
// evolve as we learn Claude Code's exact exit-code convention.
func isAuthError(err error, st *os.ProcessState) bool {
	if st == nil {
		_ = err
		return false
	}
	if status, ok := st.Sys().(syscall.WaitStatus); ok {
		code := status.ExitStatus()
		return code == EXIT_AUTH_REQUIRED || code == 41
	}
	return false
}
