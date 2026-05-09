package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// detectV04State returns a non-nil error when stateDir contains a
// config.toml from a v0.4-or-earlier eidos install. Returns nil when the
// file is missing (fresh install) or when the v0.5+ marker is present.
//
// v0.5+ marker: the [daemon].socket field, written unconditionally by the
// post-v0.5 `eidos gate init`. Pre-v0.5 configs had a [relay].mode/listen
// block instead. The previous marker [relay].enabled was tied to the
// embedded-relay opt-in that the v0.9 relay-top-level decoupling removed,
// so it is no longer written by fresh installs and produced a false
// positive against every new v0.5+ state dir.
//
// Detection uses BurntSushi/toml MetaData.IsDefined because Go's
// zero-value cannot otherwise distinguish "field missing" from "field
// present and zero".
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
	if meta.IsDefined("daemon", "socket") {
		return nil
	}
	return fmt.Errorf(`this state directory was created by an older eidos version (pre-v0.5).

v0.5 changes how the gate is initialized: the embedded relay is now opt-in,
and 'eidos gate init' requires --home <url>.

Choose one:
  (A) Re-init from scratch (loses contacts, invites, inbox history):
        eidos gate purge --yes
        eidos gate init --label <your-label> --home <url>

  (B) Migrate in place (keeps state):
        See docs/INSTALL.md#migrating-from-v04 for the SQL + config recipe.

state directory: %s`, stateDir)
}
