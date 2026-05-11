package gate

import (
	"fmt"

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

func runConfigGet(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	defer c.Close()

	var cfg config.Config
	if err := mustOK(c.Call("state.get", map[string]string{"path": "config"}, &cfg)); err != nil {
		return err
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
	c, err := newClient()
	if err != nil {
		return err
	}
	defer c.Close()

	return mustOK(c.Call("config.set", map[string]string{
		"path":  args[0],
		"value": args[1],
	}, nil))
}
