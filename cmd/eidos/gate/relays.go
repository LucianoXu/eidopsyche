package gate

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
)

var relayAddRole string

var relaysCmd = &cobra.Command{
	Use:   "relays",
	Short: "List own relays",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp []daemon.OwnRelayRow
		if err := mustOK(c.Call("relay.list", nil, &resp)); err != nil {
			return err
		}
		for _, r := range resp {
			fmt.Printf("%-9s  %s\n", r.Role, r.URL)
		}
		return nil
	},
}

var relayAddCmd = &cobra.Command{
	Use:   "relay-add <url>",
	Short: "Add a relay",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]bool
		if err := mustOK(c.Call("relay.add", map[string]string{"url": args[0], "role": relayAddRole}, &resp)); err != nil {
			return err
		}
		fmt.Println("added")
		return nil
	},
}

var relayRemoveCmd = &cobra.Command{
	Use:   "relay-remove <url>",
	Short: "Remove a relay",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]bool
		if err := mustOK(c.Call("relay.remove", map[string]string{"url": args[0]}, &resp)); err != nil {
			return err
		}
		fmt.Println("removed")
		return nil
	},
}

func init() {
	relayAddCmd.Flags().StringVar(&relayAddRole, "role", "fallback", "home or fallback")
	rootCmd.AddCommand(relaysCmd)
	rootCmd.AddCommand(relayAddCmd)
	rootCmd.AddCommand(relayRemoveCmd)
}
