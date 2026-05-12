// Package agentloop's stream-json stdout drainer.
//
// The drainer is a goroutine that reads stream-json events from claude's
// stdout, parses each line into a transcript.Event, drives the
// StateMachine, and writes agent-state.json snapshots when configured.
// Per-turn transcript file rotation is layered on top in Stage 5.1;
// this skeleton exposes hook points (OnTurnStart, OnTurnEnd, Store,
// NextTurnID, NextTurnReason) that Stage 5.1 fills in.
package agentloop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// DrainerConfig wires the drainer to its collaborators.
type DrainerConfig struct {
	TranscriptsDir string
	StateMachine   *StateMachine
	Clock          func() time.Time

	// OnTurnStart and OnTurnEnd are optional hooks for the rotation
	// goroutine to track when claude becomes idle. nil → no-op.
	// OnTurnEnd receives a wakeDriven bool: true when the turn was
	// triggered by a real wake signal, false for mailbox-driven turns.
	OnTurnStart func()
	OnTurnEnd   func(wakeDriven bool)

	// AgentStatePath, if non-empty, causes the drainer to write
	// agent-state.json snapshots on every state transition + on a 5s
	// floor heartbeat. Empty path disables.
	AgentStatePath string

	// OutstandingWakes / ResultsSeen are read by the drainer to populate
	// the corresponding agent-state.json fields. Owned by the forward /
	// rotation goroutines; the drainer only reads (and increments
	// ResultsSeen on each `result` event).
	OutstandingWakes *atomic.Int64
	ResultsSeen      *atomic.Int64

	// SessionID is the current claude session UUID; used in agent-state
	// snapshots and transcript index entries. Updated by rotation when a
	// new session starts.
	SessionID func() string

	// SessionStartedAt mirrors sessionstate.SessionStartedAt for the
	// current session. Updated by rotation.
	SessionStartedAt func() int64

	// WakesInSession returns the in-session wake counter (read from
	// sessionstate or maintained in memory by the forward goroutine).
	WakesInSession func() int

	// Dreaming returns the current dreaming-flag value, included in the
	// agent-state.json snapshot for observability.
	Dreaming func() bool

	// Store, when non-nil, enables per-turn transcript file writes.
	// The drainer opens a fresh wake-<id>.ndjson on the first non-system
	// event after each `result` and finalizes it on the next `result`.
	Store *transcript.Store

	// NextTurn is called by the drainer at turn-open time to assign the
	// transcript filename and index Reason. Wake-driven turns return the
	// real wake.Signal.ID and wake.Reason; mailbox-driven turns return a
	// synthesized "mailbox-<unix_nano>" id with reason "mailbox". The
	// wakeDriven field controls whether OnTurnEnd counts the turn toward
	// the session wake counter.
	NextTurn func() wakeMeta

	// TranscriptMaxCount returns the per-mindform max transcript count
	// override (config.MindForm.TranscriptsMaxCount), or 0 to use
	// transcript.DefaultMaxCount.
	TranscriptMaxCount func() int

	// TranscriptMaxBytes returns the per-mindform max transcript bytes
	// override (config.MindForm.TranscriptsMaxBytes parsed), or 0 to use
	// transcript.DefaultMaxBytes.
	TranscriptMaxBytes func() int64
}

// transcriptHandle holds the open file and metadata for the current turn.
type transcriptHandle struct {
	id         string
	reason     string
	wakeDriven bool
	file       *os.File
}

// Drainer reads stream-json from src, parses each line into a
// transcript.Event, drives the state machine, writes agent-state
// snapshots, and (when Store is configured) rotates per-turn
// transcript files at `result` boundaries.
type Drainer struct {
	cfg       DrainerConfig
	curHandle *transcriptHandle
	turnStart int64
	counter   *transcript.Counter
}

// NewDrainer constructs a drainer; call Run with the reader.
func NewDrainer(cfg DrainerConfig) *Drainer { return &Drainer{cfg: cfg} }

