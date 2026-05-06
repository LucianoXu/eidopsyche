package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
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
			ts := time.Unix(int64(m["received_at"].(float64)), 0)
			fmt.Printf("%s  %.16s  %s\n", ts.Format("2006-01-02 15:04:05"), m["from"], m["content"])
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
			ts := time.Unix(int64(data["received_at"].(float64)), 0)
			fmt.Printf("%s  %.16s  %s\n", ts.Format("2006-01-02 15:04:05"), data["from"], data["content"])
		}
		return nil
	},
}

func init() {
	inboxCmd.Flags().BoolVar(&inboxTailFlag, "tail", false, "follow new messages")
	inboxCmd.Flags().StringVar(&inboxFrom, "from", "", "filter by sender npub or hex")
	inboxCmd.Flags().IntVar(&inboxLimit, "limit", 50, "limit number of results")
	inboxCmd.Flags().Int64Var(&inboxSince, "since", 0, "unix seconds lower bound")
	rootCmd.AddCommand(inboxCmd)
}
