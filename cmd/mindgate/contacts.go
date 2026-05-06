package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
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
		var resp []map[string]any
		if err := mustOK(c.Call("contact.list", nil, &resp)); err != nil {
			return err
		}
		for _, r := range resp {
			fmt.Printf("%s  %-20s  %s\n", r["npub"], r["label"], r["tier"])
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

func init() {
	addContactCmd.Flags().StringArrayVar(&addContactRelays, "relay", nil, "relay URL (repeatable)")
	addContactCmd.Flags().StringVar(&addContactLabel, "label", "", "human label")
	addContactCmd.Flags().StringVar(&addContactTier, "tier", "friend", "tier")
	rootCmd.AddCommand(addContactCmd)
	rootCmd.AddCommand(contactsCmd)
	rootCmd.AddCommand(removeContactCmd)
}
