//go:build unix

package daemon

import (
	"os"
	"syscall"
)

// acquireExclusiveLock takes a non-blocking exclusive flock on the file.
// Returns an error if the lock is already held by another process.
func acquireExclusiveLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// releaseLock releases a previously-acquired flock.
func releaseLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
