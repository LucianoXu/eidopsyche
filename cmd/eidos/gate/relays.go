package gate

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
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
		// state.get relays returns map[url -> {role, state, ...}].
		// Sort by url for stable CLI output.
		resp := map[string]map[string]any{}
		if err := mustOK(c.Call("state.get", map[string]string{"path": "relays"}, &resp)); err != nil {
			return err
		}
		urls := make([]string, 0, len(resp))
		for url := range resp {
			urls = append(urls, url)
		}
		sort.Strings(urls)
		for _, url := range urls {
			role, _ := resp[url]["role"].(string)
			fmt.Printf("%-9s  %s\n", role, url)
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
