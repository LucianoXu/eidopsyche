package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

const Version = "0.0.1"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("mindgate %s\n", Version)
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
