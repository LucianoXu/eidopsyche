package gate

import (
	"fmt"

	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show this entity's identity",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			Npub       string              `json:"npub"`
			Pubkey     string              `json:"pubkey"`
			Label      string              `json:"label"`
			HomeRelays []map[string]string `json:"home_relays"`
		}
		if err := mustOK(c.Call("whoami", nil, &resp)); err != nil {
			return err
		}
		fmt.Printf("Label: %s\n", resp.Label)
		fmt.Printf("Npub:  %s\n", resp.Npub)
		fmt.Printf("Hex:   %s\n", resp.Pubkey)
		fmt.Println("Relays:")
		for _, r := range resp.HomeRelays {
			fmt.Printf("  %-10s %s\n", r["role"], r["url"])
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(whoamiCmd) }
