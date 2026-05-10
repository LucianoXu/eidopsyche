package forge

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/spf13/cobra"
)

// transcriptsDir is the in-container path to the per-mindform transcripts
// directory. var (not const) so tests can substitute a temp dir.
var transcriptsDir = "/eidos/run/transcripts"

func newTranscriptListCmd() *cobra.Command {
	var asJSON bool
	var limit int
	cmd := &cobra.Command{
		Use:    "transcript-list",
		Short:  "Internal: list recent wake transcripts (host's `forge watch --list` consumes the JSON form)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := transcript.NewStore(transcriptsDir)
			if err != nil {
				return err
			}
			idx, err := s.ReadIndex()
			if err != nil {
				return err
			}
			if limit > 0 && len(idx.Wakes) > limit {
				idx.Wakes = idx.Wakes[:limit]
			}
			if asJSON {
				body, err := json.MarshalIndent(idx, "", "  ")
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(append(body, '\n'))
				return err
			}
			return renderListTable(cmd, idx)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit raw index.json instead of a human-readable table")
	cmd.Flags().IntVar(&limit, "limit", 0, "show at most N most recent wakes (0 = all)")
	return cmd
}

func renderListTable(cmd *cobra.Command, idx transcript.Index) error {
	out := cmd.OutOrStdout()
	if len(idx.Wakes) == 0 {
		fmt.Fprintln(out, "no wakes recorded yet")
		return nil
	}
	fmt.Fprintln(out, "ID         SESSION    REASON      STARTED              DUR    COST      STATUS")
	for _, w := range idx.Wakes {
		id := w.ID
		if len(id) > 8 {
			id = id[:8]
		}
		sess := "-"
		if w.SessionID != "" {
			sess = w.SessionID
			if len(sess) > 8 {
				sess = sess[:8]
			}
		}
		started := time.Unix(w.StartedAt, 0).UTC().Format("2006-01-02 15:04:05")
		dur := "-"
		if w.EndedAt > w.StartedAt {
			dur = (time.Duration(w.EndedAt-w.StartedAt) * time.Second).Truncate(time.Second).String()
		}
		cost := "-"
		if w.CostUSD != nil && *w.CostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", *w.CostUSD)
		}
		status := "ok"
		if !w.OK {
			status = "crashed"
			if w.ExitCode != -1 && w.ExitCode != 0 {
				status = fmt.Sprintf("failed(%d)", w.ExitCode)
			}
		}
		fmt.Fprintf(out, "%-10s %-10s %-11s %-20s %-6s %-9s %s\n",
			id, sess, w.Reason, started, dur, cost, status)
	}
	return nil
}
