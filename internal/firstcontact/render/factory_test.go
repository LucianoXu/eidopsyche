package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

func TestNewAuto_FallsBackOnPipe(t *testing.T) {
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if r.Capabilities().IsTTY {
		t.Errorf("expected CLI fallback for *bytes.Buffer; got TTY-capable renderer")
	}
}

func TestNewAuto_RespectsEIDOS_NO_TUI(t *testing.T) {
	t.Setenv("EIDOS_NO_TUI", "1")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("EIDOS_NO_TUI=1 should force CLI fallback")
	}
}

func TestNewAuto_RespectsCIEnv(t *testing.T) {
	t.Setenv("CI", "true")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("CI=true should force CLI fallback")
	}
}

func TestNewAuto_RespectsDumbTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	r := render.NewAuto(strings.NewReader(""), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("TERM=dumb should force CLI fallback")
	}
}

// TestNewAuto_FallsBackOnPipedStdin pins the codex review fix:
// `printf '...' | eidos summon` from an interactive terminal has a
// real TTY stdout but a pipe stdin. The TUI cannot read keystrokes
// from a pipe, so the factory must take the CLI path.
func TestNewAuto_FallsBackOnPipedStdin(t *testing.T) {
	// strings.NewReader is not an *os.File, so isFileTTY returns false —
	// matches the piped-stdin shape regardless of stdout's TTY status.
	r := render.NewAuto(strings.NewReader("scripted input"), &bytes.Buffer{}, 30)
	if _, ok := r.(render.TUIRenderer); ok {
		t.Errorf("non-TTY stdin should force CLI fallback")
	}
}
