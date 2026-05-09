package forge

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
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
	keyHex := os.Getenv("EIDOS_FORGE_KEY_HEX")
	if label == "" || owner == "" || relay == "" {
		return errors.New("EIDOS_FORGE_LABEL, EIDOS_FORGE_OWNER, EIDOS_FORGE_RELAY must be set")
	}

	gateInitArgs := []string{"gate", "init",
		"--state-dir", gateDir,
		"--label", label,
		"--home", relay,
	}
	if keyHex != "" {
		// First Contact wizard path: the host pre-generated the keypair so
		// the operator's summoning book could include the MindForm's npub
		// before the volume existed. Pre-write the key here, then tell
		// `gate init` to adopt it instead of generating a fresh one.
		k, err := identity.FromHex(keyHex)
		if err != nil {
			return fmt.Errorf("EIDOS_FORGE_KEY_HEX: %w", err)
		}
		if err := identity.SaveKey(filepath.Join(gateDir, "key"), k); err != nil {
			return fmt.Errorf("save host-supplied key: %w", err)
		}
		gateInitArgs = append(gateInitArgs, "--key-from-existing")
	}
	if err := runCmd(stderr, "eidos", gateInitArgs...); err != nil {
		return fmt.Errorf("gate init: %w", err)
	}

	// Patch the freshly-written gate config so the in-container daemon's
	// wake hook fires when an inbound NIP-17 message lands. The default
	// Config.Wake.Dir is empty (host gate has no wake-output behavior);
	// in the mind-form container we want the daemon to write
	// pending.json into /eidos/run/wake/, which the supervisor watches.
	cfgPath := filepath.Join(gateDir, "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load gate config: %w", err)
	}
	cfg.Wake.Dir = "/eidos/run/wake"
	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("save gate config (wake dir): %w", err)
	}

	if err := applyModelEnv(cfgPath, os.Getenv("EIDOS_FORGE_MODEL")); err != nil {
		return fmt.Errorf("save gate config (model): %w", err)
	}

	// Add the master contact directly to state.db. We bypass `eidos gate
	// add-contact` here on purpose: per the SPEC's single-call-path rule,
	// every operator action goes through the gate daemon's methodTable —
	// but during init-volume there is no daemon socket yet (the supervisor
	// will start the daemon at first boot). The SPEC's exception clause
	// names exactly this case ("Only when daemon does not exist or is not
	// reachable... directly read/write state files") and requires the
	// exception to be explicitly noted at the call site. This is that
	// note.
	if err := addMasterContactDirect(stderr, gateDir, owner, relay); err != nil {
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

	// Final ownership pass: init-volume runs as root via
	// RunInitOpts.User="0:0" so it can extract the template tar, clone
	// the bundle, and git-init the parent ontology with full privileges.
	// The persistent container starts as eidos (uid 1000, image's USER
	// directive), so we hand the volume off here. Hard-coded uid:gid
	// 1000:1000 matches the Dockerfile's addgroup/adduser; resolving
	// "eidos" via os/user would add a CGO dependency this binary avoids
	// on principle.
	if err := chownTree("/eidos", 1000, 1000); err != nil {
		return fmt.Errorf("chown /eidos to eidos:eidos: %w", err)
	}

	fmt.Fprintln(stdout, "init-volume: ok")
	return nil
}

// chownTree recursively chowns every entry under root to uid:gid. Uses
// Lchown so symlinks themselves get chowned, not their targets.
// Idempotent: a no-op when the tree is already correctly owned.
func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// applyModelEnv writes mindform.model into the gate config when model
// is non-empty; validates first via config.ValidateModelID. Empty model
// is a no-op (the operator did not pin a model at create time).
func applyModelEnv(cfgPath, model string) error {
	if model == "" {
		return nil
	}
	if err := config.ValidateModelID(model); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load gate config: %w", err)
	}
	cfg.MindForm.Model = model
	return config.Save(cfgPath, cfg)
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

// addMasterContactDirect opens state.db and inserts the owner as a
// master-tier contact, using the relay URL as the contact's relay hint.
// Accepts owner as either a bech32 npub or a 64-char hex pubkey, since
// the host's `eidos forge create` flag-validates an npub but operators
// could conceivably override.
func addMasterContactDirect(stderr io.Writer, gateDir, ownerNpubOrHex, relay string) error {
	hex, err := decodeNpubOrHex(ownerNpubOrHex)
	if err != nil {
		return fmt.Errorf("decode owner: %w", err)
	}
	dbPath := filepath.Join(gateDir, "state.db")
	db, err := store.Open(dbPath, false)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()
	repo := contacts.New(db)
	ctx := context.Background()
	if err := repo.Add(ctx, contacts.Contact{
		Pubkey: hex,
		Label:  "master",
		Tier:   contacts.TierMaster,
		Relays: []string{relay},
	}); err != nil {
		// Duplicate is OK (idempotent re-init).
		if errors.Is(err, contacts.ErrExists) {
			fmt.Fprintln(stderr, "master contact already present, skipping")
			return nil
		}
		return err
	}
	return nil
}

// decodeNpubOrHex normalizes an owner identifier to lowercase 64-char hex.
func decodeNpubOrHex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "npub1") {
		return identity.DecodeNpub(s)
	}
	if len(s) != 64 {
		return "", fmt.Errorf("owner must be a 64-char hex pubkey or an npub1… string (got %d chars)", len(s))
	}
	return strings.ToLower(s), nil
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
