package render

import (
	"io"
	"os"

	"golang.org/x/term"
)

// NewAuto returns a TUI Renderer when stdout is a real ≥60-col TTY
// and TUI is not explicitly disabled, otherwise the plain CLI
// fallback. Falls back to CLI on:
//   - EIDOS_NO_TUI=1 (explicit override)
//   - CI is set (any truthy value)
//   - TERM=dumb
//   - stdout is not an *os.File
//   - stdout is not a TTY (pipe, file redirect, etc.)
//   - terminal width below 60 columns (Bubble Tea widgets truncate badly)
//
// The CLI fallback is the existing renderer; existing integration
// tests and piped-stdin runs are unaffected.
func NewAuto(in io.Reader, out io.Writer, cps int) Renderer {
	if shouldUseTUI(out) {
		return NewTUI(in, out, cps)
	}
	return NewCLI(in, out, cps)
}

func shouldUseTUI(out io.Writer) bool {
	if os.Getenv("EIDOS_NO_TUI") == "1" {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	if !term.IsTerminal(int(f.Fd())) {
		return false
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil || cols < 60 {
		return false
	}
	return true
}
