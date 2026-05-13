//go:build !windows

package supervisor

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// bornAtRel is the path inside the ontology dir that the agent writes
// at the end of a successful birth. Its presence is the supervisor's
// authoritative signal that birth has happened — once it exists, any
// stale birth.json is silently cleared.
const bornAtRel = "self/born_at"

// BirthHandler runs the agent for a birth-wake. The handler is
// responsible for invoking claude with the right context and verifying
// the agent wrote self/born_at. drainBirthIfPresent uses born_at as
// the sole authoritative idempotency signal.
type BirthHandler func(ctx context.Context, sig wake.BirthSignal, ontologyDir string) error

// birthHandlerForProduction is the BirthHandler used in production.
// Tests substitute a fake by reassigning this variable inside
// build-tagged test files.
var birthHandlerForProduction BirthHandler = productionBirthHandler

// drainBirthIfPresent is the integration point: if <wakeDir>/birth.json
// exists AND <ontologyDir>/self/born_at is absent, run the handler.
// On success, delete birth.json. On stale-found-born_at, delete birth.json
// without invoking the handler. On handler error, leave birth.json so
// the next supervisor iteration retries.
func drainBirthIfPresent(ctx context.Context, wakeDir, ontologyDir string, handler BirthHandler) error {
	sig, err := wake.ReadBirth(wakeDir)
	if err != nil {
		return fmt.Errorf("read birth: %w", err)
	}
	if sig == nil {
		return nil
	}
	bornAtPath := filepath.Join(ontologyDir, bornAtRel)
	if _, err := os.Stat(bornAtPath); err == nil {
		log.Printf("supervisor: stale birth.json with %s present; clearing", bornAtRel)
		return wake.ClearBirth(wakeDir)
	}
	if err := handler(ctx, *sig, ontologyDir); err != nil {
		log.Printf("birth handler: %v (will retry next iteration)", err)
		return err
	}
	return wake.ClearBirth(wakeDir)
}

// productionBirthHandler reads the summoning book and calling-words
// from the paths in birth.json, builds the agent's prompt
// (constitution-augmented identity + birth boot prompt as the system
// message; book + calling-words as the user prompt), and runs claude.
// Verifies the agent set self/born_at — if not, returns an error so
// the supervisor's next iteration retries.
//
// Self-gates on /eidos/run/auth_required.json: if claude auth has
// failed previously (either in a normal wake or a prior birth attempt),
// don't invoke claude again — return an error that drainBirthIfPresent
// keeps birth.json around for, but the operator gets a clear forge-
// status signal and isn't billed for failed retries.
func productionBirthHandler(ctx context.Context, sig wake.BirthSignal, ontologyDir string) error {
	if state, err := authstate.Read(); err == nil && state != nil {
		return fmt.Errorf(
			"birth handler: claude auth required (since %s); run `eidos forge login <slug>` to clear %s",
			time.Unix(state.Since, 0).UTC().Format(time.RFC3339), authstate.Path)
	}

	// Verify the on-disk summoning book and calling-words exist; the
	// birth.txt user-prompt instructs the agent to Read them via tool
	// rather than inlining them, so we only need a precondition check.
	if _, err := os.Stat(sig.SummoningBookPath); err != nil {
		return fmt.Errorf("summoning book missing at %s: %w", sig.SummoningBookPath, err)
	}
	if _, err := os.Stat(sig.CallingWordsPath); err != nil {
		return fmt.Errorf("calling-words missing at %s: %w", sig.CallingWordsPath, err)
	}
	// role-research.md is the dramaturge dossier the wizard wrote
	// (scratch path: claude-generated; prefab path: shipped in the
	// prefab tree). The birth boot prompt instructs the agent to
	// read it as the first file; refuse to spawn claude if it is
	// missing rather than burn a session on an empty file.
	roleResearchPath := filepath.Join(ontologyDir, "self", "role-research.md")
	if _, err := os.Stat(roleResearchPath); err != nil {
		return fmt.Errorf("role-research missing at %s: %w", roleResearchPath, err)
	}

	// Identity facts come from self/identity.toml; the scaffold wrote
	// them before birth.json was placed.
	facts, err := prompts.FromOntology(ontologyDir)
	if err != nil {
		return fmt.Errorf("read identity facts: %w", err)
	}
	facts.OntologyDir = ontologyDir
	facts.Effort = config.DefaultEffort
	if cfg, err := config.Load(agentLoopGateConfigPath); err == nil {
		facts.Model = cfg.MindForm.Model
		if cfg.MindForm.Effort != "" {
			facts.Effort = cfg.MindForm.Effort
		}
	}

	systemPrompt, err := prompts.Build(ctx, facts, ontologyDir)
	if err != nil {
		return fmt.Errorf("build system prompt: %w", err)
	}
	userPrompt, err := prompts.BirthUser(facts.OwnerLabel)
	if err != nil {
		return fmt.Errorf("build birth user prompt: %w", err)
	}
	args := []string{
		"--system-prompt", systemPrompt,
		"--dangerously-skip-permissions",
	}
	if facts.Model != "" {
		args = append(args, "--model", facts.Model)
	}
	args = append(args, "-p", userPrompt)

	c := exec.Command("claude", args...)
	c.Dir = ontologyDir
	c.Stdout = os.Stdout
	stderrBuf := &strings.Builder{}
	c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
	c.Env = claudeSpawnEnv(filepath.Join(ontologyDir, ".claude"))
	if err := c.Run(); err != nil {
		// Match the agent-loop's auth-failure handling: persist the
		// auth_required marker so forge status surfaces it and the
		// supervisor stops retrying birth.json on every iteration. The
		// birth handler itself doesn't exit; drainBirthIfPresent treats
		// the returned error as retry-this-iteration, but the
		// agent-loop's own self-gate (internal/agentloop) similarly
		// short-circuits subsequent wakes on this marker — and the
		// host operator sees phase: auth-required in
		// `eidos forge status <slug>`.
		v := claudeexec.ClassifyClaudeExit(err, c.ProcessState, []byte(stderrBuf.String()))
		if v.Kind == claudeexec.ClaudeAuthRequired {
			if werr := authstate.Write(time.Now()); werr != nil {
				log.Printf("birth handler: write %s: %v", authstate.Path, werr)
			}
		}
		return fmt.Errorf("claude (birth) exited: %w", err)
	}
	if _, err := os.Stat(filepath.Join(ontologyDir, bornAtRel)); err != nil {
		return fmt.Errorf("agent did not write %s: %w", bornAtRel, err)
	}
	// best-effort: stamp triggered_at into born_at if the agent wrote
	// nothing parseable — the file's existence is what matters but a
	// timestamp is friendlier to inspect.
	_ = stampBornAtIfBlank(filepath.Join(ontologyDir, bornAtRel))
	return nil
}

func stampBornAtIfBlank(path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(body) > 0 {
		return nil
	}
	return os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o600)
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
		log.Printf("birth: read setup-token: %v (falling through to .credentials.json)", err)
	}
	return env
}
