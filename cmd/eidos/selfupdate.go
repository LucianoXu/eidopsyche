package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/update"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

// installScriptURL is the canonical install/upgrade URL. self-update is a
// thin wrapper around this script — the script is the single source of truth
// for OS/arch detection, checksum verification, prefix logic, and PATH advice.
const installScriptURL = "https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh"

// selfUpdateFetchTimeout caps the latest-version probe before falling back
// to the install script. Generous compared to the daemon's 2 s background
// refresh — the user is waiting on this command, but we still want to bail
// quickly enough that a dead network does not block forever.
const selfUpdateFetchTimeout = 5 * time.Second

var (
	selfUpdatePrefix string
	selfUpdateForce  bool
)

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Upgrade eidos to the latest release (no-op when already on latest)",
	Long: `Probes GitHub Releases for the latest tag and compares it to the running
binary's version. If the running binary is already at or ahead of the latest
release, self-update is a no-op. Otherwise it re-executes the canonical
install script (` + installScriptURL + `) which downloads the new archive,
verifies SHA256 against checksums.txt, and atomically replaces the eidos
binary at the same prefix this binary lives at.

Refuses to run from a developer (Version=="dev") build — use 'make build'
for local development.

--force bypasses the version check and reinstalls unconditionally (useful
for repairing a corrupted install or pinning to a specific version with
EIDOS_VERSION=...).`,
	RunE: runSelfUpdate,
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	if version.Version == "dev" {
		return fmt.Errorf("refusing to self-update a dev build (Version=%q); rebuild from source instead", version.Version)
	}

	if !selfUpdateForce {
		latest, err := update.FetchLatestTag(selfUpdateFetchTimeout)
		switch {
		case err != nil:
			// Probe failed (offline, GitHub flake). Fall through to the install
			// script — it has its own retry and will surface a real error if the
			// network is genuinely down.
			fmt.Fprintf(os.Stderr, "self-update: could not check latest version (%v); proceeding anyway\n", err)
		case latest == version.Version:
			fmt.Printf("eidos %s is already the latest version. Use --force to reinstall.\n", version.Version)
			return nil
		case !update.IsNewerVersion(version.Version, latest):
			// Running version is ahead of latest published. Could be a tag race
			// or a developer-tagged build. Don't downgrade silently.
			fmt.Printf("eidos %s is ahead of the latest published release (%s). Use --force to reinstall %s.\n",
				version.Version, latest, latest)
			return nil
		default:
			fmt.Printf("self-update: %s → %s\n", version.Version, latest)
		}
	}

	prefix := selfUpdatePrefix
	if prefix == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate running binary: %w", err)
		}
		binDir := filepath.Dir(exe)   // .../bin
		prefix = filepath.Dir(binDir) // ...
		if filepath.Base(binDir) != "bin" {
			// Unusual install layout — fall back to the binary's parent and
			// let the install script work it out.
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

	// Hand off the process via syscall.Exec — the install script replaces us
	// completely.
	pipeline := fmt.Sprintf("%s -fsSL %q | PREFIX=%q sh", curl, installScriptURL, prefix)
	return syscall.Exec(shell, []string{"sh", "-c", pipeline}, os.Environ())
}

func init() {
	selfUpdateCmd.Flags().StringVar(&selfUpdatePrefix, "prefix", "", "install prefix (default: derived from running binary path)")
	selfUpdateCmd.Flags().BoolVar(&selfUpdateForce, "force", false, "skip the version check and reinstall unconditionally")
}
