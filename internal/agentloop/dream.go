package agentloop

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/fsnotify/fsnotify"
)

// DreamWatcherConfig wires the dream watcher.
type DreamWatcherConfig struct {
	Path                string
	OnRotationRequested func()
}

// DreamWatcher fsnotify-watches /eidos/run/dream-state.json, maintains
// an atomic dreaming flag, and buffers wakes that arrive while
// dreaming.
type DreamWatcher struct {
	cfg      DreamWatcherConfig
	dreaming atomic.Bool

	mu      sync.Mutex
	backlog []wake.Signal
	lastFin int64
}

// NewDreamWatcher constructs a DreamWatcher. Call Run to start the
// fsnotify loop.
func NewDreamWatcher(cfg DreamWatcherConfig) *DreamWatcher {
	return &DreamWatcher{cfg: cfg}
}

// Dreaming returns the current atomic flag value. Safe from any
// goroutine.
func (w *DreamWatcher) Dreaming() bool { return w.dreaming.Load() }

// AppendToBacklog appends a wake to the in-memory backlog (called by
// the forwarder when Dreaming() is true).
func (w *DreamWatcher) AppendToBacklog(s wake.Signal) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.backlog = append(w.backlog, s)
}

// DrainBacklog returns the backlog and resets it. Called by the
// rotation goroutine after the new claude is ready.
func (w *DreamWatcher) DrainBacklog() []wake.Signal {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.backlog
	w.backlog = nil
	return out
}

// SetDreamingFalse releases backlog buffering. Called by the rotation
// goroutine at the end of rotation.
func (w *DreamWatcher) SetDreamingFalse() { w.dreaming.Store(false) }

// Run watches dream-state.json until ctx cancels. Returns the watcher
// error if fsnotify fails to start.
func (w *DreamWatcher) Run(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}
	defer fsw.Close()
	dir := filepath.Dir(w.cfg.Path)
	if err := fsw.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}
	// Initial read so we don't miss state present at startup.
	w.reread()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != filepath.Base(w.cfg.Path) {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			w.reread()
		case err := <-fsw.Errors:
			return fmt.Errorf("fsnotify error: %w", err)
		}
	}
}

// reread loads the current dream-state.json and updates the dreaming
// flag and rotation channel as appropriate.
func (w *DreamWatcher) reread() {
	st, err := dreamstate.Read(w.cfg.Path)
	if err != nil {
		fmt.Fprintf(stderr(), "agent-loop: dream: read: %v\n", err)
		return
	}
	if st.CurrentlyDreaming && !w.dreaming.Load() {
		w.dreaming.Store(true)
		return
	}
	if !st.CurrentlyDreaming && w.dreaming.Load() {
		w.mu.Lock()
		grew := st.LastDreamFinishedAt > w.lastFin
		if grew {
			w.lastFin = st.LastDreamFinishedAt
		}
		w.mu.Unlock()
		if grew && w.cfg.OnRotationRequested != nil {
			w.cfg.OnRotationRequested()
		}
		// dreaming flag stays true until rotation calls SetDreamingFalse
	}
}
