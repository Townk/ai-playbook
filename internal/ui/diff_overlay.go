package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	idiff "github.com/Townk/ai-playbook/internal/diff"
)

// diffContentWidth is the visible text width of the diff dialog: the box is m.width-6
// (3 blank columns each side when centered), less the border (2) and padding (2). The
// diff content is rendered at this width so it fills the window without needing scroll.
func diffContentWidth(m model) int {
	if w := m.width - 10; w > 1 {
		return w
	}
	return 1
}

// diffGutterW is the fixed line-number gutter width: the digit count of the
// largest old/new line number across all rows (always ≥1).
func diffGutterW(rows []idiff.Row) int {
	maxNo := 0
	for _, r := range rows {
		if r.LeftNo > maxNo {
			maxNo = r.LeftNo
		}
		if r.RightNo > maxNo {
			maxNo = r.RightNo
		}
	}
	return len(strconv.Itoa(maxNo))
}

// diffTextCol is the per-pane text column width for a given content width and
// gutter width: (visW − divider(3) − 2×(gutter + its separating space)) / 2,
// clamped ≥1. Matches RenderRow's layout so the two panes fill visW.
func diffTextCol(visW, gutterW int) int {
	c := (visW - 3 - 2*(gutterW+1)) / 2
	if c < 1 {
		c = 1
	}
	return c
}

// diffPaneMax returns each pane's maximum horizontal scroll offset:
// max over rows of (displayWidth(text) − textCol), clamped ≥0. The left and
// right panes clamp independently so the short side stops scrolling while the
// long side keeps revealing its tail under one shared m.diffXOff.
func diffPaneMax(rows []idiff.Row, textCol int) (leftMax, rightMax int) {
	for _, r := range rows {
		if d := lipgloss.Width(r.Left) - textCol; d > leftMax {
			leftMax = d
		}
		if d := lipgloss.Width(r.Right) - textCol; d > rightMax {
			rightMax = d
		}
	}
	return leftMax, rightMax
}

// diffNarrow reports whether the dialog is too narrow for the two-column gutter
// layout, so the overlay falls back to a unified (single-column) render. This is
// a cheap width comparison, so it is always computed live (it agrees with the
// cache because both derive from diffContentWidth(m)).
func (m model) diffNarrow() bool { return idiff.Narrow(diffContentWidth(m)) }

// recomputeDiffGeometry (re)populates the diff-overlay geometry cache from the
// current diffRows / diffFiles and terminal width. It is called at the only two
// events that change any input: the overlay opening (activateDiffButton) and a
// resize (WindowSizeMsg). It sets diffGeomValid so the read-side accessors use the
// cache; with an empty diff it invalidates instead (an unopened/closed overlay).
func (m *model) recomputeDiffGeometry() {
	if len(m.diffRows) == 0 {
		m.diffGeomValid = false
		m.diffUnifiedC = nil
		m.diffUnifiedMaxW = 0
		m.diffGutterC = 0
		m.diffTextColC = 0
		m.diffPaneLeftC = 0
		m.diffPaneRightC = 0
		m.diffLangsC = nil
		return
	}
	visW := diffContentWidth(*m)
	if idiff.Narrow(visW) {
		m.diffUnifiedC = strings.Split(strings.TrimRight(idiff.Render(m.diffFiles, visW, highlight), "\n"), "\n")
		maxW := 0
		for _, l := range m.diffUnifiedC {
			if w := lipgloss.Width(l); w > maxW {
				maxW = w
			}
		}
		m.diffUnifiedMaxW = maxW
		m.diffGutterC, m.diffTextColC, m.diffPaneLeftC, m.diffPaneRightC, m.diffLangsC = 0, 0, 0, 0, nil
	} else {
		m.diffGutterC = diffGutterW(m.diffRows)
		m.diffTextColC = diffTextCol(visW, m.diffGutterC)
		m.diffPaneLeftC, m.diffPaneRightC = diffPaneMax(m.diffRows, m.diffTextColC)
		m.diffLangsC = diffLangs(m.diffRows)
		m.diffUnifiedC, m.diffUnifiedMaxW = nil, 0
	}
	m.diffGeomValid = true
}

