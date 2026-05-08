package gate

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

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
		key, ok := config.KeyByPath(args[0])
		if !ok {
			return fmt.Errorf("unknown config key: %s", args[0])
		}
		fmt.Println(key.Get(&cfg))
		return nil
	}

	for _, key := range config.KeyList() {
		fmt.Printf("%s = %s\n", key.Path, key.Get(&cfg))
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	path, value := args[0], args[1]
	key, ok := config.KeyByPath(path)
	if !ok {
		return fmt.Errorf("unknown config key: %s", path)
	}

	cfgPath, err := resolveConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := key.Set(&cfg, value); err != nil {
		return err
	}

	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}
