// Package mcp provides the `eidos mcp` subcommand: a stdio MCP server
// that exposes the gate daemon's IPC method table as typed tools for
// the mind-form's (or operator's) Claude Code session.
//
// See docs/superpowers/specs/2026-05-14-eidos-mcp-design.md.
package mcp

import (
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/mcp"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

var stateDir string

var rootCmd = &cobra.Command{
	Use:           "mcp",
	Short:         "MCP server exposing the gate daemon's method table as typed tools",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.ResolveStateDir(stateDir)
		if err != nil {
			return fmt.Errorf("resolve state dir: %w", err)
		}
		cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
		socket := filepath.Join(dir, cfg.Daemon.Socket)

		s, err := mcp.New(socket, version.Version)
		if err != nil {
			return fmt.Errorf("init mcp server: %w", err)
		}
		defer s.Close()

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return s.Run(ctx)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&stateDir, "state-dir", "", "MindGate state directory (default: $EIDOS_GATE_HOME or platform default)")
}

// Command returns the root cobra.Command for `eidos mcp`.
func Command() *cobra.Command { return rootCmd }
