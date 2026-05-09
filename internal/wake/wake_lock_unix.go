//go:build unix

package wake

import (
	"os"
	"syscall"
)

// platformLock takes a blocking exclusive flock on f. Producers (gate,
// cron, manual) and the consumer (PromoteToActive) coordinate through
// this lock; blocking is the desired semantics — see Submit's call site.
func platformLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// platformUnlock releases a previously-held flock.
func platformUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
