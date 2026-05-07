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
	Relay     RelayConfig     `toml:"relay"`
	Publish   PublishConfig   `toml:"publish"`
	Subscribe SubscribeConfig `toml:"subscribe"`
}

type DaemonConfig struct {
	Socket               string `toml:"socket"`
	ShutdownGraceSeconds int    `toml:"shutdown_grace_seconds"`
}

type RelayConfig struct {
	Mode      string `toml:"mode"`
	Listen    string `toml:"listen"`
	PublicURL string `toml:"public_url"`
	DataDir   string `toml:"data_dir"`
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
		Relay: RelayConfig{
			Mode:      "paired",
			Listen:    "0.0.0.0:22895",
			PublicURL: "ws://127.0.0.1:22895",
			DataDir:   "relay",
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
