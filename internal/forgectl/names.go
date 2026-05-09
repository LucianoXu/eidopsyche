// Package forgectl is the host-side Docker orchestration layer for
// `eidos forge ...`. It does not run inside the mind-form container.
package forgectl

import (
	"fmt"
	"regexp"
)

const (
	// VolumePrefix is prepended to mind-form names to derive the Docker
	// volume name. e.g., name "alice" -> "eidos-mindform-alice".
	VolumePrefix = "eidos-mindform-"
	// ContainerPrefix mirrors VolumePrefix; the container shares the same
	// suffix as its volume.
	ContainerPrefix = "eidos-mindform-"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// ValidateName enforces a mind-form name is a short, lowercase identifier.
// Rejects anything that would produce a confusing Docker name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid mind-form name %q: must be 2-32 chars, lowercase, [a-z0-9-], start with a letter", name)
	}
	return nil
}

// VolumeName returns the Docker volume name for a mind-form.
func VolumeName(name string) string { return VolumePrefix + name }

// ContainerName returns the Docker container name for a mind-form.
func ContainerName(name string) string { return ContainerPrefix + name }
