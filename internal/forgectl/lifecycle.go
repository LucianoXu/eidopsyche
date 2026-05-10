package forgectl

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// WriteToVolume writes a small file (calling-words, birth.json, etc.)
// into a MindForm's volume by running a one-shot helper container that
// `tee`s stdin into the destination path. relPath is interpreted
// relative to /eidos inside the volume.
//
// This is used by the First Contact wizard's phase 3 to drop the
// calling-words and birth.json into the volume between the
// forge.Orchestrate (which created the volume + persistent container,
// stopped) and the ContainerStart (which runs the supervisor and
// triggers the birth-wake).
//
// Reuses the same image the persistent container will run, which is
// already present locally at this point.
func WriteToVolume(ctx context.Context, c Client, image, slug, relPath string, body []byte) error {
	if strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "..") {
		return fmt.Errorf("WriteToVolume: relPath must be relative under /eidos: %q", relPath)
	}
	target := filepath.ToSlash(filepath.Join("/eidos", relPath))
	dir := filepath.ToSlash(filepath.Dir(target))
	// Use sh + tee under root so we can write anywhere in the volume
	// regardless of the persistent container's USER.
	script := fmt.Sprintf("mkdir -p %q && cat > %q && chown -R 1000:1000 %q",
		dir, target, dir)
	res, err := c.RunInit(ctx, RunInitOpts{
		Image: image,
		Mount: Mount{VolumeName: VolumeName(slug), Target: "/eidos"},
		User:  "0:0",
		Cmd:   []string{"sh", "-c", script},
		Stdin: bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("write %s to volume: %w (stderr: %s)", relPath, err, string(res.Stderr))
	}
	return nil
}

// PurgeForFailedSummon removes the container and volume for a mind-form
// whose First Contact ritual aborted post-orchestrate (e.g. boot-wake
// timeout). It is tolerant of either resource being absent and logs an
// explicit "abandoned-summon" line so operators can distinguish this
// teardown from a normal `eidos forge purge`.
//
// The two operations are best-effort: a failure on container-remove
// still attempts the volume-remove. The first error encountered is
// returned (so callers see the original cause), but cleanup proceeds.
func PurgeForFailedSummon(ctx context.Context, c Client, name string) error {
	cont := ContainerName(name)
	vol := VolumeName(name)
	log.Printf("forgectl: abandoned-summon teardown for %q (container=%s, volume=%s)", name, cont, vol)
	var firstErr error
	if exists, err := c.ContainerExists(ctx, cont); err == nil && exists {
		if rmErr := c.ContainerRemove(ctx, cont); rmErr != nil {
			firstErr = fmt.Errorf("remove container %s: %w", cont, rmErr)
		}
	} else if err != nil {
		firstErr = fmt.Errorf("inspect container %s: %w", cont, err)
	}
	if exists, err := c.VolumeExists(ctx, vol); err == nil && exists {
		if rmErr := c.VolumeRemove(ctx, vol); rmErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove volume %s: %w", vol, rmErr)
			}
		}
	} else if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("inspect volume %s: %w", vol, err)
	}
	return firstErr
}
