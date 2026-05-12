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
	Heartbeat HeartbeatConfig `toml:"heartbeat"`
}

// HeartbeatConfig is the in-container gate's mind-form heartbeat cadence.
// Lives in /eidos/gate/config.toml. Host gates leave Heartbeat at its
// zero value because the [heartbeat] block is absent from their config.
type HeartbeatConfig struct {
	// Interval is a Go duration string like "4h" or "30m". Empty = use
	// DefaultHeartbeatInterval. Must be in the supported set; see
	// ValidateHeartbeatInterval.
	Interval string `toml:"interval"`
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

	// Quiet hours feed agent-runner's wake-context computation: when
	// [now in TZ] falls in [QuietStart, QuietEnd), the wake context
	// surfaces master_likely_asleep=true. Both must be set or neither.
	// HH:MM 24-hour form; wrap-around (start > end) is supported.
	QuietStart string `toml:"quiet_start"`
	QuietEnd   string `toml:"quiet_end"`
	TZ         string `toml:"tz"`

	// DreamMinInterval is the floor for "you may dream now". Empty =
	// DefaultDreamMinInterval (12h). A Go duration ≥ 1h.
	DreamMinInterval string `toml:"dream_min_interval"`

	// TranscriptsMaxCount caps the number of per-wake transcript files
	// kept under /eidos/run/transcripts/. Zero = transcript.DefaultMaxCount.
	// Whichever of TranscriptsMaxCount and TranscriptsMaxBytes triggers
	// first prunes the oldest wake.
	TranscriptsMaxCount int `toml:"transcripts_max_count"`

	// TranscriptsMaxBytes caps the total size of per-wake transcript
	// files. Empty = transcript.DefaultMaxBytes (100MB). Accepts plain
	// bytes ("10485760") or K/M/G suffixes ("100MB", "5G").
	TranscriptsMaxBytes string `toml:"transcripts_max_bytes"`

	// DreamIdleWait is the max time to wait for claude to reach a
	// clean turn boundary (state machine = idle) after dream-end fires,
	// before forcing rotation. Empty falls back to a hardcoded default
	// (5m today). Stored as a duration string.
	DreamIdleWait string `toml:"dream_idle_wait"`

	// DreamCloseGrace is the max time to wait for claude to exit
	// cleanly after stdin close during rotation. After grace+5s the
	// process is SIGKILLed. Empty falls back to a hardcoded default
	// (60s today). Stored as a duration string.
	DreamCloseGrace string `toml:"dream_close_grace"`
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
