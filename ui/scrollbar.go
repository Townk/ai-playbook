package ui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// vthumb returns the thumb [pos, pos+size) within a `visible`-row track for a
// `total`-row document scrolled to `off`. size≥1; (0,0) when the content fits.
func vthumb(total, visible, off int) (pos, size int) {
	if total <= visible || visible < 1 {
		return 0, 0
	}
	size = visible * visible / total
	if size < 1 {
		size = 1
	}
	pos = (visible - size) * off / (total - visible)
	return pos, size
}

// thumbTrack is like vthumb/hthumb but for a 1-D scrollbar whose `track` length
// may exceed the `visible` window — e.g. when the bar runs flush into the box's
// padding. The thumb is sized to visible/total of the full track and reaches both
// ends. Returns a full-length thumb when the content fits. Used for both axes.
func thumbTrack(total, visible, track, off int) (pos, size int) {
	if track < 1 {
		return 0, 0
	}
	if total <= visible {
		return 0, track
	}
	size = track * visible / total
	if size < 1 {
		size = 1
	}
	if size > track {
		size = track
	}
	pos = (track - size) * off / (total - visible)
	if pos < 0 {
		pos = 0
	}
	if pos > track-size {
		pos = track - size
	}
	return pos, size
}

// hthumb returns the thumb [pos, pos+size) within a `view`-wide track for a
// `blockW`-column block scrolled to `xoff` (clamped). Full track when blockW≤view.
func hthumb(blockW, view, xoff int) (pos, size int) {
	if view < 1 {
		view = 1
	}
	if blockW <= view {
		return 0, view
	}
	size = view * view / blockW
	if size < 1 {
		size = 1
	}
	maxX := blockW - view
	if xoff < 0 {
		xoff = 0
	} else if xoff > maxX {
		xoff = maxX
	}
	pos = (view - size) * xoff / maxX
	return pos, size
}

// hscrollbarRow renders a cw-wide horizontal scrollbar on the given background:
// 1 leading + 1 trailing pad space (so the bar floats inside the block instead
// of touching its edges like a divider) around a ─ track / ━ thumb spanning the
// inner width, at the block's current horizontal offset.
// bg is the background color hex string (e.g. colCodeBg or colMantle).
func hscrollbarRow(blockW, xoff, cw int, bg string) string {
	pad := lipgloss.NewStyle().Background(lipgloss.Color(bg))
	inner := cw - 2
	if inner < 1 {
		// Pane too narrow for the pads; just a bg blank.
		return pad.Render(strings.Repeat(" ", cw))
	}
	pos, size := hthumb(blockW, inner, xoff)
	if pos+size > inner {
		size = inner - pos
	}
	track := lipgloss.NewStyle().Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(colSurface0))
	thumb := lipgloss.NewStyle().Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(colOverlay1))
	var sb strings.Builder
	sb.WriteString(pad.Render(" "))
	if pos > 0 {
		sb.WriteString(track.Render(strings.Repeat("─", pos)))
	}
	if size > 0 {
		sb.WriteString(thumb.Render(strings.Repeat("━", size)))
	}
	if tail := inner - pos - size; tail > 0 {
		sb.WriteString(track.Render(strings.Repeat("─", tail)))
	}
	sb.WriteString(pad.Render(" "))
	return sb.String()
}

// vscrollCell returns the right-edge vertical-scrollbar cell (with a leading
// gap) for body row i, or "" when there is no scrollbar (size≤0).
func vscrollCell(i, pos, size int) string {
	if size <= 0 {
		return ""
	}
	if i >= pos && i < pos+size {
		return " " + lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay1)).Render("┃")
	}
	return " " + lipgloss.NewStyle().Foreground(lipgloss.Color(colSurface0)).Render("│")
}

// padTo right-pads s with spaces to w display columns (never truncates).
func padTo(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// hintCodeRow renders a code-block row for hint mode: the visible text muted
// (colSubtext) on the solid code-bg fill, EXCEPT the decorative border glyphs
// (▂ tab fill U+2582 / 🮂 bottom bar U+1FB82), which keep their normal color
// (colCodeBg foreground, no background) so the block's rounded edges look
// unchanged — only the CONTENT and TAB are recolored, not the borders.
func hintCodeRow(row string, width int, buttonCols map[int]bool) string {
	plain := []rune(strip(row))
	for len(plain) < width {
		plain = append(plain, ' ')
	}
	if len(plain) > width {
		plain = plain[:width]
	}
	const (
		kBorder = iota
		kContent
		kButton
	)
	styles := map[int]lipgloss.Style{
		kBorder:  lipgloss.NewStyle().Foreground(lipgloss.Color(colCodeBg)),
		kContent: lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Background(lipgloss.Color(colCodeBg)),
		// Button glyph cells get the hint label's dark-red background so each
		// button visually connects to its floating label.
		kButton: lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Background(lipgloss.Color(colHintLabelBg)),
	}
	kind := func(i int) int {
		if buttonCols[i] {
			return kButton
		}
		if plain[i] == '▂' || plain[i] == '\U0001FB82' {
			return kBorder
		}
		return kContent
	}
	var sb strings.Builder
	for i := 0; i < len(plain); {
		j, k := i, kind(i)
		for j < len(plain) && kind(j) == k {
			j++
		}
		sb.WriteString(styles[k].Render(string(plain[i:j])))
		i = j
	}
	return sb.String()
}

// overlayLabels splices each label char into an already-styled row at its
// display column (ANSI-aware, via hslice). Works on dim prose or filled code rows.
func overlayLabels(row string, labels map[int]string, lab lipgloss.Style) string {
	if len(labels) == 0 {
		return row
	}
	cols := make([]int, 0, len(labels))
	for c := range labels {
		cols = append(cols, c)
	}
	sort.Ints(cols)
	const big = 1 << 30
	var sb strings.Builder
	prev := 0
	for _, c := range cols {
		if c < prev {
			continue
		}
		sb.WriteString(hslice(row, prev, c-prev))
		sb.WriteString(lab.Render(labels[c]))
		prev = c + 1
	}
	sb.WriteString(hslice(row, prev, big))
	return sb.String()
}
