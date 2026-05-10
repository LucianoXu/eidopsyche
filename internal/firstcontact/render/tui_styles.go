package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette — Style C (transcript, warm bronze accent on dark). Picked
// during the brainstorm visual companion. Hardcoded for now; theming
// is out of scope for this milestone.
var (
	bronze    = lipgloss.Color("#c8946b")
	bronzeDim = lipgloss.Color("#7a5a44")
	fg        = lipgloss.Color("#d4cdc1")
	fgDim     = lipgloss.Color("#5d6370")
	fgMuted   = lipgloss.Color("#3a3d44")
)

// Styled snippets the Model uses. Names express intent so View()
// reads as prose.
var (
	StyleAccent     = lipgloss.NewStyle().Foreground(bronze).Bold(true)
	StyleHint       = lipgloss.NewStyle().Foreground(fgDim)
	StylePromptChar = lipgloss.NewStyle().Foreground(bronze)
	StyleBody       = lipgloss.NewStyle().Foreground(fg)
	StylePastBody   = lipgloss.NewStyle().Foreground(fgDim)
	StylePhaseRule  = lipgloss.NewStyle().Foreground(fgMuted)
	StylePhaseLabel = lipgloss.NewStyle().Foreground(bronze).Bold(true)
	StyleSpinner    = lipgloss.NewStyle().Foreground(bronze)
)

// Reference bronzeDim so it's available for future styling without
// triggering "declared and not used" warnings on early builds.
var _ = bronzeDim

// SectionRule renders a horizontal separator at the given width.
func SectionRule(width int) string {
	if width < 1 {
		width = 40
	}
	return StylePhaseRule.Render(strings.Repeat("─", width))
}

// PhaseLabel formats "▎ phase 2 — the summoning book" with the bronze bar.
func PhaseLabel(text string) string {
	return StylePhaseLabel.Render("▎ ") + StyleAccent.Render(text)
}
