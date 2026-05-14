package daemon

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strings"
)

// workspaceContainerEntry is the shape returned to mind-forms by the
// in-container state.get forge.workspaces handler. No host_path field
// — that would leak host filesystem layout into the container.
type workspaceContainerEntry struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type forgeWorkspacesContrib struct{ d *Daemon }

func (c forgeWorkspacesContrib) Path() string { return "forge.workspaces" }

func (c forgeWorkspacesContrib) Snapshot(_ context.Context) (any, error) {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		// On non-Linux hosts or sandboxed test runners /proc may be
		// unreadable; surface an empty list rather than erroring so
		// callers can degrade gracefully.
		return []workspaceContainerEntry{}, nil
	}
	return parseWorkspacesFromMounts(data), nil
}

// parseWorkspacesFromMounts is the pure helper, split for testability.
func parseWorkspacesFromMounts(data []byte) []workspaceContainerEntry {
	out := []workspaceContainerEntry{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mp, opts := fields[1], fields[3]
		const prefix = "/workspace/"
		if !strings.HasPrefix(mp, prefix) {
			continue
		}
		name := strings.TrimPrefix(mp, prefix)
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		mode := "rw"
		for _, o := range strings.Split(opts, ",") {
			if o == "ro" {
				mode = "ro"
				break
			}
		}
		out = append(out, workspaceContainerEntry{Name: name, Mode: mode})
	}
	return out
}
