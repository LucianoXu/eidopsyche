package render

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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
		cps:     cps,
	}
}

type tuiRenderer struct {
	prog    *tea.Program
	asks    chan askMsg
	replies chan replyMsg
	cps     int
	statusN atomic.Int64
}

func (r *tuiRenderer) Capabilities() Capabilities {
	return Capabilities{IsTTY: true, ANSI: true, Color: true}
}

func (r *tuiRenderer) Frame(content string) {
	r.asks <- askMsg{kind: kindFrame, body: content}
	<-r.replies
}

func (r *tuiRenderer) Show(text string) {
	r.asks <- askMsg{kind: kindShow, body: text}
	<-r.replies
}

func (r *tuiRenderer) Typewriter(_ context.Context, text string) {
	r.asks <- askMsg{kind: kindTypewriter, body: text}
	<-r.replies
}

func (r *tuiRenderer) Prompt(question string, opts PromptOpts) (string, error) {
	kind := kindPrompt
	if opts.Multiline {
		kind = kindMultiline
	}
	r.asks <- askMsg{kind: kind, question: question, opts: opts}
	reply, ok := <-r.replies
	if !ok {
		return "", fmt.Errorf("tui closed before reply")
	}
	return reply.text, reply.err
}

func (r *tuiRenderer) PromptChoice(q string, options []ChoiceOption) (int, error) {
	r.asks <- askMsg{kind: kindPromptChoice, question: q, choices: options}
	reply, ok := <-r.replies
	if !ok {
		return 0, fmt.Errorf("tui closed before reply")
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
	return &tuiStatusHandle{prog: r.prog, id: id}
}

func (r *tuiRenderer) Logo(_ context.Context, _ time.Duration) {
	r.asks <- askMsg{kind: kindLogo}
	<-r.replies
}

// RenderMarkdown is the MarkdownRenderer implementation: routes the
// body through glamour for blockquote / heading / emphasis styling.
func (r *tuiRenderer) RenderMarkdown(_ context.Context, body string) {
	r.asks <- askMsg{
		kind: kindTypewriter,
		body: body,
		opts: PromptOpts{HelpText: "markdown"},
	}
	<-r.replies
}

type tuiStatusHandle struct {
	prog *tea.Program
	id   int
	done atomic.Bool
}

func (h *tuiStatusHandle) Update(message string) {
	if h.done.Load() {
		return
	}
	h.prog.Send(updateStatusMsg{id: h.id, message: message})
}

func (h *tuiStatusHandle) Stop() {
	if h.done.Swap(true) {
		return
	}
	h.prog.Send(stopStatusMsg{id: h.id})
}

// RunWithPhases runs the Bubble Tea program on the calling goroutine
// and the phase function in a worker. Owns the bridge channel
// lifecycle: closes replies when the TUI exits so the worker doesn't
// deadlock if it's still waiting for input.
func (r *tuiRenderer) RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error {
	workerErr := make(chan error, 1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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

	if _, runErr := r.prog.Run(); runErr != nil {
		cancel()
		// Drain the worker so its purge runs.
		<-workerErr
		return fmt.Errorf("tui: %w", runErr)
	}
	cancel()
	close(r.replies)
	return <-workerErr
}
