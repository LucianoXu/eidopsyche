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
