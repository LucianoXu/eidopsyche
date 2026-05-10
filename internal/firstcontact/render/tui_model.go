package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// modelDeps groups the bridge channels the Model talks to. Exposed so
// the bridge integration test can wire fakes.
type modelDeps struct {
	asks    <-chan askMsg
	replies chan<- replyMsg
}

// activeStatus tracks an in-flight Status() lifecycle (start → updates
// → stop). Multiple concurrent Statuses are not supported in this
// milestone (the wizard never opens two at once) — we keep just one.
type activeStatus struct {
	id      int
	message string
}

// model is the Bubble Tea Model that drives the wizard's TUI surface.
// It is a state machine over askMsg + tea.KeyMsg events; the active
// widget is a function of the current ask's kind.
type model struct {
	deps   modelDeps
	width  int
	height int

	// transcript accumulates already-rendered phase output. View()
	// concatenates transcript + active widget so past phases stay
	// visible (Style C: transcript, no alt-screen).
	transcript []string

	// activeAsk is non-nil while the worker is waiting for input.
	activeAsk *askMsg

	// Widget instances; only one is "active" at a time (driven by
	// activeAsk.kind).
	input      textinput.Model
	textarea   textarea.Model
	viewport   viewport.Model
	spinner    spinner.Model
	choiceIdx  int
	escPending bool // textarea: Esc-then-Enter submits

	// status is the in-flight Status() handle, or nil.
	status *activeStatus

	finalErr error
	done     bool
}

func newModel(d modelDeps) *model {
	ti := textinput.New()
	ti.Prompt = StylePromptChar.Render("› ")
	ti.CharLimit = 256
	ta := textarea.New()
	ta.Placeholder = ""
	ta.CharLimit = 4096
	ta.SetWidth(80)
	ta.SetHeight(5)
	vp := viewport.New(80, 12)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = StyleSpinner
	return &model{
		deps:     d,
		input:    ti,
		textarea: ta,
		viewport: vp,
		spinner:  sp,
	}
}

func (m *model) Init() tea.Cmd {
	return waitForAsk(m.deps.asks)
}

// waitForAsk is a tea.Cmd that blocks on the asks channel and surfaces
// the next askMsg as a tea.Msg. Update chains it after every reply so
// we get a continuous stream of asks.
func waitForAsk(asks <-chan askMsg) tea.Cmd {
	return func() tea.Msg {
		ask, ok := <-asks
		if !ok {
			return workerDoneMsg{}
		}
		return ask
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(max(20, msg.Width-4))
		m.viewport.Width = max(20, msg.Width-4)
		m.viewport.Height = max(6, msg.Height-8)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case startStatusMsg:
		m.status = &activeStatus{id: msg.id, message: msg.message}
		return m, m.spinner.Tick

	case updateStatusMsg:
		if m.status != nil && m.status.id == msg.id {
			m.status.message = msg.message
		}
		return m, nil

	case stopStatusMsg:
		if m.status != nil && m.status.id == msg.id {
			m.status = nil
		}
		return m, nil

	case workerDoneMsg:
		m.finalErr = msg.err
		m.done = true
		return m, tea.Quit

	case askMsg:
		return m.handleAsk(msg)

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.done = true
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}
	return m, nil
}

// handleAsk activates the right widget for the ask and (for
// fire-and-act ops) acks immediately.
func (m *model) handleAsk(msg askMsg) (tea.Model, tea.Cmd) {
	m.activeAsk = &msg
	switch msg.kind {
	case kindPrompt:
		m.input.SetValue("")
		m.input.Focus()
		return m, m.input.Cursor.BlinkCmd()

	case kindMultiline:
		m.textarea.SetValue("")
		m.textarea.Focus()
		m.escPending = false
		return m, m.textarea.Cursor.BlinkCmd()

	case kindPromptChoice:
		m.choiceIdx = 0
		return m, nil

	case kindShow, kindFrame:
		m.transcript = append(m.transcript, StyleBody.Render(m.wrap(msg.body)))
		m.activeAsk = nil
		m.deps.replies <- replyMsg{}
		return m, waitForAsk(m.deps.asks)

	case kindTypewriter:
		body := msg.body
		if msg.opts.HelpText == "markdown" {
			if rendered, err := renderMarkdown(body, m.width); err == nil {
				// glamour already word-wraps at the width we passed.
				m.transcript = append(m.transcript, rendered)
				m.activeAsk = nil
				m.deps.replies <- replyMsg{}
				return m, waitForAsk(m.deps.asks)
			}
		}
		m.transcript = append(m.transcript, StyleBody.Render(m.wrap(body)))
		m.activeAsk = nil
		m.deps.replies <- replyMsg{}
		return m, waitForAsk(m.deps.asks)

	case kindLogo:
		m.transcript = append(m.transcript, m.renderLogo())
		m.activeAsk = nil
		m.deps.replies <- replyMsg{}
		return m, waitForAsk(m.deps.asks)
	}
	// Unknown kind: ack and move on so the worker doesn't deadlock.
	m.activeAsk = nil
	m.deps.replies <- replyMsg{}
	return m, waitForAsk(m.deps.asks)
}

