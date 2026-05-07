package gate

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// configKey describes how to get and set a single scalar config key.
type configKey struct {
	get func(*config.Config) string
	set func(*config.Config, string) error
}

// configKeys is the static map of supported scalar config keys.
var configKeys = map[string]configKey{
	"log_level": {
		get: func(c *config.Config) string { return c.LogLevel },
		set: func(c *config.Config, v string) error { c.LogLevel = v; return nil },
	},
	"daemon.socket": {
		get: func(c *config.Config) string { return c.Daemon.Socket },
		set: func(c *config.Config, v string) error { c.Daemon.Socket = v; return nil },
	},
	"daemon.shutdown_grace_seconds": {
		get: func(c *config.Config) string { return strconv.Itoa(c.Daemon.ShutdownGraceSeconds) },
		set: func(c *config.Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("daemon.shutdown_grace_seconds requires an integer, got %q", v)
			}
			c.Daemon.ShutdownGraceSeconds = n
			return nil
		},
	},
	"relay.mode": {
		get: func(c *config.Config) string { return c.Relay.Mode },
		set: func(c *config.Config, v string) error {
			if v != "paired" && v != "public" {
				return fmt.Errorf("relay.mode must be \"paired\" or \"public\", got %q", v)
			}
			c.Relay.Mode = v
			return nil
		},
	},
	"relay.listen": {
		get: func(c *config.Config) string { return c.Relay.Listen },
		set: func(c *config.Config, v string) error {
			if _, _, err := net.SplitHostPort(v); err != nil {
				return fmt.Errorf("relay.listen must be host:port, got %q: %w", v, err)
			}
			c.Relay.Listen = v
			return nil
		},
	},
	"relay.public_url": {
		get: func(c *config.Config) string { return c.Relay.PublicURL },
		set: func(c *config.Config, v string) error { c.Relay.PublicURL = v; return nil },
	},
	"relay.data_dir": {
		get: func(c *config.Config) string { return c.Relay.DataDir },
		set: func(c *config.Config, v string) error { c.Relay.DataDir = v; return nil },
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write config.toml settings",
}

var configGetCmd = &cobra.Command{
	Use:   "get [<key>]",
	Short: "Print one or all scalar config settings",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a scalar config setting (daemon and relay must be restarted to pick up changes)",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}

func resolveConfigPath() (string, error) {
	dir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	cfgPath, err := resolveConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if len(args) == 1 {
		key := args[0]
		k, ok := configKeys[key]
		if !ok {
			return fmt.Errorf("unknown config key: %s", key)
		}
		fmt.Println(k.get(&cfg))
		return nil
	}

	// Print all known scalar keys in sorted order.
	keys := make([]string, 0, len(configKeys))
	for k := range configKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		fmt.Printf("%s = %s\n", name, configKeys[name].get(&cfg))
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	k, ok := configKeys[key]
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}

	cfgPath, err := resolveConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := k.set(&cfg, value); err != nil {
		return err
	}

	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}
