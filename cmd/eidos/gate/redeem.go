package gate

import (
	"fmt"

	"github.com/spf13/cobra"
)

var redeemCmd = &cobra.Command{
	Use:   "redeem <invite-uri-or-token>",
	Short: "Redeem an invitation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			IssuerNpub  string   `json:"issuer_npub"`
			IssuerRelay string   `json:"issuer_relay"`
			AcceptedBy  []string `json:"accepted_by"`
		}
		if err := mustOK(c.Call("invite.redeem", map[string]string{"token": args[0]}, &resp)); err != nil {
			return err
		}
		fmt.Printf("redeemed: %s\nrelay:    %s\naccepted_by:\n", resp.IssuerNpub, resp.IssuerRelay)
		for _, r := range resp.AcceptedBy {
			fmt.Println("  " + r)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(redeemCmd) }
