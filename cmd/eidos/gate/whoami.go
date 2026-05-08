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
		// Best-effort relay-health lookup; whoami works even if the
		// daemon's relay-health surface isn't populated yet (e.g., right
		// after start before the first connect cycle completes).
		health := map[string]relayHealthRow{}
		var rows []relayHealthRow
		if err := mustOK(c.Call("relays.health", nil, &rows)); err == nil {
			for _, r := range rows {
				health[r.URL] = r
			}
		}

		fmt.Println("Relays:")
		for _, r := range resp.HomeRelays {
			tag := ""
			if h, ok := health[r["url"]]; ok {
				tag = " [" + h.State + "]"
				if h.LastError != "" {
					tag = " [" + h.State + ": " + h.LastError + "]"
				}
			}
			fmt.Printf("  %-10s %s%s\n", r["role"], r["url"], tag)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(whoamiCmd) }
