package render

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrTUIClosed is returned by renderer methods when the TUI exited
// (Ctrl-C, panic, or tea.Program.Run error) before the worker's
// ask could be answered. Phase logic propagates the error and lets
// RunWithPhases drain the worker.
var ErrTUIClosed = errors.New("tui closed before reply")

// NewTUI returns a Bubble Tea TUI renderer. The phase logic talks to
// it through the Renderer interface; the program's lifecycle is
// owned by RunWithPhases (see TUIRenderer).
func NewTUI(in io.Reader, out io.Writer, cps int) Renderer {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	prog := tea.NewProgram(m,
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithoutCatchPanics(),
	)
	return &tuiRenderer{
		prog:    prog,
		asks:    asks,
		replies: replies,
		done:    make(chan struct{}),
		cps:     cps,
	}
}

// tuiRenderer's bridge channel pair (asks / replies) is one
// ask-then-reply per call. The done channel is closed by RunWithPhases
// when the TUI exits — every renderer method selects on done so a TUI
// shutdown unblocks the worker (rather than leaking a goroutine
// blocked forever on a send/recv to a stalled program).
type tuiRenderer struct {
	prog     *tea.Program
	asks     chan askMsg
	replies  chan replyMsg
	done     chan struct{}
	doneOnce sync.Once
	cps      int
	statusN  atomic.Int64
}

// closeDone is idempotent: RunWithPhases may call it from multiple
// error branches.
func (r *tuiRenderer) closeDone() {
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *tuiRenderer) Capabilities() Capabilities {
	return Capabilities{IsTTY: true, ANSI: true, Color: true}
}

// sendAsk sends an askMsg, observing r.done. Returns ErrTUIClosed
// if the TUI exited before the ask could be delivered.
func (r *tuiRenderer) sendAsk(msg askMsg) error {
	select {
	case r.asks <- msg:
		return nil
	case <-r.done:
		return ErrTUIClosed
	}
}

// awaitReply waits for the next replyMsg from the Model, observing
// r.done. Returns ErrTUIClosed if the TUI exited before replying.
func (r *tuiRenderer) awaitReply() (replyMsg, error) {
	select {
	case reply, ok := <-r.replies:
		if !ok {
			return replyMsg{}, ErrTUIClosed
		}
		return reply, nil
	case <-r.done:
		return replyMsg{}, ErrTUIClosed
	}
}

// fireAndAck is the synchronous-from-the-worker's-POV path used by
// Show / Frame / Typewriter / Logo / RenderMarkdown. We log the
// error rather than propagating because these methods don't return
// one — but stopping early on shutdown is the important contract.
func (r *tuiRenderer) fireAndAck(msg askMsg) {
	if err := r.sendAsk(msg); err != nil {
		return
	}
	_, _ = r.awaitReply()
}

func (r *tuiRenderer) Frame(content string) {
	r.fireAndAck(askMsg{kind: kindFrame, body: content})
}

func (r *tuiRenderer) Show(text string) {
	r.fireAndAck(askMsg{kind: kindShow, body: text})
}

func (r *tuiRenderer) Typewriter(_ context.Context, text string) {
	r.fireAndAck(askMsg{kind: kindTypewriter, body: text})
}

func (r *tuiRenderer) Prompt(question string, opts PromptOpts) (string, error) {
	kind := kindPrompt
	if opts.Multiline {
		kind = kindMultiline
	}
	if err := r.sendAsk(askMsg{kind: kind, question: question, opts: opts}); err != nil {
		return "", err
	}
	reply, err := r.awaitReply()
	if err != nil {
		return "", err
	}
	return reply.text, reply.err
}

func (r *tuiRenderer) PromptChoice(q string, options []ChoiceOption) (int, error) {
	if err := r.sendAsk(askMsg{kind: kindPromptChoice, question: q, choices: options}); err != nil {
		return 0, err
	}
	reply, err := r.awaitReply()
	if err != nil {
		return 0, err
	}
	if reply.err != nil {
		return 0, reply.err
	}
	var i int
	if _, err := fmt.Sscanf(reply.text, "%d", &i); err != nil {
		return 0, fmt.Errorf("parse choice index %q: %w", reply.text, err)
	}
	return i, nil
}

func (r *tuiRenderer) Status(message string) StatusHandle {
	id := int(r.statusN.Add(1))
	r.prog.Send(startStatusMsg{id: id, message: message})
	return &tuiStatusHandle{prog: r.prog, id: id, done: r.done}
}

func (r *tuiRenderer) Logo(_ context.Context, _ time.Duration) {
	r.fireAndAck(askMsg{kind: kindLogo})
}

// EditMultiline (stub). Task 2 replaces this with a bubbles/textarea
// implementation. This intermediate stub returns the default verbatim
// so the type satisfies the Renderer interface and the build remains
// green between Task 1 and Task 2.
func (r *tuiRenderer) EditMultiline(_ string, defaultText string) (string, error) {
	return defaultText, nil
}

// RenderMarkdown is the MarkdownRenderer implementation: routes the
// body through glamour for blockquote / heading / emphasis styling.
func (r *tuiRenderer) RenderMarkdown(_ context.Context, body string) {
	r.fireAndAck(askMsg{
		kind: kindTypewriter,
		body: body,
		opts: PromptOpts{HelpText: "markdown"},
	})
}

type tuiStatusHandle struct {
	prog    *tea.Program
	id      int
	stopped atomic.Bool
	done    <-chan struct{} // closed by RunWithPhases on TUI exit
}

func (h *tuiStatusHandle) Update(message string) {
	if h.stopped.Load() {
		return
	}
	select {
	case <-h.done:
		return
	default:
	}
	h.prog.Send(updateStatusMsg{id: h.id, message: message})
}

func (h *tuiStatusHandle) Stop() {
	if h.stopped.Swap(true) {
		return
	}
	select {
	case <-h.done:
		return
	default:
	}
	h.prog.Send(stopStatusMsg{id: h.id})
}

// RunWithPhases runs the Bubble Tea program on the calling goroutine
// and the phase function in a worker. Owns the bridge channel
// lifecycle:
//
//   - closes r.done on EVERY exit path (Run() error, normal exit,
//     panic) so renderer methods blocked in sendAsk / awaitReply
//     unblock with ErrTUIClosed instead of leaking
//   - cancels the worker's context so phase logic that's waiting on
//     I/O (claude calls, response polls) returns ctx.Err()
//   - drains workerErr so phase 3's deferred purge gets to run before
//     RunWithPhases returns
func (r *tuiRenderer) RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error {
	workerErr := make(chan error, 1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer r.closeDone()

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				workerErr <- fmt.Errorf("phase logic panicked: %v", rec)
				r.prog.Send(workerDoneMsg{err: fmt.Errorf("panic: %v", rec)})
				return
			}
		}()
		err := fn(workerCtx, r)
		workerErr <- err
		r.prog.Send(workerDoneMsg{err: err})
	}()

	_, runErr := r.prog.Run()
	cancel()
	r.closeDone() // unblock any renderer call mid-flight in the worker

	werr := <-workerErr // drain so worker's deferred cleanup ran
	if runErr != nil {
		return fmt.Errorf("tui: %w", runErr)
	}
	return werr
}
