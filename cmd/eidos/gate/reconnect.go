package gate

import (
	"fmt"

	"github.com/spf13/cobra"
)

var reconnectCmd = &cobra.Command{
	Use:   "reconnect",
	Short: "Force the daemon to recompute and reattach its relay subscriptions",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]bool
		if err := mustOK(c.Call("subscribe.refresh", nil, &resp)); err != nil {
			return err
		}
		fmt.Println("subscriber refresh requested")
		return nil
	},
}

func init() { rootCmd.AddCommand(reconnectCmd) }
