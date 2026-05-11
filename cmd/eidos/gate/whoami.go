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
		// state.get identity returns the same shape the legacy whoami
		// did, plus an identity.card subtree (ignored here).
		var resp struct {
			Npub       string              `json:"npub"`
			Pubkey     string              `json:"pubkey"`
			Label      string              `json:"label"`
			HomeRelays []map[string]string `json:"home_relays"`
		}
		if err := mustOK(c.Call("state.get", map[string]string{"path": "identity"}, &resp)); err != nil {
			return err
		}
		fmt.Printf("Label: %s\n", resp.Label)
		fmt.Printf("Npub:  %s\n", resp.Npub)
		fmt.Printf("Hex:   %s\n", resp.Pubkey)
		// Best-effort relay-health lookup: state.get relays returns a
		// map keyed by url where each entry already includes state +
		// last_error from the daemon's relayHealth store. We index by
		// url so the render below can attach the tag.
		relays := map[string]map[string]any{}
		_ = mustOK(c.Call("state.get", map[string]string{"path": "relays"}, &relays))

		fmt.Println("Relays:")
		for _, r := range resp.HomeRelays {
			tag := ""
			if h, ok := relays[r["url"]]; ok {
				state, _ := h["state"].(string)
				lastErr, _ := h["last_error"].(string)
				if state != "" {
					tag = " [" + state + "]"
					if lastErr != "" {
						tag = " [" + state + ": " + lastErr + "]"
					}
				}
			}
			fmt.Printf("  %-10s %s%s\n", r["role"], r["url"], tag)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(whoamiCmd) }
