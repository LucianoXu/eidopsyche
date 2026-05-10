//go:build !windows

package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// birthPromptPath is the in-image location of the birth boot prompt.
// Distinct from the heartbeat / mindgate prompts: the birth-wake hands
// the agent the summoning book + calling-words and asks it to write
// identity, secret, response, and born_at in that order.
const birthPromptPath = "/usr/local/lib/eidos/prompts/birth.txt"

// bornAtRel is the path inside the ontology dir that the agent writes
// at the end of a successful birth. Its presence is the supervisor's
// authoritative signal that birth has happened — once it exists, any
// stale birth.json is silently cleared.
const bornAtRel = "essence/born_at"

// BirthHandler runs the agent for a birth-wake. The handler is
// responsible for invoking claude with the right context and verifying
// the agent wrote essence/born_at. drainBirthIfPresent uses born_at as
// the sole authoritative idempotency signal.
type BirthHandler func(ctx context.Context, sig wake.BirthSignal, ontologyDir string) error

// birthHandlerForProduction is the BirthHandler used in production.
// Tests substitute a fake by reassigning this variable inside
// build-tagged test files.
var birthHandlerForProduction BirthHandler = productionBirthHandler

// drainBirthIfPresent is the integration point: if <wakeDir>/birth.json
// exists AND <ontologyDir>/essence/born_at is absent, run the handler.
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
// Verifies the agent set essence/born_at — if not, returns an error so
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

	prompt, err := os.ReadFile(birthPromptPath)
	if err != nil {
		return fmt.Errorf("read birth prompt: %w", err)
	}
	bookBody, err := os.ReadFile(sig.SummoningBookPath)
	if err != nil {
		return fmt.Errorf("read summoning book: %w", err)
	}
	wordsBody, err := os.ReadFile(sig.CallingWordsPath)
	if err != nil {
		return fmt.Errorf("read calling-words: %w", err)
	}
	identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))
	systemPrompt := string(identity) + "\n\n" + string(prompt)
	userPrompt := fmt.Sprintf(
		"Operator npub: %s\n\n--- summoning book ---\n%s\n\n--- calling-words ---\n%s\n",
		sig.OperatorNpub, string(bookBody), string(wordsBody),
	)
	args := []string{
		"--append-system-prompt", systemPrompt,
		"--dangerously-skip-permissions",
	}
	if cfg, err := config.Load(gateConfigPath); err == nil {
		if model := cfg.MindForm.Model; model != "" {
			args = append(args, "--model", model)
		}
	}
	args = append(args, "-p", userPrompt)

	c := exec.Command("claude", args...)
	c.Dir = ontologyDir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = append(os.Environ(), "CLAUDE_DIR="+filepath.Join(ontologyDir, ".claude"))
	if err := c.Run(); err != nil {
		// Mirror agent_runner's behaviour: detect auth failures and
		// persist the auth_required marker so forge status surfaces
		// it and the supervisor stops retrying birth.json on every
		// iteration. The wake-loop birth-handler itself doesn't exit;
		// drainBirthIfPresent will treat the returned error as
		// retry-this-iteration, but agent-runner's self-gate now
		// short-circuits any subsequent wake until login clears the
		// marker — and the host operator sees auth: REQUIRED in
		// `eidos forge status <slug>`.
		if isAuthError(err, c.ProcessState) {
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
