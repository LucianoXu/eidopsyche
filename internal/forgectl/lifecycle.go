package forgectl

import (
	"context"
	"fmt"
	"log"
)

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
