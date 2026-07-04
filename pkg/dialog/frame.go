package dialog

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/pkg/dialog/theme"
)

const (
	frameHPad   = 2 // left/right padding inside the outer border
	frameBorder = 2 // the rounded outer border, left + right cells
)

// renderFrame composes the single outer-bordered modal: padding gap, the ▓▓▓
// title, the rule, an inset gap, the body sections (joined by inset gaps),
// another inset gap, then the hint — all inside one rounded border whose color
// follows the variant. body sections and hint are already-styled strings. width
// is the full pane width; the rule spans it exactly so the border width == width.
func renderFrame(t Theme, variant, title string, body []string, hint string, width, padding, inset int) string {
	innerW := width - frameBorder - 2*frameHPad
	if innerW < 1 {
		innerW = 1
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.titleColor(variant)))
	ruleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Rule))

	var rows []string
	if title != "" {
		rows = append(rows,
			titleStyle.Render("▓▓▓ "+title),
			ruleStyle.Render(strings.Repeat("━", innerW)),
		)
		rows = appendBlanks(rows, inset)
	}
	for i, sec := range body {
		if i > 0 {
			rows = appendBlanks(rows, inset)
		}
		rows = append(rows, sec)
	}
	rows = appendBlanks(rows, inset)
	rows = append(rows, hint)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.variantColor(variant))).
		Background(lipgloss.Color(theme.Mantle)).
		BorderBackground(lipgloss.Color(theme.Mantle)).
		// Top padding only: the hint sits directly above the bottom border (no
		// trailing blank line between the hint row and the closing ╰──╯).
		Padding(padding, frameHPad, 0, frameHPad).
		Render(strings.Join(rows, "\n"))
}

// AskInnerWidth returns the ask dialog's inner content width — the fixed
// float width minus the outer border and both horizontal paddings — so
// callers (e.g. the confirm gate's variable list) can wrap text to the exact
// dialog geometry without hard-coding it.
func AskInnerWidth() int {
	return FloatWidthDefault - frameBorder - 2*frameHPad
}

func appendBlanks(rows []string, n int) []string {
	for i := 0; i < n; i++ {
		rows = append(rows, "")
	}
	return rows
}
