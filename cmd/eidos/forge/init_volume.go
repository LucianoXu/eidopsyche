package forge

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newInitVolumeCmd is invoked by the host's `eidos forge create` flow as
// the entrypoint of a one-shot init container. It:
//
//  1. Reads a tar of the rendered ontology template from stdin and
//     extracts it under /eidos/ontology/.
//  2. Initializes the in-container gate (key + state.db + config.toml)
//     under /eidos/gate/ using flags from EIDOS_FORGE_* env vars.
//  3. Adds the master contact (--owner) at tier=master.
//  4. Clones /opt/eidopsyche-bundle.git into /eidos/ontology/eidopsyche/
//     and removes its origin remote.
//  5. git-inits the parent ontology and creates the "first breath" commit.
//
// This subcommand is hidden from the public help; it is only meaningful as
// the init container's command.
func newInitVolumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "init-volume",
		Short:  "Internal: bootstrap a fresh mind-form volume",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInitVolume(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin)
		},
	}
}

func runInitVolume(stdout, stderr io.Writer, stdin io.Reader) error {
	const ontologyDir = "/eidos/ontology"
	const gateDir = "/eidos/gate"
	const claudeDir = "/eidos/claude"
	const bundlePath = "/opt/eidopsyche-bundle.git"

	if err := os.MkdirAll(ontologyDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(gateDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return err
	}

	if err := extractTar(stdin, ontologyDir); err != nil {
		return fmt.Errorf("extract template: %w", err)
	}

	label := os.Getenv("EIDOS_FORGE_LABEL")
	owner := os.Getenv("EIDOS_FORGE_OWNER")
	relay := os.Getenv("EIDOS_FORGE_RELAY")
	if label == "" || owner == "" || relay == "" {
		return errors.New("EIDOS_FORGE_LABEL, EIDOS_FORGE_OWNER, EIDOS_FORGE_RELAY must be set")
	}

	if err := runCmd(stderr, "eidos", "gate", "init",
		"--state-dir", gateDir,
		"--label", label,
		"--home", relay,
	); err != nil {
		return fmt.Errorf("gate init: %w", err)
	}

	if err := runCmd(stderr, "eidos", "gate", "add-contact",
		"--state-dir", gateDir,
		owner,
		"--relay", relay,
		"--label", "master",
		"--tier", "master",
	); err != nil {
		return fmt.Errorf("gate add-contact (master): %w", err)
	}

	if err := runCmd(stderr, "git", "clone", bundlePath, filepath.Join(ontologyDir, "eidopsyche")); err != nil {
		return fmt.Errorf("clone eidopsyche: %w", err)
	}
	if err := runCmdInDir(stderr, filepath.Join(ontologyDir, "eidopsyche"), "git", "remote", "remove", "origin"); err != nil {
		return fmt.Errorf("remove origin: %w", err)
	}

	if err := runCmdInDir(stderr, ontologyDir, "git", "init", "-q"); err != nil {
		return fmt.Errorf("git init ontology: %w", err)
	}
	if err := runCmdInDir(stderr, ontologyDir, "git", "add", "."); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := runCmdInDir(stderr, ontologyDir, "git", "-c", "user.name=mindform", "-c", "user.email=mindform@local", "commit", "-q", "-m", "first breath"); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	fmt.Fprintln(stdout, "init-volume: ok")
	return nil
}

func extractTar(r io.Reader, target string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(target, h.Name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, fs.FileMode(h.Mode)|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode)|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func runCmd(stderr io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	return cmd.Run()
}

func runCmdInDir(stderr io.Writer, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	return cmd.Run()
}
