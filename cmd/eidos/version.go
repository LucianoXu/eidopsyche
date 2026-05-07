package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/update"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

func buildInfo() update.BuildInfo {
	return update.BuildInfo{
		Version:   version.Version,
		Commit:    version.Commit,
		BuildDate: version.BuildDate,
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("eidos    %s\n", version.Version)
		fmt.Printf("commit:  %s\n", version.Commit)
		fmt.Printf("built:   %s\n", version.BuildDate)
		// `eidos version` always shows the update prompt regardless of cooldown.
		update.MaybePrompt(buildInfo(), os.Stderr, true)
	},
}
