package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// diffModal builds the bordered side-by-side diff box (content-capped,
// scrollable via m.diffYOff / m.diffXOff). Mirroring the help modal, it is NOT
// placed: viewString composites it centered over the live document so the
// playbook keeps rendering behind the overlay while the diff is shown.
func (m model) diffModal() string {
	if len(m.diffLines) == 0 {
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBlue)).
			BorderBackground(lipgloss.Color(colMantle)).
			Background(lipgloss.Color(colMantle)).
			Padding(0, 1).
			Render("")
	}

	// Visible row count: height-8 (border×2 + top/bottom pad×2 + margin×2).
	// Content-capped so small diffs don't leave a gap-filled box.
	visH := m.height - 8
	if visH < 1 {
		visH = 1
	}
	if visH > len(m.diffLines) {
		visH = len(m.diffLines)
	}

	// Widest rendered line (ANSI-aware).
	maxW := 0
	for _, l := range m.diffLines {
		if w := lipgloss.Width(l); w > maxW {
			maxW = w
		}
	}
	// Text column budget: width-8 (4-col margin on each side including
	// border+padding), content-capped so the box stays proportionate.
	visW := m.width - 8
	if visW > maxW {
		visW = maxW
	}
	if visW < 1 {
		visW = 1
	}

	// Vertical window: slice [diffYOff, diffYOff+visH).
	start := m.diffYOff
	if start < 0 {
		start = 0
	}
	end := start + visH
	if end > len(m.diffLines) {
		end = len(m.diffLines)
	}
	if start > end {
		start = end
	}

	// Build windowed rows with horizontal scroll via hslice.
	rows := make([]string, 0, end-start)
	for _, l := range m.diffLines[start:end] {
		rows = append(rows, hslice(l, m.diffXOff, visW))
	}
	content := strings.Join(rows, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colBlue)).
		BorderBackground(lipgloss.Color(colMantle)).
		Background(lipgloss.Color(colMantle)).
		Padding(0, 1).
		Render(content)
}

// clampDiffScroll ensures diffYOff and diffXOff stay within the valid scroll
// range for the current terminal dimensions and diff content. Mirrors
// clampHelpScroll.
func (m *model) clampDiffScroll() {
	if len(m.diffLines) == 0 {
		m.diffYOff = 0
		m.diffXOff = 0
		return
	}
	visH := m.height - 8
	if visH < 1 {
		visH = 1
	}
	maxY := len(m.diffLines) - visH
	if maxY < 0 {
		maxY = 0
	}
	if m.diffYOff > maxY {
		m.diffYOff = maxY
	}
	if m.diffYOff < 0 {
		m.diffYOff = 0
	}

	maxW := 0
	for _, l := range m.diffLines {
		if w := lipgloss.Width(l); w > maxW {
			maxW = w
		}
	}
	visW := m.width - 8
	if visW < 1 {
		visW = 1
	}
	maxX := maxW - visW
	if maxX < 0 {
		maxX = 0
	}
	if m.diffXOff > maxX {
		m.diffXOff = maxX
	}
	if m.diffXOff < 0 {
		m.diffXOff = 0
	}
}

// diffHalf returns half the visible diff text height (≥1) for Ctrl+D/Ctrl+U.
func diffHalf(m model) int {
	if h := (m.height - 8) / 2; h > 1 {
		return h
	}
	return 1
}

// diffPage returns the full visible diff text height (≥1) for Ctrl+F/Ctrl+B.
func diffPage(m model) int {
	if h := m.height - 8; h > 1 {
		return h
	}
	return 1
}

// diffHalfW returns half the visible diff text width (≥1) for L/H scroll.
func diffHalfW(m model) int {
	if w := (m.width - 8) / 2; w > 1 {
		return w
	}
	return 1
}
