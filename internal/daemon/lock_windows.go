//go:build windows

package daemon

import (
	"os"

	"golang.org/x/sys/windows"
)

// acquireExclusiveLock takes a non-blocking exclusive lock on the file using
// LockFileEx. Returns an error if the lock is held by another process.
func acquireExclusiveLock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), overlapped)
}

// releaseLock releases a previously-acquired lock via UnlockFileEx.
func releaseLock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), overlapped)
}
