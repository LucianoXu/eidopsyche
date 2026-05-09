package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	StateDir string `toml:"state_dir"`
	LogLevel string `toml:"log_level"`

	Daemon    DaemonConfig    `toml:"daemon"`
	Publish   PublishConfig   `toml:"publish"`
	Subscribe SubscribeConfig `toml:"subscribe"`
	Dashboard DashboardConfig `toml:"dashboard"`
	Wake      WakeConfig      `toml:"wake"`
	MindForm  MindFormConfig  `toml:"mindform"`
}

// MindFormConfig is the in-container gate's mind-form-runtime settings.
// Lives in /eidos/gate/config.toml; host gates leave MindForm at its
// zero value because the [mindform] block is absent from their config.
type MindFormConfig struct {
	// Model pins the claude model used by agent-runner. Empty = let
	// claude pick its subscription default. The mindform.model
	// registry key validates writes via ValidateModelID; Load tolerates
	// anything so a hand-edited config.toml with an unknown id
	// surfaces at the next wake when claude rejects it, not at
	// gate-daemon startup.
	Model string `toml:"model"`
}

// WakeConfig controls the optional wake-signal output used by the in-container
// gate daemon. When Dir is non-empty the daemon writes a wake signal to
// Dir/pending.json after each persisted inbound message so the supervisor's
// wake-watch loop can spawn the agent. Host-side gate instances leave Dir
// empty (the default) and the hook is a no-op.
type WakeConfig struct {
	Dir string `toml:"dir"`
}

type DashboardConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"`
}

type DaemonConfig struct {
	Socket               string `toml:"socket"`
	ShutdownGraceSeconds int    `toml:"shutdown_grace_seconds"`
}

type PublishConfig struct {
	FallbackRelays []string `toml:"fallback_relays"`
}

type SubscribeConfig struct {
	ExtraRelays []string `toml:"extra_relays"`
}

func Defaults() Config {
	return Config{
		LogLevel: "info",
		Daemon: DaemonConfig{
			Socket:               "sock",
			ShutdownGraceSeconds: 5,
		},
		Dashboard: DashboardConfig{
			Enabled: true,
			Listen:  "127.0.0.1:22893",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadWithMeta is Load + the BurntSushi/toml MetaData so callers can
// distinguish "field absent" from "field present with zero value". Used by
// v0.4 state-directory detection in cmd/eidos/gate.
func LoadWithMeta(path string) (Config, toml.MetaData, error) {
	cfg := Defaults()
	meta, err := toml.DecodeFile(path, &cfg)
	return cfg, meta, err
}

func Save(path string, cfg Config) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// ResolveStateDir picks the gate state directory using precedence:
// flag (passed in) > $EIDOS_GATE_HOME > $XDG_STATE_HOME/eidos/gate > $HOME/.eidos/gate.
//
// The state directory holds the gate's identity key, SQLite metadata, embedded
// relay data, and IPC socket. Each invocation of `eidos gate` resolves the
// same directory; multiple personas on one host run by setting different
// $EIDOS_GATE_HOME values or passing distinct --state-dir flags.
func ResolveStateDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if v := os.Getenv("EIDOS_GATE_HOME"); v != "" {
		return filepath.Abs(v)
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Abs(filepath.Join(v, "eidos", "gate"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".eidos", "gate"), nil
}
