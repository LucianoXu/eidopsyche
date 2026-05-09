package forge

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var follow, essence bool
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show mind-form logs (default: docker; --essence for episodic memory)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			if essence {
				return tailEssence(cmd.Context(), c, name, follow, cmd.OutOrStdout())
			}
			return c.ContainerLogs(cmd.Context(), forgectl.ContainerName(name), follow, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().BoolVar(&essence, "essence", false, "tail the mind-form's own episodic memory instead of docker logs")
	return cmd
}

func tailEssence(ctx context.Context, c forgectl.Client, name string, follow bool, out io.Writer) error {
	// v0: snapshot the episodic dir to a tmp tar, print the path. A richer
	// implementation (parse, follow) is reserved.
	_ = follow
	tmp, err := os.MkdirTemp("", "eidos-essence-*")
	if err != nil {
		return err
	}
	tarPath := filepath.Join(tmp, "episodic.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		os.RemoveAll(tmp)
		return err
	}
	defer f.Close()
	if err := c.CopyFromContainer(ctx, forgectl.ContainerName(name), "/eidos/ontology/memory/episodic", f); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	fmt.Fprintf(out, "essence snapshot: %s\n", tarPath)
	return nil
}
