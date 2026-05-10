package firstcontact

import (
	"context"
	"sync"
)

// TaskState is the per-task status reported through ReadyState.
type TaskState struct {
	Status string // "pending" | "ready" | "failed"
	Error  string
}

// ReadyState is the snapshot of all background-readiness tasks. The
// channel emits a fresh ReadyState whenever any task transitions.
type ReadyState struct {
	DockerImage TaskState
	KeyPair     TaskState
	HomeRelay   TaskState

	// MindFormNpub and MindFormKeyHex are populated when KeyPair.Status
	// is "ready" — phase 3 reads them off the final ReadyState before
	// invoking forge.Orchestrate.
	MindFormNpub   string
	MindFormKeyHex string
}

// AllReady reports whether every task has reached "ready".
func (r ReadyState) AllReady() bool {
	return r.DockerImage.Status == "ready" &&
		r.KeyPair.Status == "ready" &&
		r.HomeRelay.Status == "ready"
}

// AnyFailed reports whether any task has terminally failed.
func (r ReadyState) AnyFailed() bool {
	return r.DockerImage.Status == "failed" ||
		r.KeyPair.Status == "failed" ||
		r.HomeRelay.Status == "failed"
}

// ReadyDeps are the pluggable inputs to StartBackground. Each function
// is one of the three background tasks.
type ReadyDeps struct {
	PullImage    func(ctx context.Context) error
	GenerateKey  func() (npub, hex string, err error)
	ProbeRelay   func(ctx context.Context, url string) error
	HomeRelayURL string
}

// StartBackground spawns one goroutine per task and returns a buffered
// channel that emits a ReadyState every time a task transitions. The
// channel is closed when all tasks have completed (ready or failed).
func StartBackground(ctx context.Context, deps ReadyDeps) <-chan ReadyState {
	ch := make(chan ReadyState, 8)
	state := ReadyState{
		DockerImage: TaskState{Status: "pending"},
		KeyPair:     TaskState{Status: "pending"},
		HomeRelay:   TaskState{Status: "pending"},
	}
	var mu sync.Mutex
	emit := func(mut func(*ReadyState)) {
		mu.Lock()
		mut(&state)
		snap := state
		mu.Unlock()
		select {
		case ch <- snap:
		case <-ctx.Done():
		}
	}
	emit(func(_ *ReadyState) {})

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		err := deps.PullImage(ctx)
		emit(func(s *ReadyState) {
			if err != nil {
				s.DockerImage = TaskState{Status: "failed", Error: err.Error()}
			} else {
				s.DockerImage = TaskState{Status: "ready"}
			}
		})
	}()
	go func() {
		defer wg.Done()
		npub, hex, err := deps.GenerateKey()
		emit(func(s *ReadyState) {
			if err != nil {
				s.KeyPair = TaskState{Status: "failed", Error: err.Error()}
			} else {
				s.KeyPair = TaskState{Status: "ready"}
				s.MindFormNpub = npub
				s.MindFormKeyHex = hex
			}
		})
	}()
	go func() {
		defer wg.Done()
		err := deps.ProbeRelay(ctx, deps.HomeRelayURL)
		emit(func(s *ReadyState) {
			if err != nil {
				s.HomeRelay = TaskState{Status: "failed", Error: err.Error()}
			} else {
				s.HomeRelay = TaskState{Status: "ready"}
			}
		})
	}()

	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}
