package daemon

import (
	"context"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// MindFormKnown reports whether name has a container or volume on
// this host. forge.workspace.* handlers use this to reject typos.
func (d *Daemon) MindFormKnown(name string) bool {
	if _, ok := d.testKnownMindForms[name]; ok {
		return true
	}
	if d.forgectlClient == nil {
		return false
	}
	ctx := context.Background()
	if exists, _ := d.forgectlClient.ContainerExists(ctx, forgectl.ContainerName(name)); exists {
		return true
	}
	if exists, _ := d.forgectlClient.VolumeExists(ctx, forgectl.VolumeName(name)); exists {
		return true
	}
	return false
}

// SeedMindForm registers name as a known mind-form for the duration
// of the test. Production callers must not use this — there's no
// reset path. Lives next to the production probe so the two can be
// reasoned about together.
func (d *Daemon) SeedMindForm(name string) {
	if d.testKnownMindForms == nil {
		d.testKnownMindForms = map[string]struct{}{}
	}
	d.testKnownMindForms[name] = struct{}{}
}
