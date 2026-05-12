package agentloop

import "sync"

// wakeMeta is the per-turn metadata that travels from the forwarder
// (which writes a user message to claude stdin) to the drainer (which
// stamps the resulting turn's transcript). Wake-driven turns carry the
// real wake.Signal.ID and wake.Reason; mailbox-driven turns get a
// synthesized id/reason at drain time when the queue is empty.
type wakeMeta struct {
	id         string
	reason     string
	firstWake  bool
	wakeDriven bool
}

// wakeQueue is a small mutex-protected FIFO of wakeMeta. The forwarder
// enqueues before writing to claude stdin; the drainer dequeues on the
// first non-system event of each new turn. If the queue is empty when
// the drainer opens a turn, the turn is treated as mailbox-driven.
type wakeQueue struct {
	mu sync.Mutex
	q  []wakeMeta
}

func newWakeQueue() *wakeQueue { return &wakeQueue{} }

func (w *wakeQueue) push(m wakeMeta) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.q = append(w.q, m)
}

// pop removes and returns the head element. ok=false when the queue
// is empty.
func (w *wakeQueue) pop() (wakeMeta, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.q) == 0 {
		return wakeMeta{}, false
	}
	head := w.q[0]
	w.q = w.q[1:]
	return head, true
}

// reset drops all queued entries. Called by the rotation goroutine
// between sessions so a new claude doesn't inherit pending wake-ids.
func (w *wakeQueue) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.q = nil
}
