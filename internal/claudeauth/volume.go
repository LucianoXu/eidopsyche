package claudeauth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// VolumeWriter is the surface claudeauth needs to install creds into a
// mindform's volume. Production wiring uses forgectl + one-shot docker
// runs; tests substitute fakes.
type VolumeWriter interface {
	// Write puts body at the given path inside the volume, relative
	// to /eidos. Implementations chmod 600.
	Write(relPath string, body []byte) error
	// Remove deletes a file from the volume at the given path
	// relative to /eidos. Idempotent (rm -f semantics).
	Remove(relPath string) error
	// ClearAuthRequired removes /eidos/run/auth_required.json so the
	// supervisor's self-gate stops blocking the next wake.
	ClearAuthRequired() error
}

// WriteToVolume validates the blob, writes it into the mindform's
// volume at /eidos/claude/.claude/.credentials.json, removes any
// stale setup-token file so the file path wins unambiguously, and
// clears the auth_required marker. Returns the validation error
// verbatim so the operator sees which field is missing.
func WriteToVolume(ctx context.Context, name, image string, blob []byte) error {
	w, err := NewForgectlVolumeWriter(ctx, name, image)
	if err != nil {
		return err
	}
	return writeToVolumeUsing(w, blob)
}

// WriteToVolumeUsing is the test seam — accepts an arbitrary VolumeWriter
// so callers and tests can substitute fakes. Production callers should
// use WriteToVolume.
func WriteToVolumeUsing(w VolumeWriter, blob []byte) error {
	return writeToVolumeUsing(w, blob)
}

// writeToVolumeUsing is the unexported core.
func writeToVolumeUsing(w VolumeWriter, blob []byte) error {
	if err := Validate(blob); err != nil {
		return err
	}
	if err := w.Write(CredentialsFile, blob); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	// Mirror WriteSetupTokenToVolume's mutual-exclusion guarantee: if a
	// previous --setup-token-stdin left an env-path setup_token behind,
	// the supervisor would inject CLAUDE_CODE_OAUTH_TOKEN at spawn time
	// AND claude would also see the new credentials.json. Claude's LN5
	// guard would then drop the env var and use the file (correct), but
	// the operator's intent here is "this credentials blob replaces
	// whatever auth I had" — clean up so inspection matches behaviour.
	if err := w.Remove(SetupTokenFile); err != nil {
		return fmt.Errorf("remove stale setup-token: %w", err)
	}
	if err := w.ClearAuthRequired(); err != nil {
		return fmt.Errorf("clear auth_required: %w", err)
	}
	return nil
}

// NewForgectlVolumeWriter returns the production VolumeWriter wired to
// the host docker daemon.
func NewForgectlVolumeWriter(ctx context.Context, name, image string) (VolumeWriter, error) {
	dc, err := forgectl.New()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &forgectlWriter{ctx: ctx, client: dc, image: image, slug: name}, nil
}

// forgectlWriter adapts forgectl.WriteToVolume + a docker-run rm to the
// VolumeWriter interface.
type forgectlWriter struct {
	ctx    context.Context
	client forgectl.Client
	image  string
	slug   string
}

func (f *forgectlWriter) Write(relPath string, body []byte) error {
	return forgectl.WriteToVolume(f.ctx, f.client, f.image, f.slug, relPath, body)
}

func (f *forgectlWriter) Remove(relPath string) error {
	return f.runRemove(relPath)
}

func (f *forgectlWriter) ClearAuthRequired() error {
	return f.runRemove("run/auth_required.json")
}

// runRemove runs `rm -f /eidos/<relPath>` in a one-shot helper
// container as uid 0 — the auth_required marker and the credentials
// file may have been written by uid 1000 (supervisor) or uid 0
// (init-time root), and only root can remove both. relPath is
// validated to live inside /eidos (filepath.Clean drops any "..",
// rejecting attempts to escape) and the target is passed as a
// separate argv to docker's --entrypoint rm so shell metacharacters
// in relPath cannot alter command semantics.
func (f *forgectlWriter) runRemove(relPath string) error {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	if clean == "." || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("remove: relPath must be a normalised path under /eidos: %q", relPath)
	}
	target := filepath.ToSlash(filepath.Join("/eidos", clean))
	// --entrypoint rm with the target as a separate argv element avoids
	// the shell entirely — `-- <target>` neutralises any leading `-` in
	// the path so rm cannot interpret it as a flag (option injection).
	c := exec.CommandContext(f.ctx, "docker", "run", "--rm",
		"--user", "0:0",
		"--mount", "source="+forgectl.VolumeName(f.slug)+",target=/eidos",
		"--entrypoint", "rm",
		f.image,
		"-f", "--", target,
	) //nolint:gosec // argv built from validated inputs
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("rm -f %s: %w (stderr: %s)", target, err, stderr.String())
	}
	return nil
}
