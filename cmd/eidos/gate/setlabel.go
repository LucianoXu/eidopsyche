package gate

import (
	"fmt"

	"github.com/spf13/cobra"
)

var setLabelCmd = &cobra.Command{
	Use:   "set-label <new-label>",
	Short: "Change this entity's own label (the one shown in card / whoami)",
	Long: `Updates the label that this MindGate identity advertises in its
mindgate:// card URI and in 'eidos gate whoami' output. The label is purely
client-side metadata — it does not affect the npub, the home relay, or any
on-the-wire identifier. Peers who already added you keep the label they
chose locally; this changes only what new peers see when they parse a fresh
card from you.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			Label string `json:"label"`
		}
		if err := mustOK(c.Call("set-label", map[string]string{"label": args[0]}, &resp)); err != nil {
			return err
		}
		fmt.Printf("label set: %s\n", resp.Label)
		return nil
	},
}

var setContactLabelCmd = &cobra.Command{
	Use:   "set-contact-label <npub-or-hex-or-current-label> <new-label>",
	Short: "Rename a contact",
	Long: `Updates the label of an existing contact. The first argument is
resolved exactly the same way 'send' / 'remove-contact' resolve their target:
bech32 npub, 64-char hex pubkey, or the contact's current label (rejected
if multiple contacts share that label).`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			Pubkey string `json:"pubkey"`
			Label  string `json:"label"`
		}
		if err := mustOK(c.Call("contact.set-label", map[string]string{
			"target": args[0],
			"label":  args[1],
		}, &resp)); err != nil {
			return err
		}
		fmt.Printf("label set: %s → %s\n", resp.Pubkey, resp.Label)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(setLabelCmd)
	rootCmd.AddCommand(setContactLabelCmd)
}
