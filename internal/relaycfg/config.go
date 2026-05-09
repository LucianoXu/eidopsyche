// Package relaycfg owns the embedded relay's configuration: a TOML
// file rooted at the relay's state directory, distinct from the gate
// config in internal/config. Kept separate so a relay-only build path
// does not import gate-specific types.
package relaycfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const FileName = "config.toml"

type Config struct {
	LogLevel string       `toml:"log_level,omitempty"`
	Relay    RelaySection `toml:"relay"`
}

type RelaySection struct {
	Mode        string      `toml:"mode"`
	Listen      string      `toml:"listen"`
	OwnerPubkey string      `toml:"owner_pubkey,omitempty"`
	TLS         TLSSection  `toml:"tls,omitempty"`
	Auth        AuthSection `toml:"auth,omitempty"`
}

type TLSSection struct {
	CertFile string `toml:"cert_file,omitempty"`
	KeyFile  string `toml:"key_file,omitempty"`
}

type AuthSection struct {
	Required   bool   `toml:"required"`
	ServiceURL string `toml:"service_url,omitempty"`
}

// Defaults returns a Config with the spec-defined defaults applied.
// OwnerPubkey deliberately has no default — `eidos relay init` requires
// --owner when --mode=paired and rejects it when --mode=public.
func Defaults() Config {
	return Config{
		LogLevel: "info",
		Relay: RelaySection{
			Mode:   "paired",
			Listen: "0.0.0.0:7777",
			Auth:   AuthSection{Required: true},
		},
	}
}

// Load reads dir/config.toml, applies spec defaults for any fields the
// file omits, and validates mode/owner consistency.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, FileName)
	cfg := Defaults()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("relaycfg load %s: %w", path, err)
	}
	// Re-apply spec default for Auth.Required if the field was absent.
	if !meta.IsDefined("relay", "auth", "required") {
		cfg.Relay.Auth.Required = true
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes cfg to dir/config.toml (mkdir -p the parent first).
func Save(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("relaycfg save mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("relaycfg save open %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("relaycfg save encode: %w", err)
	}
	return nil
}

// Validate checks the invariants `init` and `start` both rely on.
func (c Config) Validate() error {
	switch c.Relay.Mode {
	case "paired":
		if strings.TrimSpace(c.Relay.OwnerPubkey) == "" {
			return fmt.Errorf("relay.mode=paired requires owner_pubkey")
		}
	case "public":
		if strings.TrimSpace(c.Relay.OwnerPubkey) != "" {
			return fmt.Errorf("relay.mode=public must not set owner_pubkey")
		}
	default:
		return fmt.Errorf("relay.mode must be \"paired\" or \"public\" (got %q)", c.Relay.Mode)
	}
	if strings.TrimSpace(c.Relay.Listen) == "" {
		return fmt.Errorf("relay.listen must be set (host:port)")
	}
	if (c.Relay.TLS.CertFile == "") != (c.Relay.TLS.KeyFile == "") {
		return fmt.Errorf("relay.tls: cert_file and key_file must both be set or both empty")
	}
	return nil
}

// EventStorePath returns the canonical events.db path for a config dir.
func EventStorePath(dir string) string { return filepath.Join(dir, "events.db") }

// DefaultDir returns the conventional relay config dir under the gate's
// XDG_CONFIG_HOME (or $HOME/.config). Stays a sibling of gate's config
// dir so co-located deployments share the parent.
func DefaultDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "eidos", "relay"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "eidos", "relay"), nil
}
