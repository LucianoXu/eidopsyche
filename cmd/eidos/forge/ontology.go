package forge

import (
	"fmt"
	"os"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newOntologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ontology",
		Short: "Ontology export / import",
	}
	cmd.AddCommand(newOntologyExportCmd(), newOntologyImportCmd())
	return cmd
}

func newOntologyExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <name> <path>",
		Short: "Snapshot /eidos/ontology to a tar at <path>",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return c.CopyFromContainer(cmd.Context(), forgectl.ContainerName(name), "/eidos/ontology", f)
		},
	}
}

func newOntologyImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <name> <path>",
		Short: "Restore /eidos/ontology from a tar at <path> (mind-form must be stopped)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("ontology import: deferred to a follow-up; use docker cp manually for now")
		},
	}
}
