//go:build windows

package config

import "fmt"

// HostPathOwnerUID has no meaningful implementation on Windows (no
// posix-style uid on the host filesystem); the daemon's add handler
// treats any non-nil error as "skip the warning". Returning a
// well-formed error here is non-fatal — the operator just doesn't
// get the uid-mismatch hint, which is the right outcome on a host
// that doesn't have uid 1000 as a concept anyway.
func HostPathOwnerUID(p string) (uint32, error) {
	return 0, fmt.Errorf("HostPathOwnerUID: not supported on windows (no posix uid)")
}
