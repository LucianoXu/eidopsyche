package gate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

var (
	inboxTailFlag bool
	inboxFrom     string
	inboxLimit    int
	inboxSince    int64
	inboxSender   string
)

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Show received messages",
	Long: `Show received messages.

By default, only messages from known contacts are shown. Messages from
strangers (pubkeys with no contact row or a TierBlocked row) are kept
in a "pending" view — pass --sender unknown to see them, or --sender
all to see both. Promote a pending sender with 'eidos gate add-contact'
and their backlog appears in the default view on the next refresh.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch inboxSender {
		case "", "known", "unknown", "all":
			// ok
		default:
			return fmt.Errorf("--sender must be one of known|unknown|all (got %q)", inboxSender)
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		params := map[string]any{}
		if inboxFrom != "" {
			params["from"] = inboxFrom
		}
		if inboxLimit > 0 {
			params["limit"] = inboxLimit
		}
		if inboxSince > 0 {
			params["since"] = inboxSince
		}
		if inboxSender != "" {
			params["sender"] = inboxSender
		}
		var resp []map[string]any
		if err := mustOK(c.Call("inbox.list", params, &resp)); err != nil {
			return err
		}
		for _, m := range resp {
			fmt.Println(formatInboxRow(m))
		}
		if !inboxTailFlag {
			return nil
		}
		tailParams := map[string]any{}
		if inboxSender != "" {
			tailParams["sender"] = inboxSender
		}
		var ack map[string]bool
		if err := mustOK(c.Call("inbox.tail", tailParams, &ack)); err != nil {
			return err
		}
		for ev := range c.Events() {
			if ev.Event != "inbox.message" {
				continue
			}
			var data map[string]any
			_ = json.Unmarshal(ev.Data, &data)
			fmt.Println(formatInboxRow(data))
		}
		return nil
	},
}

// formatInboxRow renders one inbox row. The format is:
//
//	YYYY-MM-DD HH:MM:SS  <sender>  [malformed: <reason>] <body>
//
// <sender> renders, in priority order:
//   - the contact label when one is known (daemon-side join via inbox.list)
//   - "(pending) <short-hex>" when the row's Pending flag is true and no
//     label is available — that short-hex is exactly what an operator can
//     paste into 'eidos gate add-contact' to promote the sender
//   - the short-hex prefix with ellipsis when neither is available
//
// For envelope-decoded chat rows the body is the parsed text; for
// malformed rows the prefix carries the reason and the body falls back
// to whatever content arrived. Legacy plain-text rows (pre-envelope, no
// Malformed flag) render their content as-is.
func formatInboxRow(m map[string]any) string {
	tsRaw, _ := m["received_at"].(float64)
	ts := time.Unix(int64(tsRaw), 0)
	from, _ := m["from"].(string)
	label, _ := m["label"].(string)
	content, _ := m["content"].(string)
	pending, _ := m["pending"].(bool)

	var prefix string
	if mal, _ := m["malformed"].(bool); mal {
		reason, _ := m["reject_reason"].(string)
		prefix = fmt.Sprintf("[malformed: %s] ", reason)
	}

	body := content
	if env, err := envelope.Decode(content); err == nil && env.Type == envelope.TypeChat {
		body = env.Text
	}
	senderCol := contacts.FormatPubkey(label, from)
	if pending && label == "" {
		senderCol = "(pending) " + contacts.FormatPubkey("", from)
	}
	return fmt.Sprintf("%s  %-26s  %s%s",
		ts.Format("2006-01-02 15:04:05"), senderCol, prefix, strings.TrimRight(body, "\n"))
}

func init() {
	inboxCmd.Flags().BoolVar(&inboxTailFlag, "tail", false, "follow new messages")
	inboxCmd.Flags().StringVar(&inboxFrom, "from", "", "filter by sender npub or hex")
	inboxCmd.Flags().IntVar(&inboxLimit, "limit", 50, "limit number of results")
	inboxCmd.Flags().Int64Var(&inboxSince, "since", 0, "unix seconds lower bound")
	inboxCmd.Flags().StringVar(&inboxSender, "sender", "",
		"contact-graph filter: known (default), unknown (no contact / blocked), or all")
	rootCmd.AddCommand(inboxCmd)
}
