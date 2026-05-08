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
	Dashboard DashboardConfig `toml:"dashboard"`
}

type DashboardConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"`
}

type DaemonConfig struct {
	Socket               string `toml:"socket"`
	ShutdownGraceSeconds int    `toml:"shutdown_grace_seconds"`
}

type RelayConfig struct {
	Enabled bool            `toml:"enabled"`
	Mode    string          `toml:"mode"`
	Listen  string          `toml:"listen"`
	DataDir string          `toml:"data_dir"`
	Auth    RelayAuthConfig `toml:"auth"`
	TLS     RelayTLSConfig  `toml:"tls"`
}

// RelayAuthConfig governs NIP-42 AUTH enforcement on the embedded relay.
// Required defaults to true (NIP-17 §Recommendations). ServiceURL overrides
// khatru's auto-derived URL — useful when the relay sits behind a reverse
// proxy whose Host header doesn't reflect the externally-reachable URL.
type RelayAuthConfig struct {
	Required   bool   `toml:"required"`
	ServiceURL string `toml:"service_url"`
}

// RelayTLSConfig enables native TLS in the relay process. Both fields must
// be set to take effect; setting only one is a startup error. No autocert
// in this release — cert lifecycle is BYO (certbot, manual, etc.) and the
// relay reloads the cert only on restart.
type RelayTLSConfig struct {
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

// RelayEnabled reports whether the embedded relay should run on this host.
// Single source of truth: every code path that asks "should I spin up the
// relay" routes through this method.
func (c Config) RelayEnabled() bool { return c.Relay.Enabled }

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
			Enabled: false,
			Mode:    "paired",
			Listen:  "0.0.0.0:22895",
			DataDir: "relay",
			Auth:    RelayAuthConfig{Required: true},
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
