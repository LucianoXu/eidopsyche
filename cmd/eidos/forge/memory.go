package forge

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Memory inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List files under memory/",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := "/eidos/ontology/memory"
			return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(root, p)
				info, _ := d.Info()
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %d bytes\n", rel, info.Size())
				return nil
			})
		},
	})
	return cmd
}
