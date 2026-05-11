package gate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

var (
	addContactRelays []string
	addContactLabel  string
	addContactTier   string
)

var addContactCmd = &cobra.Command{
	Use:   "add-contact <npub-or-uri>",
	Short: "Add a contact",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		input := args[0]
		var npub string
		var relays []string
		var label string
		if strings.HasPrefix(input, "mindgate://") {
			var parsed map[string]string
			if err := mustOK(c.Call("card.parse", map[string]string{"uri": input}, &parsed)); err != nil {
				return err
			}
			npub = parsed["npub"]
			relays = []string{parsed["relay"]}
			label = parsed["label"]
		} else {
			npub = input
			relays = addContactRelays
			label = addContactLabel
		}
		// flag overrides take precedence whenever set
		if addContactLabel != "" {
			label = addContactLabel
		}
		if len(addContactRelays) > 0 {
			relays = addContactRelays
		}
		var resp map[string]bool
		if err := mustOK(c.Call("contact.add", map[string]any{
			"npub":   npub,
			"relays": relays,
			"label":  label,
			"tier":   addContactTier,
		}, &resp)); err != nil {
			return err
		}
		fmt.Printf("added %s\n", npub)
		return nil
	},
}

var contactsCmd = &cobra.Command{
	Use:   "contacts",
	Short: "List contacts",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		// state.get contacts returns map[pubkey -> {label, tier, ...}].
		// Sort by pubkey for stable CLI output (map iteration order is
		// unspecified).
		resp := map[string]map[string]any{}
		if err := mustOK(c.Call("state.get", map[string]string{"path": "contacts"}, &resp)); err != nil {
			return err
		}
		pks := make([]string, 0, len(resp))
		for pk := range resp {
			pks = append(pks, pk)
		}
		sort.Strings(pks)
		for _, pk := range pks {
			ct := resp[pk]
			label, _ := ct["label"].(string)
			tier, _ := ct["tier"].(string)
			npub, _ := identity.EncodeNpub(pk)
			fmt.Printf("%s  %-20s  %s\n", npub, label, tier)
		}
		return nil
	},
}

var removeContactCmd = &cobra.Command{
	Use:   "remove-contact <npub>",
	Short: "Remove a contact",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]bool
		if err := mustOK(c.Call("contact.remove", map[string]string{"npub": args[0]}, &resp)); err != nil {
			return err
		}
		fmt.Println("removed")
		return nil
	},
}

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Per-contact operations",
}

var contactSetTierCmd = &cobra.Command{
	Use:   "set-tier <target> <tier>",
	Short: "Move a contact between trust tiers (master|friend|acquaintance|blocked)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]string
		if err := mustOK(c.Call("contact.set-tier", map[string]string{
			"target": args[0],
			"tier":   args[1],
		}, &resp)); err != nil {
			return err
		}
		fmt.Printf("%s: tier=%s\n", resp["pubkey"], resp["tier"])
		return nil
	},
}

func init() {
	addContactCmd.Flags().StringArrayVar(&addContactRelays, "relay", nil, "relay URL (repeatable)")
	addContactCmd.Flags().StringVar(&addContactLabel, "label", "", "human label")
	addContactCmd.Flags().StringVar(&addContactTier, "tier", "friend", "tier")
	rootCmd.AddCommand(addContactCmd)
	rootCmd.AddCommand(contactsCmd)
	rootCmd.AddCommand(removeContactCmd)
	contactCmd.AddCommand(contactSetTierCmd)
	rootCmd.AddCommand(contactCmd)
}
