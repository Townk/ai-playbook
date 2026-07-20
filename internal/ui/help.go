package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type helpBind struct{ keys, desc string }
type helpGroup struct {
	title string
	binds []helpBind
}

// helpSection is a top-level group (rendered in Mauve) containing sub-groups.
type helpSection struct {
	title  string
	groups []helpGroup
}

var helpSections = []helpSection{
	{"Key Bindings", []helpGroup{
		{"Actions", []helpBind{
			{"󱁐", "hint mode for keyboard-only click"},
			{"󰳽", "mouse clicks activate buttons"},
			{"r", "refine (note persists as a session constraint)"},
			{"w", "wrap-up work in the playbook"},
			{"c", "generate a playbook for the solution"},
			{"?", "toggle this help"},
			{"q", "quit"},
			{"󱊷", "cancel/dismiss (never quits)"},
		}},
		{"Movement", []helpBind{
			{"J / ↓", "down one line"},
			{"K / ↑", "up one line"},
			{"󰘴 D / 󰘴 U", "half page down / up"},
			{"󰘴 F / 󰘴 B", "full page down / up"},
			{"G / 󰘶 G", "top / bottom"},
		}},
		{"Horizontal", []helpBind{
			{"H / L", "left / right one column"},
			{"󰘶 H / 󰘶 L", "left / right half-width"},
			{"0 / $", "line start / end"},
		}},
	}},
	{"Other Interactions", []helpGroup{
		// Buttons appear inside blocks; click them (mouse) or activate via hint mode.
		{"Buttons", []helpBind{
			{glyphCopy, "copy block to clipboard"},
			{glyphPlay, "run entire block in origin shell"},
			{glyphRun, "run block in assistant's shell"},
			{glyphStop, "stop an agent-running block"},
			{glyphViewDiff, "view the diff in a pop-up window"},
			{glyphApply, "apply diff as patch"},
			{glyphUndo, "revert applied patch"},
			{"\U0010F1DA", "invalidate cache re-run prompt"},
		}},
	}},
}

// buildHelpLines renders the keybinding cheatsheet as Wide lines: a leading
// blank, then per group a bold header + underline and /-aligned, styled
// bindings with a description column, with a blank line between groups.
// Headers are flush at content col 0; binding lines have a 2-col sub-indent.
func buildHelpLines() []Line {
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colWhite))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))

	// Compute max widths of left/right sides of each key split for /-alignment,
	// across EVERY binding in every section so the key column lines up throughout.
	maxLeftW := 0
	maxRightW := 0
	for _, s := range helpSections {
		for _, g := range s.groups {
			for _, b := range g.binds {
				left, right, _ := splitKey(b.keys)
				if w := lipgloss.Width(left); w > maxLeftW {
					maxLeftW = w
				}
				if w := lipgloss.Width(right); w > maxRightW {
					maxRightW = w
				}
			}
		}
	}

	sectionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true)
	// Sub-group titles share one color (headingColor(2) = colPeach), a level below
	// the colMauve top-section titles.
	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(headingColor(2))).Bold(true)

	var out []Line
	add := func(s string) { out = append(out, Line{Text: s, Wide: true}) }
	for si, s := range helpSections {
		if si > 0 {
			add("") // blank before the next top-level (Mauve) section
		}
		add(sectionStyle.Render(s.title)) // top-level header, Mauve
		add("")                           // blank between the section header and its first sub-group
		for gi, g := range s.groups {
			if gi > 0 {
				add("")
			}
			add(groupStyle.Render(g.title)) // sub-group header, Peach
			add(rule.Render(strings.Repeat("─", lipgloss.Width(g.title))))
			for _, b := range g.binds {
				left, right, hasSep := splitKey(b.keys)
				// Right-pad left so all "/" separators align; left-pad right for symmetry.
				leftPadded := strings.Repeat(" ", maxLeftW-lipgloss.Width(left)) + left
				var sep string
				if hasSep {
					sep = " / "
				} else {
					sep = "   "
				}
				rightPadded := right + strings.Repeat(" ", maxRightW-lipgloss.Width(right))
				add(keyStyle.Render(leftPadded+sep+rightPadded) + "  " + descStyle.Render(b.desc))
			}
		}
	}
	return out
}

// splitKey splits a key string on " / " returning left, right, and whether the
// separator was present. When there is no " / ", right is "" and hasSep is false.
func splitKey(keys string) (left, right string, hasSep bool) {
	if i := strings.Index(keys, " / "); i >= 0 {
		return keys[:i], keys[i+3:], true
	}
	return keys, "", false
}

