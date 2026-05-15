// Package render carries the wizard's renderer interface and the CLI
// implementation. The interface exists so a future WebUI renderer can
// plug into the same Run() loop without forking the phase logic.
package render

import (
	"context"
	"time"
)

// Capabilities describes what flourishes the current renderer supports.
// The CLI implementation derives this once at construction time from
// the underlying file descriptor.
type Capabilities struct {
	IsTTY bool
	ANSI  bool // cursor positioning, alt-screen, etc.
	Color bool
}

// PromptOpts controls Prompt() shape.
type PromptOpts struct {
	// Multiline accepts input until a blank line followed by Enter.
	Multiline bool
	// AllowEmpty lets the prompt return an empty string instead of
	// re-prompting. Used by phase 2 step 4 where empty-input means
	// "accept the derived slug".
	AllowEmpty bool
	// HelpText is an optional one-line hint shown under the prompt.
	HelpText string
}

// ChoiceOption is one entry in a PromptChoice() list.
type ChoiceOption struct {
	Label string
	Hint  string // shown dimly under the focused option
}

// StatusHandle is returned by Status() to update or stop the live line.
type StatusHandle interface {
	Update(message string)
	Stop()
}

// Renderer is the surface the wizard talks to.
type Renderer interface {
	Capabilities() Capabilities
	Frame(content string)
	Show(text string)
	Typewriter(ctx context.Context, text string)
	Prompt(question string, opts PromptOpts) (string, error)
	PromptChoice(question string, options []ChoiceOption) (int, error)
	Status(message string) StatusHandle
	Logo(ctx context.Context, d time.Duration)

	// EditMultiline shows the operator a multiline editor pre-filled
	// with default_. The operator can accept as-is or edit before
	// submitting. Returns the (possibly empty) edited text, or io.EOF
	// if the operator cancels.
	//
	// Submit gesture and cancel gesture are implementation-specific
	// (typically Ctrl+D submit / Esc cancel for the TUI). Phase 3.5
	// calling-words is the only caller today; an empty submission is
	// a valid result.
	EditMultiline(prompt, default_ string) (string, error)

	// WithRawTerminal hands the real /dev/tty back to fn so it can run
	// a child process that needs raw stdin/stdout (e.g. `claude
	// setup-token`'s device-authorization flow). For the TUI renderer
	// this releases Bubble Tea's hold on the terminal for the duration
	// of fn and re-acquires it afterwards; for the plain CLI renderer
	// it is a passthrough since nothing owns the terminal already.
	//
	// fn's error is returned verbatim. Errors from release / restore
	// are wrapped so the caller can distinguish "the child failed"
	// from "I could not give the child the terminal."
	WithRawTerminal(fn func() error) error
}

// TUIRenderer is implemented by renderers whose lifecycle requires
// owning the main goroutine (Bubble Tea). Surfaces detect this via
// type-assertion and call RunWithPhases instead of running the phase
// driver directly. The plain Renderer interface (Prompt, Show, etc.)
// is unchanged so phase logic doesn't need to know about the
// distinction.
type TUIRenderer interface {
	Renderer
	RunWithPhases(ctx context.Context, fn func(context.Context, Renderer) error) error
}

// MarkdownRenderer is implemented by renderers that can render
// markdown bodies (e.g. the TUI via glamour). Surfaces opt in via
// type-assertion; the CLI does not implement this and falls back to
// plain Typewriter.
type MarkdownRenderer interface {
	RenderMarkdown(ctx context.Context, body string)
}
