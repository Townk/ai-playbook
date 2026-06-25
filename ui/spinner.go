package ui

import (
	"github.com/mattn/go-runewidth"

	"charm.land/lipgloss/v2"
)

// spinnerFrames are the braille dots used by the "working…" indicator — the same
// frames the shell launcher used before the pager owned the spinner.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// spinnerLine renders one indicator row: "<frame> <label> <seconds>s", the frame
// and seconds in colBlue, the label in colSubtext. frame is taken modulo the
// frame count (tolerates negative/large values).
func spinnerLine(frame int, label string, seconds int) string {
	n := len(spinnerFrames)
	g := spinnerFrames[((frame%n)+n)%n]
	dot := lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue))
	lab := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
	return dot.Render(string(g)) + " " + lab.Render(label) + " " + dot.Render(itoa(seconds)+"s")
}

// activityGlyph is the spinning-arrow marker shown before a live agent tool-call
// summary on the activity line (distinct from the braille "Working…" frame).
const activityGlyph = "⟳"

// activityLineStr renders the agent's live tool-call summary row: "⟳ <summary>"
// in a dim subtext colour, truncated to width. Empty when summary is empty.
func activityLineStr(summary string, width int) string {
	if summary == "" {
		return ""
	}
	s := activityGlyph + " " + summary
	if width > 0 && runewidth.StringWidth(s) > width {
		s = runewidth.Truncate(s, width, "…")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render(s)
}
