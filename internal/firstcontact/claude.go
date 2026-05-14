package firstcontact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ClaudeRunner runs claude with the given args, returning stdout. The
// production runner is exec.CommandContext-based; tests substitute a
// closure that returns canned bytes so the wizard's logic is exercised
// without spawning a real claude binary.
type ClaudeRunner func(ctx context.Context, args []string) (stdout string, err error)

// Claude is the wizard's wrapper around the host's `claude` binary.
type Claude struct {
	Run     ClaudeRunner
	Model   string        // optional override; defaults to ClaudeModel
	Timeout time.Duration // per-call hard timeout; 0 → 90s
}

func (c *Claude) timeout() time.Duration {
	if c.Timeout == 0 {
		return 90 * time.Second
	}
	return c.Timeout
}

func (c *Claude) model() string {
	if c.Model == "" {
		return ClaudeModel
	}
	return c.Model
}

// args builds the argv for the wizard's one-shot research / text call.
// Deliberately omits --tools: this is `-p --output-format json` with
// no tool loop, so internal/claudeexec.Allowed does not apply here.
// See docs/superpowers/specs/2026-05-14-mindform-tool-allowlist-design.md
// §"Non-Goals: first-contact research call".
func (c *Claude) args(prompt string) []string {
	return []string{"-p", "--model", c.model(), "--output-format", "json", prompt}
}

// claudeJSONResult is the envelope `claude --output-format json` produces.
type claudeJSONResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// Call runs claude and parses the result string into schema (a non-nil
// pointer). On JSON-parse failure of the result string, retries once.
// Used for the research call (typed CharacterProfile).
func (c *Claude) Call(ctx context.Context, prompt string, schema any) error {
	if schema == nil {
		return errors.New("schema must be a non-nil pointer; use CallText for plain text")
	}
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := c.runOnce(ctx, prompt)
		if err != nil {
			return err
		}
		// Tolerate markdown code-fence wrapping. Despite "return ONLY the
		// JSON object" in the prompt, claude often emits ```json ... ```;
		// the model is unreliable about this so it must be the parser's
		// job, not the prompt's.
		stripped := stripCodeFence(raw)
		if jerr := json.Unmarshal([]byte(stripped), schema); jerr == nil {
			return nil
		} else if attempt == 1 {
			return fmt.Errorf("claude returned non-JSON result after retry: %w (got %q)", jerr, truncate(raw, 200))
		}
	}
	return errors.New("unreachable")
}

// stripCodeFence removes a leading ```lang and trailing ``` if the input
// is wrapped in a markdown code fence. Tolerates surrounding whitespace
// and a missing language tag (`+\n`).
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	// Skip the opening fence line ("```json", "```", etc.) up to the first newline.
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[i+1:]
	} else {
		// No newline — just a "```" with no body. Nothing to strip.
		return s
	}
	t = strings.TrimRight(t, " \t\r\n")
	t = strings.TrimSuffix(t, "```")
	return strings.TrimSpace(t)
}

// CallText runs claude and returns the result string verbatim. Used for
// the displaying paragraph and the calling-words.
func (c *Claude) CallText(ctx context.Context, prompt string) (string, error) {
	return c.runOnce(ctx, prompt)
}

func (c *Claude) runOnce(ctx context.Context, prompt string) (string, error) {
	timed, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	out, err := c.Run(timed, c.args(prompt))
	if err != nil {
		if errors.Is(timed.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("claude timeout after %s", c.timeout())
		}
		return "", fmt.Errorf("claude exec: %w", err)
	}
	var env claudeJSONResult
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		return "", fmt.Errorf("claude envelope parse: %w (got %q)", err, truncate(out, 200))
	}
	if env.IsError {
		return "", fmt.Errorf("claude reported error: %s", env.Result)
	}
	if strings.TrimSpace(env.Result) == "" {
		return "", errors.New("claude returned empty result")
	}
	return env.Result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// ProductionRunner is the real exec-based ClaudeRunner. Used outside
// tests by cmd/eidos/summon.
func ProductionRunner(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
