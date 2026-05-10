package render_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

func TestCLI_PromptReadsLine(t *testing.T) {
	r := render.NewCLI(strings.NewReader("alice\n"), &bytes.Buffer{}, 0)
	got, err := r.Prompt("name?", render.PromptOpts{})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got != "alice" {
		t.Errorf("Prompt = %q, want %q", got, "alice")
	}
}

func TestCLI_PromptRejectsBlankUntilAnswer(t *testing.T) {
	r := render.NewCLI(strings.NewReader("\n\nalice\n"), &bytes.Buffer{}, 0)
	got, err := r.Prompt("name?", render.PromptOpts{})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got != "alice" {
		t.Errorf("Prompt = %q", got)
	}
}

func TestCLI_PromptAllowEmpty(t *testing.T) {
	r := render.NewCLI(strings.NewReader("\n"), &bytes.Buffer{}, 0)
	got, err := r.Prompt("hit enter to accept", render.PromptOpts{AllowEmpty: true})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got != "" {
		t.Errorf("Prompt = %q, want empty", got)
	}
}

func TestCLI_PromptMultiline(t *testing.T) {
	in := strings.NewReader("line one\nline two\n\n")
	r := render.NewCLI(in, &bytes.Buffer{}, 0)
	got, err := r.Prompt("describe", render.PromptOpts{Multiline: true})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Errorf("Prompt multiline = %q", got)
	}
}

func TestCLI_PromptChoice(t *testing.T) {
	r := render.NewCLI(strings.NewReader("2\n"), &bytes.Buffer{}, 0)
	idx, err := r.PromptChoice("?", []render.ChoiceOption{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	})
	if err != nil {
		t.Fatalf("PromptChoice: %v", err)
	}
	if idx != 1 {
		t.Errorf("PromptChoice = %d, want 1", idx)
	}
}

func TestCLI_PromptChoiceRetriesOnInvalid(t *testing.T) {
	r := render.NewCLI(strings.NewReader("99\nfoo\n2\n"), &bytes.Buffer{}, 0)
	idx, err := r.PromptChoice("?", []render.ChoiceOption{{Label: "a"}, {Label: "b"}})
	if err != nil {
		t.Fatalf("PromptChoice: %v", err)
	}
	if idx != 1 {
		t.Errorf("PromptChoice = %d, want 1", idx)
	}
}

func TestCLI_TypewriterFastPathsOnNonTTY(t *testing.T) {
	var out bytes.Buffer
	r := render.NewCLI(strings.NewReader(""), &out, 30)
	start := time.Now()
	r.Typewriter(context.Background(), "hello world")
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("non-TTY Typewriter took too long; should fast-path: %v", time.Since(start))
	}
	if !strings.Contains(out.String(), "hello world") {
		t.Errorf("output missing payload: %q", out.String())
	}
}

func TestCLI_NoTTYFallback(t *testing.T) {
	r := render.NewCLI(strings.NewReader(""), &bytes.Buffer{}, 30)
	if r.Capabilities().IsTTY {
		t.Errorf("expected IsTTY=false for *bytes.Buffer")
	}
}

func TestCLI_FrameClearsOnAnsi(t *testing.T) {
	// Non-ANSI buffer: just printlines.
	var out bytes.Buffer
	r := render.NewCLI(strings.NewReader(""), &out, 0)
	r.Frame("hello")
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("frame missing content: %q", out.String())
	}
}