// handleKey dispatches a keystroke to the active widget.
func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.activeAsk == nil {
		return m, nil
	}
	switch m.activeAsk.kind {
	case kindPrompt:
		if msg.Type == tea.KeyEnter {
			text := strings.TrimSpace(m.input.Value())
			if text == "" && !m.activeAsk.opts.AllowEmpty {
				return m, nil // re-prompt; ignore Enter
			}
			m.activeAsk = nil
			m.input.Blur()
			m.transcript = append(m.transcript,
				"  "+StylePromptChar.Render("› ")+StylePastBody.Render(text))
			m.deps.replies <- replyMsg{text: text}
			return m, waitForAsk(m.deps.asks)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case kindMultiline:
		if msg.Type == tea.KeyEsc {
			m.escPending = true
			return m, nil
		}
		if msg.Type == tea.KeyEnter && m.escPending {
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" && !m.activeAsk.opts.AllowEmpty {
				m.escPending = false
				return m, nil
			}
			m.activeAsk = nil
			m.textarea.Blur()
			m.escPending = false
			m.transcript = append(m.transcript,
				StylePastBody.Render(indentBlock(text, "  ")))
			m.deps.replies <- replyMsg{text: text}
			return m, waitForAsk(m.deps.asks)
		}
		m.escPending = false
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case kindPromptChoice:
		switch msg.Type {
		case tea.KeyUp:
			if m.choiceIdx > 0 {
				m.choiceIdx--
			}
			return m, nil
		case tea.KeyDown:
			if m.choiceIdx < len(m.activeAsk.choices)-1 {
				m.choiceIdx++
			}
			return m, nil
		case tea.KeyEnter:
			idx := m.choiceIdx
			label := m.activeAsk.choices[idx].Label
			m.activeAsk = nil
			m.transcript = append(m.transcript,
				"  "+StylePromptChar.Render("› ")+StylePastBody.Render(label))
			m.deps.replies <- replyMsg{text: fmt.Sprintf("%d", idx)}
			return m, waitForAsk(m.deps.asks)
		}
	}
	return m, nil
}

