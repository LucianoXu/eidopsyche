//go:build unix

package dreamstate

import (
	"os"
	"syscall"
)

// platformLock takes a blocking exclusive flock on f.
func platformLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// platformUnlock releases a previously-held flock.
func platformUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
