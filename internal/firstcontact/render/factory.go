package render

import (
	"io"
	"os"

	"golang.org/x/term"
)

// NewAuto returns a TUI Renderer when BOTH stdin and stdout are real
// ≥60-col TTYs and TUI is not explicitly disabled, otherwise the plain
// CLI fallback. Falls back to CLI on:
//   - EIDOS_NO_TUI=1 (explicit override)
//   - CI is set (any truthy value)
//   - TERM=dumb
//   - stdin or stdout is not an *os.File
//   - stdin or stdout is not a TTY (pipe, file redirect, etc.)
//   - terminal width below 60 columns (Bubble Tea widgets truncate badly)
//
// Stdin matters because the TUI relies on reading raw keystrokes; a
// piped-stdin invocation like `printf '...' | eidos summon` must take
// the CLI path even when stdout points at a real terminal.
func NewAuto(in io.Reader, out io.Writer, cps int) Renderer {
	if shouldUseTUI(in, out) {
		return NewTUI(in, out, cps)
	}
	return NewCLI(in, out, cps)
}

func shouldUseTUI(in io.Reader, out io.Writer) bool {
	if os.Getenv("EIDOS_NO_TUI") == "1" {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if !isFileTTY(in) {
		return false
	}
	outF, ok := out.(*os.File)
	if !ok {
		return false
	}
	if !term.IsTerminal(int(outF.Fd())) {
		return false
	}
	cols, _, err := term.GetSize(int(outF.Fd()))
	if err != nil || cols < 60 {
		return false
	}
	return true
}

func isFileTTY(rw any) bool {
	f, ok := rw.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
