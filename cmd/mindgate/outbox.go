package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
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
			ts := time.Unix(int64(m["sent_at"].(float64)), 0)
			fmt.Printf("%s  -> %.16s  %s\n", ts.Format("2006-01-02 15:04:05"), m["to"], m["content"])
		}
		return nil
	},
}

func init() {
	outboxCmd.Flags().StringVar(&outboxTo, "to", "", "filter by recipient npub or hex")
	outboxCmd.Flags().IntVar(&outboxLimit, "limit", 50, "limit number of results")
	rootCmd.AddCommand(outboxCmd)
}