func bi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// helpTextDims returns the modal's visible help-text area (cols x rows) and
// whether each scrollbar is shown. The title now scrolls with the content
// (m.helpLines includes it), so the modal area (m.height-4) holds, top to
// bottom: border(1) + padTop(1) + text rows + padBottom(1) + border(1) = text+4.
// Horizontally the box is capped to width-8 — the modal is centered in the full
// pane width with a 4-col margin on each side (mirroring the vertical) — and laid
// out as border(1) + leftPad(2) + text + gap(2) + vbar(needV?1:0) + border(1):
// the bar sits flush against the right border with a 2-col gap from the text, so
// the text budget is width-14, minus one more column when the vbar is shown. The
// horizontal bar (when needH) takes one text row. All dims floored at 1.
func (m model) helpTextDims() (textW, textH int, needV, needH bool) {
	contentMaxW := MaxWideWidth(m.helpLines)
	maxRows := m.height - 8
	if maxRows < 1 {
		maxRows = 1
	}
	// Two passes resolve the interaction between the bars: reserving the hbar row
	// can tip vertical overflow, and showing the vbar narrows the text budget.
	for pass := 0; pass < 2; pass++ {
		available := maxRows - bi(needH) // rows left for text after the hbar
		if available < 1 {
			available = 1
		}
		needV = len(m.helpLines) > available
		maxTextW := m.width - 14 - bi(needV)
		if maxTextW < 1 {
			maxTextW = 1
		}
		needH = contentMaxW > maxTextW
	}
	// At a tiny pane there may be no room for the hbar row; drop it so the box
	// still fits the area (one text row beats a scrollbar that overflows it).
	if maxRows-bi(needH) < 1 {
		needH = false
	}
	// Visible dims: content-sized, capped to the available area.
	textH = maxRows - bi(needH)
	if textH > len(m.helpLines) {
		textH = len(m.helpLines)
	}
	if textH < 1 {
		textH = 1
	}
	textW = m.width - 14 - bi(needV)
	if textW > contentMaxW {
		textW = contentMaxW
	}
	if textW < 1 {
		textW = 1
	}
	return textW, textH, needV, needH
}

func (m *model) clampHelpScroll() {
	textW, textH, _, _ := m.helpTextDims()
	maxY := len(m.helpLines) - textH
	if maxY < 0 {
		maxY = 0
	}
	if m.helpYOff > maxY {
		m.helpYOff = maxY
	}
	if m.helpYOff < 0 {
		m.helpYOff = 0
	}
	maxX := MaxWideWidth(m.helpLines) - textW
	if maxX < 0 {
		maxX = 0
	}
	if m.helpXOff > maxX {
		m.helpXOff = maxX
	}
	if m.helpXOff < 0 {
		m.helpXOff = 0
	}
}

func helpInnerH(m model) int { _, h, _, _ := m.helpTextDims(); return h }
func helpInnerW(m model) int { w, _, _, _ := m.helpTextDims(); return w }
func helpHalf(m model) int {
	if h := helpInnerH(m) / 2; h > 1 {
		return h
	}
	return 1
}
func helpPage(m model) int {
	if h := helpInnerH(m); h > 1 {
		return h
	}
	return 1
}
func helpHalfW(m model) int {
	if w := helpInnerW(m) / 2; w > 1 {
		return w
	}
	return 1
}

// mantleBg is the ANSI truecolor background sequence for colMantle, used to
// band each interior row so the modal background is uniform throughout.
const mantleBg = "\x1b[48;2;24;24;37m" // #181825 = R24 G24 B37

// helpModal builds the bordered keybinding box (content-sized, capped to width-8
// wide × (m.height-4) tall by helpTextDims). It is NOT placed: the View overlays
// it onto the live document view so the markdown keeps rendering behind it.
func (m model) helpModal() string {
	textW, textH, needV, needH := m.helpTextDims()
	contentW := MaxWideWidth(m.helpLines)

	// All padding is applied manually (the box uses Padding(0,0)) so both
	// scrollbars run flush to their borders. Each row is leftPad(2) + text +
	// gap(2) + vbar(1 when needV). Rows top to bottom: top pad, text rows, bottom
	// pad, then the hbar (when needH) flush against the bottom border with the
	// bottom pad as its gap above. The vbar occupies the rightmost column on every
	// row, so it runs from the top border to the bottom border.
	windowed := Window(m.helpLines, m.helpXOff, m.helpYOff, textW, textH)
	// The vbar track spans top pad + text rows + bottom pad (NOT the hbar row), so
	// when both bars show the vbar ends one cell above the hbar — they don't
	// collide at the corner. With only the vbar, this is every inner row, so it
	// still runs flush from the top border to the bottom border.
	trackH := textH + 2
	vpos, vsize := thumbTrack(len(m.helpLines), textH, trackH, m.helpYOff)
	vbar := func(trackRow int) string {
		if !needV {
			return ""
		}
		glyph, col := "│", colSurface0
		if trackRow >= vpos && trackRow < vpos+vsize {
			glyph, col = "┃", colOverlay1
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(glyph)
	}
	// band re-injects the modal bg after every inner color reset so plain gaps and
	// reset segments keep the modal background instead of the terminal's.
	blank := strings.Repeat(" ", textW)
	row := func(text string, trackRow int) string {
		return band("  "+text+"  "+vbar(trackRow), mantleBg, 0)
	}
	var body []string
	tr := 0
	body = append(body, row(blank, tr)) // top pad row
	tr++
	for _, w := range windowed {
		body = append(body, row(padTo(w, textW), tr))
		tr++
	}
	body = append(body, row(blank, tr)) // bottom pad (gap above the hbar; vbar runs through it)
	if needH {
		// Horizontal bar: a row flush to the bottom border, spanning the full inner
		// width. hscrollbarRow always renders 1 leading + 1 trailing space, so the
		// bar floats just inside the left/right borders regardless of the vbar. When
		// the vbar is shown, the trailing space lands in the vbar column (which the
		// vbar vacates on this row), so the two bars never collide at the corner.
		body = append(body, band(hscrollbarRow(contentW, m.helpXOff, textW+4+bi(needV), colMantle), mantleBg, 0))
	}

	content := strings.Join(body, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colBlue)).
		BorderBackground(lipgloss.Color(colMantle)).
		Background(lipgloss.Color(colMantle)).
		Padding(0, 0).
		Render(content)
}
