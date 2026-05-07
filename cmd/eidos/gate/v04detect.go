package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// detectV04State returns a non-nil error when stateDir contains a
// config.toml that lacks the [relay].enabled field — the unambiguous
// signal of a v0.4-or-earlier state directory. Returns nil when the file
// is missing (fresh install) or when [relay].enabled is present (v0.5+).
//
// Detection uses BurntSushi/toml MetaData.IsDefined because Go's bool
// zero-value cannot otherwise distinguish "field missing" from "field
// present and false".
func detectV04State(stateDir string) error {
	path := filepath.Join(stateDir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	_, meta, err := config.LoadWithMeta(path)
	if err != nil {
		return fmt.Errorf("read config.toml: %w", err)
	}
	if meta.IsDefined("relay", "enabled") {
		return nil
	}
	return fmt.Errorf(`this state directory was created by an older eidos version (pre-v0.5).

v0.5 changes how the gate is initialized: the embedded relay is now opt-in,
and 'eidos gate init' requires --home <url>.

Choose one:
  (A) Re-init from scratch (loses contacts, invites, inbox history):
        eidos gate purge --yes
        eidos gate init --label <your-label> --home <url> [--with-local-relay]

  (B) Migrate in place (keeps state):
        See docs/INSTALL.md#migrating-from-v04 for the SQL + config recipe.

state directory: %s`, stateDir)
}
