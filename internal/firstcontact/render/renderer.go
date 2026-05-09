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
}
