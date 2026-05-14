package config

import (
	"fmt"
	"regexp"
)

// DefaultModel is the claude model alias used when mindform.model is
// empty in /eidos/gate/config.toml. Surfaced via the `--model` flag at
// every claude spawn (agent-loop, supervisor birth, prompt-dump) and
// also as text in the system prompt's Info block.
const DefaultModel = "opus"

// modelIDRegex matches Anthropic's claude model id shape:
// claude-{family}-{numeric-segments}, optionally with a datestamped suffix.
// Examples that match: claude-sonnet-4-7, claude-haiku-4-5,
// claude-opus-4-7, claude-sonnet-4-6-20250101.
//
// We do not maintain a whitelist of known model ids — Anthropic ships new
// models faster than we can chase. The regex catches typos and shell
// metacharacters; runtime gives the operator a clear error on a bona-fide
// unknown id.
var modelIDRegex = regexp.MustCompile(`^claude-(sonnet|haiku|opus)-[0-9]+(-[0-9]+)*$`)

// modelAliasRegex matches the bare claude family aliases that
// `claude --model` itself accepts (e.g. `sonnet`, `opus`, `haiku`).
// Accepting these lets DefaultModel = "opus" round-trip through
// config validation without forcing operators to pin a specific
// numbered version.
var modelAliasRegex = regexp.MustCompile(`^(sonnet|haiku|opus)$`)

// ValidateModelID returns nil for the empty string (meaning "use the
// DefaultModel") and for syntactically plausible model ids or
// recognised family aliases; otherwise it returns an error explaining
// the expected shape.
func ValidateModelID(id string) error {
	if id == "" {
		return nil
	}
	if modelAliasRegex.MatchString(id) {
		return nil
	}
	if !modelIDRegex.MatchString(id) {
		return fmt.Errorf("invalid model id %q: expected the alias sonnet|haiku|opus, or shape claude-{sonnet|haiku|opus}-N[-N...] (e.g. claude-sonnet-4-7)", id)
	}
	return nil
}
