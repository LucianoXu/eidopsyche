// Package fileops collects small filesystem helpers shared across packages.
package fileops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// AtomicWrite writes body to path durably by writing a sibling temp file
// first and renaming it into place. The temp file lives in the same
// directory so the rename is atomic on POSIX. On any failure the temp
// file is removed.
//
// Callers are responsible for ensuring path's parent directory exists.
func AtomicWrite(path string, body []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
