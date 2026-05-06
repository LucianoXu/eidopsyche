package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "mindgate",
	Short:         "MindGate — Eidopsyche identity and communication CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var globalStateDir string

func init() {
	rootCmd.PersistentFlags().StringVar(&globalStateDir, "state-dir", "", "MindGate state directory")
}
