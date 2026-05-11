package claudeauth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// VolumeWriter is the surface claudeauth needs to install creds into a
// mindform's volume. Production wiring uses forgectl + a one-shot
// docker run to remove the auth_required marker; tests substitute fakes.
type VolumeWriter interface {
	// Write puts body at the given path inside the volume, relative
	// to /eidos. Implementations chmod 600.
	Write(relPath string, body []byte) error
	// ClearAuthRequired removes /eidos/run/auth_required.json so the
	// supervisor's self-gate stops blocking the next wake.
	ClearAuthRequired() error
}

// WriteToVolume validates the blob, writes it into the mindform's
// volume at /eidos/claude/.claude/.credentials.json, and clears the
// auth_required marker. Returns the validation error verbatim so the
// operator sees which field is missing.
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
	if err := w.Write("claude/.claude/.credentials.json", blob); err != nil {
		return fmt.Errorf("write credentials: %w", err)
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

func (f *forgectlWriter) ClearAuthRequired() error {
	// One-shot helper container running as uid 0; auth_required.json may
	// have been written by either the in-container uid 1000 supervisor
	// or an init-time root process — only root can remove both. Tolerates
	// absence via rm -f.
	c := exec.CommandContext(f.ctx, "docker", "run", "--rm",
		"--user", "0:0",
		"--mount", "source="+forgectl.VolumeName(f.slug)+",target=/eidos",
		"--entrypoint", "sh",
		f.image,
		"-c", "rm -f /eidos/run/auth_required.json",
	) //nolint:gosec // argv built from validated inputs
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("clear auth_required: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}
