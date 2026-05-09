package forge

import "github.com/spf13/cobra"

// stub returns a cobra.Command that prints "not yet implemented" and exits 0.
func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.PrintErrf("%s: not yet implemented\n", use)
			return nil
		},
	}
}
