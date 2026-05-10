package gate

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

var (
	outboxTo    string
	outboxLimit int
)

var outboxCmd = &cobra.Command{
	Use:   "outbox",
	Short: "Show sent messages (collapsed by event id)",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		params := map[string]any{}
		if outboxTo != "" {
			params["to"] = outboxTo
		}
		if outboxLimit > 0 {
			params["limit"] = outboxLimit
		}
		var resp []map[string]any
		if err := mustOK(c.Call("outbox.list", params, &resp)); err != nil {
			return err
		}
		for _, m := range resp {
			fmt.Println(formatOutboxRow(m))
		}
		return nil
	},
}

// formatOutboxRow renders one outbox row. <recipient> is the contact
// label when known (daemon-side join via outbox.list), otherwise a
// short hex prefix with an ellipsis.
func formatOutboxRow(m map[string]any) string {
	tsRaw, _ := m["sent_at"].(float64)
	ts := time.Unix(int64(tsRaw), 0)
	to, _ := m["to"].(string)
	label, _ := m["label"].(string)
	content, _ := m["content"].(string)
	return fmt.Sprintf("%s  %s  -> %-16s  %s",
		ts.Format("2006-01-02 15:04:05"), outboxStatus(m), contacts.FormatPubkey(label, to), content)
}

// outboxStatus returns a fixed-width column indicating delivery state.
// "✓✓" = peer ack received (tier 2); "✓ " = at least one relay accepted
// the publish (tier 1); "  " = neither (unexpected — send returns an
// error before persisting if no relay accepts, but kept for safety).
func outboxStatus(m map[string]any) string {
	if v, ok := m["acked_at"].(float64); ok && v != 0 {
		return "✓✓"
	}
	if accepted, ok := m["accepted_by"].([]any); ok && len(accepted) > 0 {
		return "✓ "
	}
	return "  "
}

func init() {
	outboxCmd.Flags().StringVar(&outboxTo, "to", "", "filter by recipient npub or hex")
	outboxCmd.Flags().IntVar(&outboxLimit, "limit", 50, "limit number of results")
	rootCmd.AddCommand(outboxCmd)
}
