package wake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	pendingName = "pending.json"
	activeName  = "active.json"
	lockName    = ".lock"
)

// PendingPath returns the absolute path of the pending slot for a wake dir.
func PendingPath(dir string) string { return filepath.Join(dir, pendingName) }

// ActivePath returns the absolute path of the active slot for a wake dir.
func ActivePath(dir string) string { return filepath.Join(dir, activeName) }

// WritePending atomically replaces the pending slot.
//
// Caller is expected to have already merged with any existing pending under
// the wake-dir flock; see Merge for the merging rules.
func WritePending(dir string, sig Signal) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir wake dir: %w", err)
	}
	if sig.V == 0 {
		sig.V = SchemaVersion
	}
	body, err := json.Marshal(sig)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "pending-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, PendingPath(dir)); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	cleanup = false
	if err := fsyncDir(dir); err != nil {
		return err
	}
	return nil
}

// ReadPending returns the current pending wake, or nil if no pending file
// exists. It does NOT take the wake-dir flock; callers that read+merge+write
// must do so under Lock.
func ReadPending(dir string) (*Signal, error) {
	return readSlot(PendingPath(dir))
}

// ReadActive returns the current active wake, or nil if none.
func ReadActive(dir string) (*Signal, error) {
	return readSlot(ActivePath(dir))
}

// PromoteToActive moves pending.json -> active.json under the wake-dir flock.
// Returns the promoted signal, or nil if there was no pending. Errors only on
// IO failure. Holding the flock for the entire read+rename critical section
// prevents a concurrent Submit from replacing pending.json between the read
// and the rename, which would leave the caller with a stale in-memory signal
// while active.json on disk contains a newer write.
func PromoteToActive(dir string) (*Signal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir wake dir: %w", err)
	}
	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)

	src := PendingPath(dir)
	dst := ActivePath(dir)
	sig, err := readSlot(src)
	if err != nil {
		return nil, err
	}
	if sig == nil {
		return nil, nil
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("promote rename: %w", err)
	}
	if err := fsyncDir(dir); err != nil {
		return nil, err
	}
	return sig, nil
}

// ClearActive removes active.json. No-op if it does not exist.
func ClearActive(dir string) error {
	err := os.Remove(ActivePath(dir))
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Merge folds `next` into the existing pending slot's `prev` (which may be
// nil if there is no pending slot). The result adopts `next`'s primary
// fields (id, reason, triggered_at, hint, context) and increments
// coalesced_count, appending `prev`'s reason to coalesced_from.
//
// This is the merge rule named in the spec §6.3.
func Merge(prev *Signal, next Signal) Signal {
	if next.V == 0 {
		next.V = SchemaVersion
	}
	if prev == nil {
		if next.CoalescedFrom == nil {
			next.CoalescedFrom = []Reason{}
		}
		return next
	}
	out := next
	out.CoalescedCount = prev.CoalescedCount + 1
	from := make([]Reason, 0, len(prev.CoalescedFrom)+1)
	from = append(from, prev.CoalescedFrom...)
	from = append(from, prev.Reason)
	out.CoalescedFrom = from
	return out
}

// Submit is the producer-side entry point. It takes the wake-dir flock,
// reads pending if any, merges with `sig`, and atomically writes it back.
// Safe under concurrent producers (gate daemon, cron, manual).
func Submit(dir string, sig Signal) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir wake dir: %w", err)
	}
	lock, err := acquireLock(dir)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	prev, err := ReadPending(dir)
	if err != nil {
		return fmt.Errorf("read pending: %w", err)
	}
	merged := Merge(prev, sig)
	return WritePending(dir, merged)
}

// acquireLock and releaseLock are platform-specific (see wake_lock_unix.go
// and wake_lock_windows.go). They take a blocking exclusive lock on the
// wake-dir's .lock sentinel file, mirroring the cross-platform pattern
// used in internal/daemon/lock_{unix,windows}.go.

func acquireLock(dir string) (*os.File, error) {
	path := filepath.Join(dir, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := platformLock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return f, nil
}

func releaseLock(f *os.File) {
	_ = platformUnlock(f)
	_ = f.Close()
}

func readSlot(path string) (*Signal, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s Signal
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &s, nil
}

// fsyncDir opens dir read-only and calls Sync() to flush directory-entry
// updates (e.g. renames) to stable storage. Without this, a kernel crash
// after os.Rename may leave the directory entry update un-durable on most
// filesystems.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("fsync wake dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync wake dir: %w", err)
	}
	return nil
}
