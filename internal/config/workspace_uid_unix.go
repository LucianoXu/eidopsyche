//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
)

// HostPathOwnerUID stat()'s p and returns the owning uid. Unix-only;
// see workspace_uid_windows.go for the Windows stub.
func HostPathOwnerUID(p string) (uint32, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", p, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat %s: cannot read owner uid (unexpected Sys type)", p)
	}
	return sys.Uid, nil
}
