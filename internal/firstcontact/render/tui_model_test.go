package render

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModel_HandleAsk_Show drains a kindShow ask through Update and
// verifies the body lands in the transcript and a reply is sent.
func TestModel_HandleAsk_Show(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(askMsg{kind: kindShow, body: "hello world"})

	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0], "hello world") {
		t.Errorf("transcript = %v; want body in entry", m.transcript)
	}
	select {
	case r := <-replies:
		if r.err != nil {
			t.Errorf("reply.err = %v", r.err)
		}
	case <-time.After(time.Second):
		t.Errorf("no reply received")
	}
}

// TestModel_HandleAsk_Logo verifies the logo lands in the transcript.
func TestModel_HandleAsk_Logo(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(askMsg{kind: kindLogo})

	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0], "E") {
		t.Errorf("logo missing from transcript: %v", m.transcript)
	}
}

// TestModel_HandleAsk_PromptSubmitsOnEnter walks Prompt → typed text →
// Enter and verifies the reply text and transcript entry.
func TestModel_HandleAsk_PromptSubmitsOnEnter(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	// 1. Activate the prompt.
	_, _ = m.Update(askMsg{kind: kindPrompt, question: "name?", opts: PromptOpts{}})
	if m.activeAsk == nil || m.activeAsk.kind != kindPrompt {
		t.Fatalf("prompt not activated; activeAsk=%+v", m.activeAsk)
	}

	// 2. Type "alice" by feeding individual rune key msgs.
	for _, r := range "alice" {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// 3. Press Enter.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case r := <-replies:
		if r.text != "alice" {
			t.Errorf("reply.text = %q, want alice", r.text)
		}
	case <-time.After(time.Second):
		t.Fatalf("no reply received")
	}
	if m.activeAsk != nil {
		t.Errorf("activeAsk should be cleared after submit; got %+v", m.activeAsk)
	}
}

// TestModel_HandleAsk_PromptRejectsBlankWithoutAllowEmpty pins that
// pressing Enter on empty input does NOT reply when AllowEmpty is false.
func TestModel_HandleAsk_PromptRejectsBlankWithoutAllowEmpty(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(askMsg{kind: kindPrompt, question: "name?", opts: PromptOpts{AllowEmpty: false}})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case r := <-replies:
		t.Errorf("got unexpected reply on blank input: %+v", r)
	case <-time.After(50 * time.Millisecond):
		// expected: no reply
	}
	if m.activeAsk == nil {
		t.Errorf("activeAsk should still be set; got nil")
	}
}

// TestModel_HandleAsk_PromptAllowsEmptyWithFlag covers slug-confirm.
func TestModel_HandleAsk_PromptAllowsEmptyWithFlag(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(askMsg{kind: kindPrompt, opts: PromptOpts{AllowEmpty: true}})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case r := <-replies:
		if r.text != "" {
			t.Errorf("reply.text = %q, want empty", r.text)
		}
	case <-time.After(time.Second):
		t.Fatalf("no reply on AllowEmpty Enter")
	}
}

// TestModel_HandleAsk_PromptChoiceArrowKeys walks down twice + Enter
// to select the third option.
func TestModel_HandleAsk_PromptChoiceArrowKeys(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(askMsg{
		kind:    kindPromptChoice,
		choices: []ChoiceOption{{Label: "a"}, {Label: "b"}, {Label: "c"}},
	})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case r := <-replies:
		if r.text != "2" {
			t.Errorf("reply.text = %q, want 2", r.text)
		}
	case <-time.After(time.Second):
		t.Fatalf("no reply")
	}
}

// TestModel_StatusLifecycle verifies start → update → stop transitions
// the in-flight status correctly and the spinner gets a tick command.
func TestModel_StatusLifecycle(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, cmd := m.Update(startStatusMsg{id: 1, message: "thinking..."})
	if cmd == nil {
		t.Errorf("startStatus should return spinner.Tick cmd")
	}
	if m.status == nil || m.status.message != "thinking..." {
		t.Errorf("status not active after start: %+v", m.status)
	}

	_, _ = m.Update(updateStatusMsg{id: 1, message: "thinking... (5s)"})
	if m.status == nil || m.status.message != "thinking... (5s)" {
		t.Errorf("status not updated: %+v", m.status)
	}

	_, _ = m.Update(stopStatusMsg{id: 1})
	if m.status != nil {
		t.Errorf("status should be cleared after stop: %+v", m.status)
	}
}

// TestModel_StatusUpdateIgnoresWrongID guards against accidental cross-talk
// between concurrent Status() handles.
func TestModel_StatusUpdateIgnoresWrongID(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(startStatusMsg{id: 1, message: "first"})
	_, _ = m.Update(updateStatusMsg{id: 999, message: "stale"})

	if m.status.message != "first" {
		t.Errorf("update with wrong id mutated status: %+v", m.status)
	}
}

// TestModel_CtrlCQuits covers the cancellation contract: Ctrl-C marks
// done and returns tea.Quit.
func TestModel_CtrlCQuits(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Errorf("Ctrl-C should produce a tea.Cmd (Quit)")
	}
	if !m.done {
		t.Errorf("model.done should be true after Ctrl-C")
	}
}

// TestModel_WindowResizeReflows covers tea.WindowSizeMsg without crashing.
func TestModel_WindowResizeReflows(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.width != 100 || m.height != 30 {
		t.Errorf("window not resized: w=%d h=%d", m.width, m.height)
	}
	// textarea.Width() reports the inner content width (smaller than the
	// outer SetWidth value due to widget padding). Just verify it grew
	// from the default 80 in response to the resize, not the exact value.
	if m.textarea.Width() <= 80 {
		t.Errorf("textarea did not grow on resize; width = %d", m.textarea.Width())
	}
}

// TestModel_View_ContainsHeader_AndSectionRule pins the static header
// elements rendered in every frame.
func TestModel_View_ContainsHeader_AndSectionRule(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	m.width = 80

	out := m.View()
	if !strings.Contains(out, "eidos summon") {
		t.Errorf("view missing header: %q", out)
	}
	if !strings.Contains(out, "─") {
		t.Errorf("view missing section rule: %q", out)
	}
}
