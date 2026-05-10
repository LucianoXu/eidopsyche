package render

import (
	"io"
)

// NewTUI returns a Bubble Tea TUI renderer. Stub for milestone A —
// returns the CLI renderer until the real Model + bridge land in
// milestones B and C.
func NewTUI(in io.Reader, out io.Writer, cps int) Renderer {
	return NewCLI(in, out, cps)
}