// Run drains src until EOF or ctx cancellation. Returns the underlying
// IO error, or nil on clean EOF.
func (d *Drainer) Run(ctx context.Context, src io.Reader) error {
	// Close any in-flight transcript FD on shutdown (ctx-cancel, EOF, IO
	// error). The index entry is intentionally NOT written here — consistent
	// with "no finalize on crash; let Recover handle it" policy.
	defer func() {
		if d.curHandle != nil && d.curHandle.file != nil {
			_ = d.curHandle.file.Close()
			d.curHandle = nil
		}
	}()

	r := bufio.NewReaderSize(src, 1<<20)
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	type lineOrErr struct {
		line []byte
		err  error
	}
	lines := make(chan lineOrErr, 1)
	go func() {
		for {
			line, err := readLineUnbounded(r)
			select {
			case lines <- lineOrErr{line: line, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			d.writeSnapshot()
		case le := <-lines:
			if len(le.line) > 0 {
				d.handleLine(le.line)
			}
			if le.err == io.EOF {
				return nil
			}
			if le.err != nil {
				return le.err
			}
		}
	}
}

func (d *Drainer) handleLine(line []byte) {
	ev, err := transcript.ParseEvent(line)
	if err != nil {
		fmt.Fprintf(stderr(), "agent-loop: drain: parse: %v\n", err)
		return
	}
	now := d.cfg.Clock()

	// Open a new per-turn transcript on the first assistant or user event
	// arriving while no handle is open.
	if d.curHandle == nil && d.cfg.Store != nil && d.cfg.NextTurn != nil &&
		(ev.Type == transcript.TypeAssistant || ev.Type == transcript.TypeUser) {
		meta := d.cfg.NextTurn()
		f, openErr := d.cfg.Store.Open(meta.id)
		if openErr != nil {
			fmt.Fprintf(stderr(), "agent-loop: drain: open transcript: %v\n", openErr)
		} else {
			d.curHandle = &transcriptHandle{
				id:         meta.id,
				reason:     meta.reason,
				wakeDriven: meta.wakeDriven,
				file:       f,
			}
			d.turnStart = now.Unix()
			d.counter = &transcript.Counter{}
		}
	}

	if d.curHandle != nil && d.curHandle.file != nil {
		_, _ = d.curHandle.file.Write(line)
	}
	if d.counter != nil {
		d.counter.Observe(ev)
	}

	prevBusy := d.cfg.StateMachine.Snapshot().ClaudeBusy
	d.cfg.StateMachine.Observe(ev, now)
	curBusy := d.cfg.StateMachine.Snapshot().ClaudeBusy

	if !prevBusy && curBusy && d.cfg.OnTurnStart != nil {
		d.cfg.OnTurnStart()
	}
	if prevBusy && !curBusy {
		if d.cfg.ResultsSeen != nil {
			d.cfg.ResultsSeen.Add(1)
		}
		wakeDriven := d.curHandle != nil && d.curHandle.wakeDriven
		d.finalizeCurrentTurn(now.Unix())
		if d.cfg.OnTurnEnd != nil {
			d.cfg.OnTurnEnd(wakeDriven)
		}
	}
	d.writeSnapshot()
}

// finalizeCurrentTurn closes the active transcript file and writes its
// index entry. No-op when no handle is open or Store is unconfigured.
func (d *Drainer) finalizeCurrentTurn(endedAt int64) {
	if d.curHandle == nil || d.cfg.Store == nil {
		return
	}
	entry := transcript.Entry{
		ID:        d.curHandle.id,
		Reason:    d.curHandle.reason,
		StartedAt: d.turnStart,
		EndedAt:   endedAt,
		OK:        true, // default; overridden below if a result event carried is_error
	}
	// Fix 3: stamp SessionID so historical rows surface session boundaries.
	if d.cfg.SessionID != nil {
		entry.SessionID = d.cfg.SessionID()
	}
	if d.counter != nil {
		entry.ToolUseCount = d.counter.ToolUseCount
		entry.ThinkingBlocks = d.counter.ThinkingBlocks
		if d.counter.Result != nil {
			entry.OK = d.counter.Result.OK
			if d.counter.Result.TotalCostUSD != nil {
				cost := *d.counter.Result.TotalCostUSD
				entry.CostUSD = &cost
			}
		}
	}
	// Fix 4: honor per-mindform transcript limits from config.
	maxCount := transcript.DefaultMaxCount
	maxBytes := transcript.DefaultMaxBytes
	if d.cfg.TranscriptMaxCount != nil {
		if n := d.cfg.TranscriptMaxCount(); n > 0 {
			maxCount = n
		}
	}
	if d.cfg.TranscriptMaxBytes != nil {
		if b := d.cfg.TranscriptMaxBytes(); b > 0 {
			maxBytes = b
		}
	}
	_ = d.curHandle.file.Close()
	if err := d.cfg.Store.Finalize(entry, maxCount, maxBytes); err != nil {
		fmt.Fprintf(stderr(), "agent-loop: drain: finalize: %v\n", err)
	}
	d.curHandle = nil
	d.counter = nil
}

// writeSnapshot serializes the current state to agent-state.json if
// configured. Errors are logged but not propagated; the live snapshot
// is best-effort observability.
func (d *Drainer) writeSnapshot() {
	if d.cfg.AgentStatePath == "" {
		return
	}
	snap := d.cfg.StateMachine.Snapshot()
	st := AgentState{
		ClaudeBusy:    snap.ClaudeBusy,
		SinceUnix:     snap.SinceUnix,
		LastEventType: snap.LastEventType,
		LastEventAt:   snap.LastEventAt,
	}
	if d.cfg.SessionID != nil {
		st.SessionID = d.cfg.SessionID()
	}
	if d.cfg.SessionStartedAt != nil {
		st.SessionStartedAt = d.cfg.SessionStartedAt()
	}
	if d.cfg.WakesInSession != nil {
		st.WakesInSession = d.cfg.WakesInSession()
	}
	if d.cfg.OutstandingWakes != nil {
		st.OutstandingWakesSent = int(d.cfg.OutstandingWakes.Load())
	}
	if d.cfg.ResultsSeen != nil {
		st.ResultsSeen = int(d.cfg.ResultsSeen.Load())
	}
	if d.cfg.Dreaming != nil {
		st.Dreaming = d.cfg.Dreaming()
	}
	if err := WriteAgentState(d.cfg.AgentStatePath, st); err != nil {
		fmt.Fprintf(stderr(), "agent-loop: write agent-state: %v\n", err)
	}
}

// readLineUnbounded reads up to and including the next '\n' from r,
// stitching together as many ReadSlice chunks as needed. A trailing
// partial line at EOF is returned with (line, io.EOF). Copy of the
// helper used by the legacy agent-runner.
func readLineUnbounded(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}
