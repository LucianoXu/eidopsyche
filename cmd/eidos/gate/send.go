package gate

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

var (
	sendStdin   bool
	sendCommand string
)

var sendCmd = &cobra.Command{
	Use:   "send <npub> [<content>]",
	Short: "Send a NIP-17 encrypted message",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()

		var env envelope.Envelope
		if sendCommand != "" {
			if len(args) > 1 || sendStdin {
				return fmt.Errorf("--command cannot be combined with content or --stdin")
			}
			env = envelope.Envelope{
				V:       envelope.SchemaVersion,
				Type:    envelope.TypeCommand,
				Text:    "/" + sendCommand,
				Command: &envelope.Command{Name: sendCommand, Args: map[string]any{}},
				Client:  &envelope.Client{Name: "eidos", Ver: version.Version},
			}
		} else {
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
			if content == "" {
				return fmt.Errorf("empty message")
			}
			env = envelope.Envelope{
				V:      envelope.SchemaVersion,
				Type:   envelope.TypeChat,
				Text:   content,
				Client: &envelope.Client{Name: "eidos", Ver: version.Version},
			}
		}

		var resp struct {
			EventID    string   `json:"event_id"`
			AcceptedBy []string `json:"accepted_by"`
		}
		if err := mustOK(c.Call("send", map[string]any{"to": args[0], "envelope": env}, &resp)); err != nil {
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
	sendCmd.Flags().StringVar(&sendCommand, "command", "", "send a v1 command (e.g., status) instead of chat")
	rootCmd.AddCommand(sendCmd)
}