func (m *model) View() string {
	var b strings.Builder
	b.WriteString(StyleAccent.Render("eidos summon"))
	b.WriteString("\n")
	ruleWidth := m.width
	if ruleWidth <= 0 {
		ruleWidth = 60
	}
	b.WriteString(SectionRule(ruleWidth))
	b.WriteString("\n")
	for _, line := range m.transcript {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.activeAsk != nil {
		switch m.activeAsk.kind {
		case kindPrompt:
			b.WriteString("\n")
			b.WriteString(StyleBody.Render(m.wrap(m.activeAsk.question)))
			b.WriteString("\n")
			if m.activeAsk.opts.HelpText != "" {
				b.WriteString(StyleHint.Render(m.wrap("  (" + m.activeAsk.opts.HelpText + ")")))
				b.WriteString("\n")
			}
			b.WriteString(m.input.View())

		case kindMultiline:
			b.WriteString("\n")
			b.WriteString(StyleBody.Render(m.wrap(m.activeAsk.question)))
			b.WriteString("\n")
			b.WriteString(StyleHint.Render("  (Press Esc then Enter to submit)"))
			b.WriteString("\n")
			b.WriteString(m.textarea.View())

		case kindPromptChoice:
			b.WriteString("\n")
			b.WriteString(StyleBody.Render(m.wrap(m.activeAsk.question)))
			b.WriteString("\n")
			for i, opt := range m.activeAsk.choices {
				focus := "  "
				label := opt.Label
				if i == m.choiceIdx {
					focus = StylePromptChar.Render("› ")
					label = StyleAccent.Render(opt.Label)
				}
				b.WriteString(focus + label)
				if opt.Hint != "" {
					b.WriteString("    " + StyleHint.Render("("+opt.Hint+")"))
				}
				b.WriteString("\n")
			}
		}
	}
	if m.status != nil {
		b.WriteString("\n")
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(StyleHint.Render(m.status.message))
	}
	return b.String()
}

// renderLogo produces a single-line stylized title:
//
//	─── E·I·D·O·P·S·Y·C·H·E ───
//
// Each letter is bolded in bronze; the dots are dim bronze; the
// flanking em-dashes pick up the muted palette of section rules.
// Style C is a transcript — the multi-line letter circle felt like
// a curtain raise that didn't fit.
func (m *model) renderLogo() string {
	letters := []string{"E", "I", "D", "O", "P", "S", "Y", "C", "H", "E"}
	mid := lipgloss.NewStyle().Foreground(bronzeDim).Render("·")
	parts := make([]string, 0, len(letters)*2-1)
	for i, l := range letters {
		parts = append(parts, StyleAccent.Render(l))
		if i < len(letters)-1 {
			parts = append(parts, mid)
		}
	}
	body := strings.Join(parts, " ")
	rule := StylePhaseRule.Render("───")
	return rule + "  " + body + "  " + rule
}

// wrap wraps text to roughly the current terminal width, leaving a
// small margin for the leading spaces View() prepends. CJK-aware:
// uses display width (each Han / Kana / Hangul rune counts as 2
// cells) rather than rune count, so a paragraph of Chinese narrative
// no longer overruns the right edge by 2x.
//
// When the terminal width hasn't been received yet (m.width == 0),
// defaults to 80 columns so output stays readable in pty harnesses.
func (m *model) wrap(text string) string {
	width := m.width - 2
	if width < 40 {
		width = 80
	}
	return wrapDisplayWidth(text, width)
}

// wrapCondition treats East Asian ambiguous-width characters (em-dash
// `—`, fullwidth punctuation, certain symbols) as 2 cells. Real
// terminals running in CJK locales render those glyphs at 2 cells, so
// using the narrower width would underestimate line length and let
// CJK narrative overrun the right edge — which is exactly the bug
// reported from a tmux smoke. The default runewidth.Condition's
// EastAsianWidth flag depends on LANG / LC_ALL at startup; we pin it
// to true so the wrap is correct regardless of the user's locale.
var wrapCondition = &runewidth.Condition{EastAsianWidth: true}

// wrapDisplayWidth wraps each paragraph to fit within `width` display
// columns. Prefers breaking at spaces (word boundaries for Latin
// text); falls back to a hard char-break when a single token's
// display width would overrun (covers CJK paragraphs that have no
// internal spaces and any English word longer than the line).
func wrapDisplayWidth(text string, width int) string {
	if width < 1 {
		width = 80
	}
	var out strings.Builder
	paragraphs := strings.Split(text, "\n")
	for pi, p := range paragraphs {
		if pi > 0 {
			out.WriteByte('\n')
		}
		runes := []rune(p)
		lineStart := 0
		col := 0
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			rw := wrapCondition.RuneWidth(r)
			if col+rw <= width {
				col += rw
				continue
			}
			// Need to break before runes[i]. Try to back up to the
			// nearest preceding space within the current line.
			breakAt := -1
			for j := i - 1; j > lineStart; j-- {
				if runes[j] == ' ' {
					breakAt = j
					break
				}
			}
			if breakAt > lineStart {
				out.WriteString(string(runes[lineStart:breakAt]))
				out.WriteByte('\n')
				lineStart = breakAt + 1 // skip the space
			} else {
				// No space to break at — hard char-break.
				out.WriteString(string(runes[lineStart:i]))
				out.WriteByte('\n')
				lineStart = i
			}
			// Recompute the new line's running width.
			col = 0
			for j := lineStart; j <= i; j++ {
				col += wrapCondition.RuneWidth(runes[j])
			}
		}
		out.WriteString(string(runes[lineStart:]))
	}
	return out.String()
}

func renderMarkdown(body string, width int) (string, error) {
	if width < 40 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width-4),
	)
	if err != nil {
		return "", err
	}
	return r.Render(body)
}

func indentBlock(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}
