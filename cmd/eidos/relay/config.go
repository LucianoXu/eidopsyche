package relay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

var configDir string

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Get or set relay config keys",
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the current value of a relay config key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(configDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}
		v, err := getKey(cfg, args[0])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a relay config key (writes config.toml)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(configDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}
		if err := setKey(&cfg, args[0], args[1]); err != nil {
			return err
		}
		// Save() validates internally, so a malformed mutation is caught
		// before any disk write happens.
		return relaycfg.Save(dir, cfg)
	},
}

func resolveConfigDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return relaycfg.DefaultDir()
}

func getKey(cfg relaycfg.Config, key string) (string, error) {
	switch key {
	case "log_level":
		return cfg.LogLevel, nil
	case "relay.mode":
		return cfg.Relay.Mode, nil
	case "relay.listen":
		return cfg.Relay.Listen, nil
	case "relay.owner_pubkey":
		return cfg.Relay.OwnerPubkey, nil
	case "relay.tls.cert_file":
		return cfg.Relay.TLS.CertFile, nil
	case "relay.tls.key_file":
		return cfg.Relay.TLS.KeyFile, nil
	case "relay.auth.required":
		return strconv.FormatBool(cfg.Relay.Auth.Required), nil
	case "relay.auth.service_url":
		return cfg.Relay.Auth.ServiceURL, nil
	}
	return "", fmt.Errorf("unknown config key %q", key)
}

func setKey(cfg *relaycfg.Config, key, value string) error {
	switch key {
	case "log_level":
		cfg.LogLevel = value
	case "relay.mode":
		cfg.Relay.Mode = value
	case "relay.listen":
		cfg.Relay.Listen = value
	case "relay.owner_pubkey":
		cfg.Relay.OwnerPubkey = strings.TrimSpace(value)
	case "relay.tls.cert_file":
		cfg.Relay.TLS.CertFile = value
	case "relay.tls.key_file":
		cfg.Relay.TLS.KeyFile = value
	case "relay.auth.required":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("relay.auth.required must be true|false: %w", err)
		}
		cfg.Relay.Auth.Required = b
	case "relay.auth.service_url":
		cfg.Relay.Auth.ServiceURL = value
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func init() {
	configCmd.PersistentFlags().StringVar(&configDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	configCmd.AddCommand(configGetCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}
