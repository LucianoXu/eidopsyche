package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// transcriptHandle owns the per-wake transcript file and the underlying
// store's bookkeeping. It exists so runWithTranscript can stay focused
// on coordinating the claude subprocess.
type transcriptHandle struct {
	store  *transcript.Store
	out    *os.File
	wakeID string
	closed bool
}

// openTranscriptHandle prepares a store under dir, recovers any partial
// state, and opens out for wakeID. On a same-second same-reason
// collision it retries once with a process-unique suffix.
func openTranscriptHandle(dir, wakeID string) (*transcriptHandle, error) {
	store, err := transcript.NewStore(dir)
	if err != nil {
		return nil, fmt.Errorf("transcripts: %w", err)
	}
	if rerr := store.Recover(); rerr != nil {
		log.Printf("agent-runner: transcripts recover: %v", rerr)
	}
	out, err := store.Open(wakeID)
	if err != nil && errors.Is(err, fs.ErrExist) {
		altID := fmt.Sprintf("%s-dup-%d-%d", wakeID, os.Getpid(), time.Now().UnixNano())
		log.Printf("agent-runner: transcripts open(%s) collided; retrying as %s", wakeID, altID)
		wakeID = altID
		out, err = store.Open(wakeID)
	}
	if err != nil {
		return nil, fmt.Errorf("transcripts open(%s): %w", wakeID, err)
	}
	return &transcriptHandle{store: store, out: out, wakeID: wakeID}, nil
}

// Write implements io.Writer for the per-wake transcript file. It is
// safe to wrap in a drain goroutine.
func (h *transcriptHandle) Write(p []byte) (int, error) { return h.out.Write(p) }

// close releases the file. Safe to call more than once.
func (h *transcriptHandle) close() {
	if h.closed {
		return
	}
	_ = h.out.Close()
	h.closed = true
}

// finalize closes the file (idempotently) and writes the index entry.
func (h *transcriptHandle) finalize(entry transcript.Entry, maxCount int, maxBytes int64) error {
	h.close()
	return h.store.Finalize(entry, maxCount, maxBytes)
}