// diffUnified returns the cached unified lines when the geometry cache is valid,
// else a live render (the narrow-path fallback for direct-state tests).
func (m model) diffUnified() []string {
	if m.diffGeomValid {
		return m.diffUnifiedC
	}
	return m.diffUnifiedLines()
}

// diffUnifiedLines renders the parsed diff as flat unified lines (narrow path).
func (m model) diffUnifiedLines() []string {
	rendered := idiff.Render(m.diffFiles, diffContentWidth(m), highlight)
	return strings.Split(strings.TrimRight(rendered, "\n"), "\n")
}

// diffUnifiedMaxWidth returns the widest unified line's display width (narrow
// horizontal max), from the cache when valid else computed live.
func (m model) diffUnifiedMaxWidth() int {
	if m.diffGeomValid {
		return m.diffUnifiedMaxW
	}
	maxW := 0
	for _, l := range m.diffUnifiedLines() {
		if w := lipgloss.Width(l); w > maxW {
			maxW = w
		}
	}
	return maxW
}

// diffWideGeom returns the side-by-side geometry (gutter width, per-pane text
// column, and each pane's max horizontal offset) from the cache when valid, else
// computed live for the current width.
func (m model) diffWideGeom() (gutterW, textCol, leftMax, rightMax int) {
	if m.diffGeomValid {
		return m.diffGutterC, m.diffTextColC, m.diffPaneLeftC, m.diffPaneRightC
	}
	gutterW = diffGutterW(m.diffRows)
	textCol = diffTextCol(diffContentWidth(m), gutterW)
	leftMax, rightMax = diffPaneMax(m.diffRows, textCol)
	return gutterW, textCol, leftMax, rightMax
}

// diffRowLangs returns the per-row highlight languages from the cache when valid,
// else computed live.
func (m model) diffRowLangs() []string {
	if m.diffGeomValid {
		return m.diffLangsC
	}
	return diffLangs(m.diffRows)
}

// diffRowCount is the total scrollable row count for the current layout: the
// structured rows in side-by-side mode, or the unified line count when narrow.
func (m model) diffRowCount() int {
	if m.diffNarrow() {
		return len(m.diffUnified())
	}
	return len(m.diffRows)
}

// diffLangs precomputes the syntax-highlight language for each row by carrying
// the language of the most recent Header row forward, so a windowed content row
// still highlights correctly even when its file header scrolls out of view.
func diffLangs(rows []idiff.Row) []string {
	langs := make([]string, len(rows))
	cur := ""
	for i, r := range rows {
		if r.Header {
			cur = idiff.LangFromPath(strings.TrimPrefix(r.Right, "+++ "))
		}
		langs[i] = cur
	}
	return langs
}

