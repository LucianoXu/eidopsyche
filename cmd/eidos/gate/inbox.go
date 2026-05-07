package gate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

var (
	inboxTailFlag bool
	inboxFrom     string
	inboxLimit    int
	inboxSince    int64
)

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Show received messages",
	RunE: func(cmd *cobra.Command, args []string) error {
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
		var ack map[string]bool
		if err := mustOK(c.Call("inbox.tail", nil, &ack)); err != nil {
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
//	YYYY-MM-DD HH:MM:SS  <from-prefix>  [malformed: <reason>] <body>
//
// For envelope-decoded chat rows the body is the parsed text. For malformed
// rows the prefix carries the reason; the body falls back to whatever
// content arrived (so operators can debug interop). Legacy plain-text rows
// (pre-envelope, no Malformed flag) render their content as-is.
func formatInboxRow(m map[string]any) string {
	tsRaw, _ := m["received_at"].(float64)
	ts := time.Unix(int64(tsRaw), 0)
	from, _ := m["from"].(string)
	content, _ := m["content"].(string)

	var prefix string
	if mal, _ := m["malformed"].(bool); mal {
		reason, _ := m["reject_reason"].(string)
		prefix = fmt.Sprintf("[malformed: %s] ", reason)
	}

	body := content
	if env, err := envelope.Decode(content); err == nil && env.Type == envelope.TypeChat {
		body = env.Text
	}
	return fmt.Sprintf("%s  %.16s  %s%s",
		ts.Format("2006-01-02 15:04:05"), from, prefix, strings.TrimRight(body, "\n"))
}

func init() {
	inboxCmd.Flags().BoolVar(&inboxTailFlag, "tail", false, "follow new messages")
	inboxCmd.Flags().StringVar(&inboxFrom, "from", "", "filter by sender npub or hex")
	inboxCmd.Flags().IntVar(&inboxLimit, "limit", 50, "limit number of results")
	inboxCmd.Flags().Int64Var(&inboxSince, "since", 0, "unix seconds lower bound")
	rootCmd.AddCommand(inboxCmd)
}
