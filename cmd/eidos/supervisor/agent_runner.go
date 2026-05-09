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

	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/spf13/cobra"
)

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

	identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))
	msg := buildWakeMessage(wakePromptInput{
		Reason:               string(sig.Reason),
		Hint:                 sig.Hint,
		InboxUnread:          sig.Context.InboxUnread,
		SinceLastWakeSeconds: sig.Context.SinceLastWakeSeconds,
	})

	c := exec.Command("claude",
		"--append-system-prompt", string(identity),
		"-p", msg,
	)
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

type wakePromptInput struct {
	Reason               string
	Hint                 string
	InboxUnread          int
	SinceLastWakeSeconds int64
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
	return sb.String()
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
