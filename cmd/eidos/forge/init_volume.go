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

// initVolumeDirs are the per-mind-form volume directories the init pass
// creates as root and the final chownTree below transfers to
// eidos:eidos. /eidos/run must be in this list so the supervisor /
// agent-runner / gate daemon running as user 1000 can write into it
// without EACCES on the parent (transcripts/, agent.lock,
// dream-state.json, wake/, plans/...). Without this, /eidos/run gets
// created later — by entrypoint.sh, by the gate daemon's wake-dir
// setup, or by a sudo'd cron child — at a moment when it can land
// root-owned 0755, and the failure mode is silent: agent-runner's
// transcripts/transcript-list/transcript-tail all fail-soft and the
// operator just sees an empty `forge watch` with no error.
var initVolumeDirs = []string{
	"/eidos/ontology",
	"/eidos/gate",
	"/eidos/claude",
	"/eidos/run",
}

func runInitVolume(stdout, stderr io.Writer, stdin io.Reader) error {
	const bundlePath = "/opt/eidopsyche-bundle.git"
	const ontologyDir = "/eidos/ontology"
	const gateDir = "/eidos/gate"

	for _, d := range initVolumeDirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
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

	// CLI `eidos forge create` leaves CreateOpts.MindFormNpub empty
	// because the keypair is generated inside `gate init` above
	// (only the wizard pre-generates host-side and passes
	// EIDOS_FORGE_KEY_HEX). The scaffold tar therefore embedded an
	// empty mindgate_npub into self/identity.toml. Patch it now with
	// the npub of the key we just persisted so the agent-loop's Info
	// block — and any tool that reads identity.toml — reports the
	// real value. Idempotent: the wizard path overwrites with the
	// same npub it pre-generated.
	if err := patchIdentityNpub(ontologyDir, gateDir); err != nil {
		return fmt.Errorf("patch identity.toml npub: %w", err)
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

	if err := applyHeartbeatEnv(cfgPath, os.Getenv("EIDOS_FORGE_HEARTBEAT_INTERVAL")); err != nil {
		return fmt.Errorf("save gate config (heartbeat): %w", err)
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

// patchIdentityNpub reads <gateDir>/key, derives the mind-form's
// npub, and writes it into <ontologyDir>/self/identity.toml's
// mindgate_npub field. Used to repair the on-disk identity record
// for the CLI `eidos forge create` path, where the scaffold tar was
// rendered before `gate init` had a chance to mint the keypair.
//
// The rewrite preserves the existing identity.toml contents byte-
// for-byte except for the mindgate_npub line. We do not round-trip
// through a TOML encoder here because that would silently reorder
// keys and strip comments — and a single-line regex-style replace
// is well-defined when the file shape is the framework's own.
func patchIdentityNpub(ontologyDir, gateDir string) error {
	keypair, err := identity.LoadKey(filepath.Join(gateDir, "key"))
	if err != nil {
		return fmt.Errorf("load gate key: %w", err)
	}
	identityPath := filepath.Join(ontologyDir, "self", "identity.toml")
	body, err := os.ReadFile(identityPath)
	if err != nil {
		return fmt.Errorf("read identity.toml: %w", err)
	}
	lines := strings.Split(string(body), "\n")
	patched := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "mindgate_npub") {
			lines[i] = fmt.Sprintf("mindgate_npub = %q", keypair.Npub)
			patched = true
			break
		}
	}
	if !patched {
		// First-write: append the field if the on-disk file somehow
		// lacks it (template regression guard).
		lines = append(lines, fmt.Sprintf("mindgate_npub = %q", keypair.Npub))
	}
	return os.WriteFile(identityPath, []byte(strings.Join(lines, "\n")), 0o600)
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

// applyHeartbeatEnv writes heartbeat.interval into the gate config when
// interval is non-empty; validates first via
// config.ValidateHeartbeatInterval. Empty is a no-op (operator did not
// pin a cadence at create time, so the supervisor uses
// config.DefaultHeartbeatInterval at PID-1 startup).
func applyHeartbeatEnv(cfgPath, interval string) error {
	if interval == "" {
		return nil
	}
	if err := config.ValidateHeartbeatInterval(interval); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load gate config: %w", err)
	}
	cfg.Heartbeat.Interval = interval
	return config.Save(cfgPath, cfg)
}

// safeTarPath validates a tar entry name and returns the joined destination
// rooted under target. Returns an error for absolute paths, paths whose raw
// form contains "..", or paths whose cleaned form escapes target. Treating
// raw `..` segments as fatal (not just post-clean escapes) defends against
// tools that strip outermost `..` only.
func safeTarPath(target, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("tar entry has empty Name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("tar entry rejected (absolute path): %q", name)
	}
	// Normalize separators (tar archives canonically use forward slashes)
	// and check for ".." anywhere in the raw segments.
	normalized := filepath.ToSlash(name)
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", fmt.Errorf("tar entry rejected (parent traversal): %q", name)
		}
	}
	dst := filepath.Join(target, filepath.FromSlash(normalized))
	rel, err := filepath.Rel(target, dst)
	if err != nil {
		return "", fmt.Errorf("tar entry %q: %w", name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tar entry escapes target after clean: %q", name)
	}
	return dst, nil
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
		dst, err := safeTarPath(target, h.Name)
		if err != nil {
			return err
		}
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
		default:
			// Fail loud on symlinks, hardlinks, char/block devices, FIFOs.
			// Previous behavior was a silent skip, which would let a hostile
			// tar quietly drop unexpected payloads — and is one careless
			// future "case tar.TypeSymlink: os.Symlink(...)" away from a
			// real escape. Refuse instead.
			return fmt.Errorf("tar entry %q has unsupported type 0x%x", h.Name, h.Typeflag)
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
