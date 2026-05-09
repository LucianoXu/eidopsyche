//go:build !windows

package identity

import (
	"fmt"
	"os"
)

// checkKeyACL enforces the historical "0600 only" rule on unix. The bit test
// rejects any group / world access, mirroring how OpenSSH guards id_rsa.
func checkKeyACL(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat key file: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("keystore %s has insecure mode %04o (want 0600)", path, mode)
	}
	return nil
}

// protectKeyFile re-applies 0600 in case the file existed under a wider mode
// before SaveKey overwrote it. os.WriteFile honours the mode only on file
// creation, so on an in-place rewrite we still need an explicit chmod.
func protectKeyFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod 0600 %s: %w", path, err)
	}
	return nil
}
