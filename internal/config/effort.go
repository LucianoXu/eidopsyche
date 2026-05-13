package config

import "fmt"

// DefaultEffort is the reasoning-effort applied when mindform.effort is
// empty in /eidos/gate/config.toml. Surfaced to the mind-form via the
// system prompt's Info block.
const DefaultEffort = "medium"

// ValidEfforts are the accepted reasoning-effort levels for
// mindform.effort. Mirrors the Anthropic reasoning_effort knob.
var ValidEfforts = []string{"low", "medium", "high"}

// ValidateEffort returns nil for the empty string (meaning "use the
// DefaultEffort") and for any value in ValidEfforts; otherwise it
// returns an explanatory error.
func ValidateEffort(v string) error {
	if v == "" {
		return nil
	}
	for _, e := range ValidEfforts {
		if v == e {
			return nil
		}
	}
	return fmt.Errorf("invalid effort %q: expected one of %v (or empty for %q)", v, ValidEfforts, DefaultEffort)
}