// diffModal builds the bordered side-by-side diff box (fixed geometry,
// scrollable via m.diffYOff / m.diffXOff). Mirroring the help modal, it is NOT
// placed: viewString composites it centered over the live document so the
// playbook keeps rendering behind the overlay while the diff is shown. It
// renders from m.diffRows each frame so line-number gutters stay fixed while the
// text columns scroll (each pane clamped to its own content end).
func (m model) diffModal() string {
	box := func(content string) string {
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBlue)).
			BorderBackground(lipgloss.Color(colMantle)).
			Background(lipgloss.Color(colMantle)).
			Padding(0, 1).
			Render(content)
	}

	if len(m.diffRows) == 0 {
		return box("")
	}

	// Visible row count: height-4 (border×2 + margin×2). The box is ALWAYS
	// m.height-2 tall (visH+2 border) — 1 blank line above/below when centered —
	// so it never shrinks to content; short diffs are blank-padded below.
	visH := m.height - 4
	if visH < 1 {
		visH = 1
	}

	// Text budget: the box is ALWAYS m.width-6 wide (visW + 2 padding + 2 border),
	// never content-capped — 3 blank columns each side when centered — so short
	// rows are blank-padded to visW to keep the full width painted.
	visW := diffContentWidth(m)

	// Dialog bg painted onto padding cells so every column carries an explicit
	// background (matching the side-by-side context cells) — nothing leaks.
	dialogBg := bgANSI(colMantle)
	padRow := func(s string) string {
		if w := lipgloss.Width(s); w < visW {
			s += dialogBg + strings.Repeat(" ", visW-w) + "\x1b[0m"
		}
		return s
	}

	// Narrow terminals fall back to the flat unified render (no gutters/panes),
	// windowed vertically then horizontally like the pre-gutter overlay did.
	if m.diffNarrow() {
		lines := m.diffUnified()
		blankRow := padRow(idiff.DividerRow(visW))
		start, end := m.diffWindow(len(lines), visH)
		rows := make([]string, 0, visH)
		for _, l := range lines[start:end] {
			rows = append(rows, padRow(hslice(l, m.diffXOff, visW)))
		}
		for len(rows) < visH {
			rows = append(rows, blankRow)
		}
		return box(strings.Join(rows, "\n"))
	}

	// Side-by-side gutter layout (geometry from the cache when the overlay is open).
	gutterW, textCol, leftMax, rightMax := m.diffWideGeom()
	// ONE shared offset, clamped per pane: the short side stops at its own end
	// while the long side keeps revealing.
	leftXOff := min(m.diffXOff, leftMax)
	rightXOff := min(m.diffXOff, rightMax)

	// Blank filler row: an empty non-header Row renders as empty gutters + blank
	// panes carrying the divider, so it runs unbroken to the bottom border.
	blankRow := padRow(idiff.RenderRow(idiff.Row{}, 0, 0, textCol, gutterW, "", highlight))

	langs := m.diffRowLangs()
	start, end := m.diffWindow(len(m.diffRows), visH)
	rows := make([]string, 0, visH)
	for i := start; i < end; i++ {
		line := idiff.RenderRow(m.diffRows[i], leftXOff, rightXOff, textCol, gutterW, langs[i], highlight)
		rows = append(rows, padRow(line))
	}
	for len(rows) < visH {
		rows = append(rows, blankRow)
	}
	return box(strings.Join(rows, "\n"))
}

// diffWindow returns the [start,end) vertical slice bounds for n rows given the
// current m.diffYOff and a visible height.
func (m model) diffWindow(n, visH int) (start, end int) {
	start = m.diffYOff
	if start < 0 {
		start = 0
	}
	end = start + visH
	if end > n {
		end = n
	}
	if start > end {
		start = end
	}
	return start, end
}

// clampDiffScroll ensures diffYOff and diffXOff stay within the valid scroll
// range for the current terminal dimensions and diff content. Mirrors
// clampHelpScroll. The horizontal max is the wider pane's content end
// (max(leftMax, rightMax)) so h/l can reveal the longest line on either side.
func (m *model) clampDiffScroll() {
	if len(m.diffRows) == 0 {
		m.diffYOff = 0
		m.diffXOff = 0
		return
	}
	visH := m.height - 4
	if visH < 1 {
		visH = 1
	}
	maxY := m.diffRowCount() - visH
	if maxY < 0 {
		maxY = 0
	}
	if m.diffYOff > maxY {
		m.diffYOff = maxY
	}
	if m.diffYOff < 0 {
		m.diffYOff = 0
	}

	visW := diffContentWidth(*m)
	var maxX int
	if m.diffNarrow() {
		maxX = m.diffUnifiedMaxWidth() - visW
	} else {
		_, _, leftMax, rightMax := m.diffWideGeom()
		maxX = max(leftMax, rightMax)
	}
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
	if h := (m.height - 4) / 2; h > 1 {
		return h
	}
	return 1
}

// diffPage returns the full visible diff text height (≥1) for Ctrl+F/Ctrl+B.
func diffPage(m model) int {
	if h := m.height - 4; h > 1 {
		return h
	}
	return 1
}

// diffHalfW returns half the visible diff text width (≥1) for L/H scroll.
func diffHalfW(m model) int {
	if w := diffContentWidth(m) / 2; w > 1 {
		return w
	}
	return 1
}
