package gate

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cardCmd = &cobra.Command{
	Use:   "card",
	Short: "Print this entity's mindgate:// card URI",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			URI string `json:"uri"`
		}
		if err := mustOK(c.Call("card.export", nil, &resp)); err != nil {
			return err
		}
		fmt.Println(resp.URI)
		return nil
	},
}

var scanCmd = &cobra.Command{
	Use:   "scan <uri>",
	Short: "Parse a mindgate:// URI without storing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]string
		if err := mustOK(c.Call("card.parse", map[string]string{"uri": args[0]}, &resp)); err != nil {
			return err
		}
		for k, v := range resp {
			fmt.Printf("%s: %s\n", k, v)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cardCmd)
	rootCmd.AddCommand(scanCmd)
}
