package config

import (
	"fmt"
	"slices"
)

// DefaultEffort is the reasoning-effort applied when mindform.effort is
// empty in /eidos/gate/config.toml. Drives the `--effort` flag passed
// to the claude CLI at spawn, and is also surfaced to the mind-form
// as text via the system prompt's Info block.
const DefaultEffort = "medium"

// ValidEfforts are the accepted reasoning-effort levels for
// mindform.effort. Mirrors `claude --effort` exactly so writes that
// pass validation are guaranteed to be accepted by the CLI at spawn.
var ValidEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// ValidateEffort returns nil for the empty string (meaning "use the
// DefaultEffort") and for any value in ValidEfforts; otherwise it
// returns an explanatory error.
func ValidateEffort(v string) error {
	if v == "" {
		return nil
	}
	if slices.Contains(ValidEfforts, v) {
		return nil
	}
	return fmt.Errorf("invalid effort %q: expected one of %v (or empty for %q)", v, ValidEfforts, DefaultEffort)
}
