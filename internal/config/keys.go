package config

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
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
		Path:        "relay.enabled",
		Description: "Run the embedded MindGate relay alongside the daemon.",
		Get: func(c *Config) string {
			if c.Relay.Enabled {
				return "true"
			}
			return "false"
		},
		Set: func(c *Config, v string) error {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "on":
				c.Relay.Enabled = true
			case "false", "0", "no", "off":
				c.Relay.Enabled = false
			default:
				return fmt.Errorf(`relay.enabled must be true or false, got %q`, v)
			}
			return nil
		},
	})
	register(Key{
		Path:        "relay.mode",
		Description: `"paired" accepts only kind:1059 events addressed to the owner; "public" accepts any well-formed event.`,
		Get:         func(c *Config) string { return c.Relay.Mode },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v != "paired" && v != "public" {
				return fmt.Errorf(`relay.mode must be "paired" or "public", got %q`, v)
			}
			c.Relay.Mode = v
			return nil
		},
	})
	register(Key{
		Path:        "relay.listen",
		Description: "host:port the embedded relay binds to. Disable the relay via relay.enabled instead of clearing this.",
		Get:         func(c *Config) string { return c.Relay.Listen },
		Set: func(c *Config, v string) error {
			v = strings.TrimSpace(v)
			if v == "" {
				return fmt.Errorf("relay.listen must be a non-empty host:port; to disable the local relay, set relay.enabled = false instead")
			}
			if _, _, err := net.SplitHostPort(v); err != nil {
				return fmt.Errorf("relay.listen must be host:port, got %q: %w", v, err)
			}
			c.Relay.Listen = v
			return nil
		},
	})
	register(Key{
		Path:        "relay.data_dir",
		Description: "Subdirectory under the state directory where the relay writes its event store.",
		Get:         func(c *Config) string { return c.Relay.DataDir },
		Set:         func(c *Config, v string) error { c.Relay.DataDir = strings.TrimSpace(v); return nil },
	})
	register(Key{
		Path:        "dashboard.enabled",
		Description: "Run the embedded local web dashboard alongside the daemon.",
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
