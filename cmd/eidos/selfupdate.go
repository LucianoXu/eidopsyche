package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/version"
)

// installScriptURL is the canonical install/upgrade URL. self-update is a
// thin wrapper around this script — the script is the single source of truth
// for OS/arch detection, checksum verification, prefix logic, and PATH advice.
const installScriptURL = "https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh"

var selfUpdatePrefix string

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Re-run the install script to upgrade to the latest release",
	Long: `Re-executes the canonical install script (` + installScriptURL + `).
The script downloads the latest release, verifies SHA256 against checksums.txt,
and atomically replaces the eidos binary at the same prefix this binary lives at.

Refuses to run from a developer (Version=="dev") build — use 'make build' for
local development.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if version.Version == "dev" {
			return fmt.Errorf("refusing to self-update a dev build (Version=%q); rebuild from source instead", version.Version)
		}

		// Resolve the install prefix. Default: parent of the directory holding
		// the running binary (so a binary at /usr/local/bin/eidos installs to
		// /usr/local). Override via --prefix.
		prefix := selfUpdatePrefix
		if prefix == "" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate running binary: %w", err)
			}
			binDir := filepath.Dir(exe)   // .../bin
			prefix = filepath.Dir(binDir) // ...
			if filepath.Base(binDir) != "bin" {
				// Unusual install layout — fall back to the binary's parent
				// and let the install script work it out.
				prefix = binDir
			}
		}

		shell, err := exec.LookPath("sh")
		if err != nil {
			return fmt.Errorf("self-update requires 'sh' on PATH: %w", err)
		}
		curl, err := exec.LookPath("curl")
		if err != nil {
			return fmt.Errorf("self-update requires 'curl' on PATH: %w", err)
		}

		fmt.Printf("self-update: prefix=%s\n", prefix)
		fmt.Printf("self-update: fetching %s\n", installScriptURL)

		// Hand off the process via syscall.Exec — the install script replaces
		// us completely.
		pipeline := fmt.Sprintf("%s -fsSL %q | PREFIX=%q sh", curl, installScriptURL, prefix)
		return syscall.Exec(shell, []string{"sh", "-c", pipeline}, os.Environ())
	},
}

func init() {
	selfUpdateCmd.Flags().StringVar(&selfUpdatePrefix, "prefix", "", "install prefix (default: derived from running binary path)")
}
