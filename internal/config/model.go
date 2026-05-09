package config

import (
	"fmt"
	"regexp"
)

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

// ValidateModelID returns nil for the empty string (meaning "use claude's
// default") and for syntactically plausible model ids; otherwise it
// returns an error explaining the expected shape.
func ValidateModelID(id string) error {
	if id == "" {
		return nil
	}
	if !modelIDRegex.MatchString(id) {
		return fmt.Errorf("invalid model id %q: expected shape claude-{sonnet|haiku|opus}-N[-N...] (e.g. claude-sonnet-4-7)", id)
	}
	return nil
}
