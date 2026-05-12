package config

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Context identifies in which daemon context a config key is valid.
// The host gate daemon and the in-container PID-1 share most keys, but
// a few (e.g. heartbeat.interval, mindform.model) only make sense
// inside a mindform — the Mutate framework rejects mismatched writes
// with CONTEXT_MISMATCH and points the operator to the correct verb.
type Context uint8

const (
	HostCtx      Context = 1 << 0
	ContainerCtx Context = 1 << 1
	BothCtx              = HostCtx | ContainerCtx
)

// Key describes a single user-settable scalar config option exposed by
// `eidos gate config get/set` and the dashboard's Settings → Config tab.
//
// Both surfaces share this registry so a key documented or validated in
// one place automatically appears in the other. Adding a new scalar
// option is a single registration.
type Key struct {
	// Path is the dotted TOML path, e.g. "relay.mode".
	Path string
	// Description is a one-line operator-facing explanation.
	Description string
	// Get returns the current value of this key as a printable string.
	Get func(*Config) string
	// Set parses value and writes it onto cfg. Returns an error with a
	// human-readable message on bad input; the caller is responsible for
	// surfacing it (CLI prints to stderr, dashboard renders inline).
	Set func(*Config, string) error
	// Contexts is a bitmask of daemon contexts in which this key is
	// settable. Keys that only make sense in one context (e.g.
	// heartbeat.interval inside a mindform container) declare it here so
	// the Mutate framework can short-circuit attempts to write them from
	// the wrong side with a helpful CONTEXT_MISMATCH error.
	Contexts Context
}

// keys is the registry of supported scalar config keys. The order in which
// keys are inserted does not matter; KeyList returns them sorted by Path.
var keys = map[string]Key{}

func register(k Key) {
	if _, dup := keys[k.Path]; dup {
		panic("config: duplicate key " + k.Path)
	}
	keys[k.Path] = k
}

// KeyByPath looks up the key descriptor for a dotted path.
// Returns ok=false when the path is not registered.
func KeyByPath(path string) (Key, bool) {
	k, ok := keys[path]
	return k, ok
}

// KeyList returns every registered key sorted by Path. Use this to drive
// the CLI's "list everything" output and the dashboard's Settings → Config
// table.
func KeyList() []Key {
	out := make([]Key, 0, len(keys))
	for _, k := range keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func init() {
	register(Key{
		Path:        "log_level",
		Description: "Daemon log verbosity (debug, info, warn, error).",
		Contexts:    BothCtx,
		Get:         func(c *Config) string { return c.LogLevel },
		Set: func(c *Config, v string) error {
			v = strings.ToLower(strings.TrimSpace(v))
			switch v {
			case "debug", "info", "warn", "error":
				c.LogLevel = v
				return nil
			default:
				return fmt.Errorf(`log_level must be one of debug|info|warn|error, got %q`, v)
			}
		},
	})
	register(Key{
		Path:        "daemon.socket",
		Description: "Filename of the daemon's IPC unix socket inside the state directory.",
		Contexts:    BothCtx,
		Get:         func(c *Config) string { return c.Daemon.Socket },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v == "" {
				return fmt.Errorf("daemon.socket must not be empty")
			}
			c.Daemon.Socket = v
			return nil
		},
	})
	register(Key{
		Path:        "daemon.shutdown_grace_seconds",
		Description: "Seconds to wait for in-flight requests before forcing daemon shutdown.",
		Contexts:    BothCtx,
		Get:         func(c *Config) string { return strconv.Itoa(c.Daemon.ShutdownGraceSeconds) },
		Set: func(c *Config, v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("daemon.shutdown_grace_seconds requires an integer, got %q", v)
			}
			if n < 0 {
				return fmt.Errorf("daemon.shutdown_grace_seconds must be non-negative, got %d", n)
			}
			c.Daemon.ShutdownGraceSeconds = n
			return nil
		},
	})
	register(Key{
		Path:        "dashboard.enabled",
		Description: "Run the embedded local web dashboard alongside the daemon.",
		Contexts:    BothCtx,
		Get: func(c *Config) string {
			if c.Dashboard.Enabled {
				return "true"
			}
			return "false"
		},
		Set: func(c *Config, v string) error {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "on":
				c.Dashboard.Enabled = true
			case "false", "0", "no", "off":
				c.Dashboard.Enabled = false
			default:
				return fmt.Errorf(`dashboard.enabled must be true or false, got %q`, v)
			}
			return nil
		},
	})
	register(Key{
		Path:        "dashboard.listen",
		Description: "host:port for the dashboard webui. Loopback-only is enforced; non-loopback addresses are refused.",
		Contexts:    BothCtx,
		Get:         func(c *Config) string { return c.Dashboard.Listen },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v == "" {
				return fmt.Errorf("dashboard.listen must be a non-empty host:port; to disable the dashboard, set dashboard.enabled = false instead")
			}
			host, _, err := net.SplitHostPort(v)
			if err != nil {
				return fmt.Errorf("dashboard.listen must be host:port, got %q: %w", v, err)
			}
			if !isLoopbackHost(host) {
				return fmt.Errorf("dashboard.listen %q is not loopback; the daemon refuses to expose the dashboard on non-loopback addresses (e.g. 0.0.0.0 or a public IP). Use 127.0.0.1 or [::1]", v)
			}
			c.Dashboard.Listen = v
			return nil
		},
	})
	register(Key{
		Path:        "mindform.model",
		Description: "Pin the claude model used by agent-loop (e.g. claude-sonnet-4-7). Empty lets claude pick its subscription default.",
		Contexts:    ContainerCtx,
		Get:         func(c *Config) string { return c.MindForm.Model },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if err := ValidateModelID(v); err != nil {
				return err
			}
			c.MindForm.Model = v
			return nil
		},
	})
	register(Key{
		Path:        "heartbeat.interval",
		Description: "Mind-form heartbeat cadence. Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h. Empty uses the 2h default. Hot-reloaded by the apply hook framework — no container restart needed.",
		Contexts:    ContainerCtx,
		Get:         func(c *Config) string { return c.Heartbeat.Interval },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if err := ValidateHeartbeatInterval(v); err != nil {
				return err
			}
			c.Heartbeat.Interval = v
			return nil
		},
	})
	register(Key{
		Path:        "mindform.dream_idle_wait",
		Description: "Max wait for claude to reach idle after dream-end before forcing rotation. Examples: 5m, 1m, 30s. Empty uses the 5m default.",
		Contexts:    ContainerCtx,
		Get:         func(c *Config) string { return c.MindForm.DreamIdleWait },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v != "" {
				if _, err := time.ParseDuration(v); err != nil {
					return fmt.Errorf("dream_idle_wait must be a duration like 5m: %w", err)
				}
			}
			c.MindForm.DreamIdleWait = v
			return nil
		},
	})
	register(Key{
		Path:        "mindform.dream_close_grace",
		Description: "Max wait for claude to exit after stdin close during dream rotation. Examples: 60s, 2m. Empty uses the 60s default.",
		Contexts:    ContainerCtx,
		Get:         func(c *Config) string { return c.MindForm.DreamCloseGrace },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v != "" {
				if _, err := time.ParseDuration(v); err != nil {
					return fmt.Errorf("dream_close_grace must be a duration like 60s: %w", err)
				}
			}
			c.MindForm.DreamCloseGrace = v
			return nil
		},
	})
}

// isLoopbackHost mirrors internal/dashboard's isLoopback; duplicated here
// to keep internal/config dependency-free. Bare port (host == "") is
// treated as non-loopback because it binds all interfaces; "localhost"
// is allowed alongside literal loopback IPs.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
