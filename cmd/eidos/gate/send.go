package gate

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var sendStdin bool

var sendCmd = &cobra.Command{
	Use:   "send <npub> [<content>]",
	Short: "Send a NIP-17 encrypted message",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var content string
		if sendStdin || len(args) == 1 {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			content = strings.TrimRight(string(b), "\n")
		} else {
			content = args[1]
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			EventID    string   `json:"event_id"`
			AcceptedBy []string `json:"accepted_by"`
		}
		if err := mustOK(c.Call("send", map[string]string{"to": args[0], "content": content}, &resp)); err != nil {
			return err
		}
		fmt.Printf("event_id: %s\n", resp.EventID)
		fmt.Println("accepted_by:")
		for _, r := range resp.AcceptedBy {
			fmt.Printf("  %s\n", r)
		}
		return nil
	},
}

func init() {
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "read message from stdin")
	rootCmd.AddCommand(sendCmd)
}
