package update

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// SkipReason returns a non-empty string identifying why the update flow
// should be a no-op for this invocation, or "" when the flow may proceed.
// Layered checks (in declared order):
//
//  1. Developer build sentinel — Version "dev" or empty
//  2. EIDOS_NO_UPDATE_CHECK environment variable (any truthy value)
//  3. CI environment variable set (matches GitHub Actions, GitLab CI, etc.)
//  4. ~/.config/eidos/config.toml has [update] check = false
func SkipReason(b BuildInfo) string {
	v := strings.TrimSpace(b.Version)
	if v == "" || v == "dev" {
		return "dev build"
	}
	if isTruthy(os.Getenv("EIDOS_NO_UPDATE_CHECK")) {
		return "EIDOS_NO_UPDATE_CHECK"
	}
	if os.Getenv("CI") != "" {
		return "CI"
	}
	if isConfigDisabled() {
		return "config"
	}
	return ""
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// isConfigDisabled reads ~/.config/eidos/config.toml (honouring
// XDG_CONFIG_HOME) and returns true when [update] check = false. Any
// read/parse failure is treated as "not disabled" — the safe default.
func isConfigDisabled() bool {
	p := userConfigPath()
	if p == "" {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var cfg struct {
		Update struct {
			Check *bool `toml:"check"`
		} `toml:"update"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return false
	}
	return cfg.Update.Check != nil && !*cfg.Update.Check
}

func userConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "eidos", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "eidos", "config.toml")
}
