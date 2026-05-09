//go:build windows

package wake

import (
	"os"

	"golang.org/x/sys/windows"
)

// platformLock takes a blocking exclusive lock on f via LockFileEx. The
// blocking call (no LOCKFILE_FAIL_IMMEDIATELY) matches the Unix flock
// (LOCK_EX without LOCK_NB) so producers and consumers serialise the
// same way on both platforms.
func platformLock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), overlapped)
}

// platformUnlock releases a previously-held lock via UnlockFileEx.
func platformUnlock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), overlapped)
}
