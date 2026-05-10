package render

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
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

// TestModel_WrapLongShowBody pins the long-line wrap fix: a Show body
// wider than the terminal must be word-wrapped before landing in the
// transcript, otherwise the right edge truncates on terminals without
// auto-wrap.
func TestModel_WrapLongShowBody(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	m.width = 50

	long := strings.Repeat("word ", 40)
	_, _ = m.Update(askMsg{kind: kindShow, body: long})

	if len(m.transcript) != 1 {
		t.Fatalf("transcript = %v entries", len(m.transcript))
	}
	got := m.transcript[0]
	if !strings.Contains(got, "\n") {
		t.Errorf("expected newlines from wrap; got single-line entry: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		// Drop ANSI escape sequences before counting length.
		visible := stripANSI(line)
		if len(visible) > m.width {
			t.Errorf("wrapped line exceeds width %d: %q (len=%d)", m.width, visible, len(visible))
		}
	}
}

// TestModel_WrapHandlesCJKDisplayWidth pins the second wrap fix:
// CJK characters take 2 terminal cells but rune-based wrap counts
// them as 1, so the visible line was twice the wrap width. Reproduces
// the tmux smoke output where Chinese narrative ran past the right
// edge despite m.width=80. After the fix, no wrapped line should
// exceed m.width by display-width measure.
func TestModel_WrapHandlesCJKDisplayWidth(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	m.width = 60

	cjk := strings.Repeat("他先于脚步声出现——圆框眼镜折回审讯室的白炽灯光，", 3)
	_, _ = m.Update(askMsg{kind: kindShow, body: cjk})

	if len(m.transcript) != 1 {
		t.Fatalf("transcript = %v entries", len(m.transcript))
	}
	got := m.transcript[0]
	for _, line := range strings.Split(got, "\n") {
		visible := stripANSI(line)
		w := displayWidth(visible)
		if w > m.width {
			t.Errorf("CJK line exceeds display width %d: %q (w=%d)", m.width, visible, w)
		}
	}
}

// displayWidth uses the same EastAsianWidth=true Condition the wrap
// itself uses, so the assertion sees the wrap's view of cell counts
// (em-dash `—` and other ambiguous-width glyphs as 2 cells).
func displayWidth(s string) int {
	return wrapCondition.StringWidth(s)
}

// TestModel_GlobalRuneWidthIsEastAsian pins the init() that flips the
// runewidth GLOBAL DefaultCondition. Without this, glamour (via
// muesli/reflow/wordwrap → runewidth.StringWidth) underestimates CJK
// line widths and the agent's markdown response overflows the right
// edge — reproduced from the user's tmux smoke output.
func TestModel_GlobalRuneWidthIsEastAsian(t *testing.T) {
	if !runewidth.DefaultCondition.EastAsianWidth {
		t.Errorf("runewidth.DefaultCondition.EastAsianWidth = false; init() should have set it true")
	}
	if w := runewidth.StringWidth("—"); w != 2 {
		t.Errorf("StringWidth(em-dash) = %d, want 2", w)
	}
}

// TestModel_MarkdownTypewriterWrapsCJK pins the response-render path
// also respects display width. Per-line check on the transcript:
// after glamour renders, no line should exceed m.width.
func TestModel_MarkdownTypewriterWrapsCJK(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})
	m.width = 60

	cjk := "> 致 Yingte——\n\n" +
		strings.Repeat("我听见了。久美子，号嘴贴上唇前那一秒的静默。", 3)
	_, _ = m.Update(askMsg{
		kind: kindTypewriter,
		body: cjk,
		opts: PromptOpts{HelpText: "markdown"},
	})

	if len(m.transcript) != 1 {
		t.Fatalf("transcript = %v entries", len(m.transcript))
	}
	for _, line := range strings.Split(m.transcript[0], "\n") {
		visible := stripANSI(line)
		w := displayWidth(visible)
		if w > m.width {
			t.Errorf("markdown line exceeds display width %d: %q (w=%d)", m.width, visible, w)
		}
	}
}

// TestModel_LogoIsSingleLine pins the design change: the logo is now
// a one-line stylized title rather than the multi-line letter circle.
func TestModel_LogoIsSingleLine(t *testing.T) {
	asks := make(chan askMsg, 1)
	replies := make(chan replyMsg, 1)
	m := newModel(modelDeps{asks: asks, replies: replies})

	logo := m.renderLogo()
	visible := stripANSI(logo)
	if strings.Contains(visible, "\n") {
		t.Errorf("logo should be single-line; got %q", visible)
	}
	for _, letter := range "EIDOPSYCHE" {
		if !strings.ContainsRune(visible, letter) {
			t.Errorf("logo missing letter %q in %q", letter, visible)
		}
	}
}

// stripANSI is a tiny helper for tests that need to compare visible
// width — strips bytes between ESC and 'm' (covers SGR styling).
func stripANSI(s string) string {
	var out []rune
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
