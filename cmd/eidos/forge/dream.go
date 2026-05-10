package forge

import (
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/spf13/cobra"
)

// dreamStatePath is the in-container dream-state.json path. var (not
// const) so tests can redirect it to a temp file.
var dreamStatePath = "/eidos/run/dream-state.json"

func newDreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "Mark the start or end of a dream (memory consolidation)",
	}
	cmd.AddCommand(newDreamBeginCmd())
	cmd.AddCommand(newDreamEndCmd())
	return cmd
}

func newDreamBeginCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "begin",
		Short: "Mark the start of a dream",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := dreamBeginEcho(dreamStatePath, note)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "optional intent line")
	return cmd
}

func newDreamEndCmd() *cobra.Command {
	var note, prosePath string
	cmd := &cobra.Command{
		Use:   "end",
		Short: "Mark the end of a dream (note required)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := dreamEndEcho(dreamStatePath, note, prosePath)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "one-line summary of what the dream consolidated (required)")
	cmd.Flags().StringVar(&prosePath, "prose-path", "", "relative path to the dream prose under memory/episodic/")
	_ = cmd.MarkFlagRequired("note")
	return cmd
}

// dreamBeginEcho returns the human-readable confirmation line for the begin command.
func dreamBeginEcho(path, note string) (string, error) {
	prev, _ := dreamstate.Read(path)
	now := time.Now()
	if err := dreamstate.Begin(path, now, note); err != nil {
		return "", err
	}
	if prev.LastDreamFinishedAt > 0 {
		gap := now.Sub(time.Unix(prev.LastDreamFinishedAt, 0)).Truncate(time.Second)
		return fmt.Sprintf("dreaming since %s\n%s since last dream\n",
			now.Format(time.RFC3339), formatDuration(gap)), nil
	}
	return fmt.Sprintf("dreaming since %s (no prior dream recorded)\n",
		now.Format(time.RFC3339)), nil
}

// dreamEndEcho returns the human-readable confirmation line for the end command.
func dreamEndEcho(path, note, prosePath string) (string, error) {
	prev, _ := dreamstate.Read(path)
	now := time.Now()
	if err := dreamstate.End(path, now, note, prosePath); err != nil {
		return "", err
	}
	st, _ := dreamstate.Read(path)
	if prev.LastDreamStartedAt > 0 {
		dur := now.Sub(time.Unix(prev.LastDreamStartedAt, 0)).Truncate(time.Second)
		return fmt.Sprintf("dream ended after %s — recorded as #%d\n",
			formatDuration(dur), st.DreamCount), nil
	}
	return fmt.Sprintf("dream ended (no prior begin) — recorded as #%d\n", st.DreamCount), nil
}
