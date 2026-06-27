// adapt.go — the adapt-on-run UI affordances (Task 9): the exported valid-playbook
// predicate the launcher's junk-guard reuses, the "adapted from <slug>" banner
// text, and the original→adapted diff overlay shown by the `d` keybind.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ValidatePlaybook reports whether md is a REAL final playbook (an H1 title AND at
// least one runnable code block) rather than a narration. It is the exported
// wrapper the adapt-on-run junk-guard (internal/launcher) uses to decide whether a
// freshly adapted document is safe to display: it REUSES the unexported
// isValidPlaybook predicate and the Render block counter (the same machinery the
// stream-EOF final-draft guard uses) rather than reimplementing the check, so the
// definition of "a valid playbook" stays single-sourced.
//
// The width passed to Render only affects layout, never the block COUNT, so a
// fixed nominal width is used.
func ValidatePlaybook(md string) bool {
	_, _, blocks := Render(md, 80, nil, "")
	return isValidPlaybook(md, len(blocks))
}

// adaptedBanner is the subtitle/banner caption shown for an adapt-on-run render:
// "adapted from <slug>". The pager reuses the subtitle slot to display it (set in
// Main when --adapted-from is supplied), so no new header layout is introduced.
func adaptedBanner(slug string) string { return "adapted from " + slug }

// unifiedDiffLines computes a line-oriented unified-ish diff of orig vs adapted:
// each result line is prefixed with " " (context), "-" (removed from orig), or "+"
// (added in adapted). It walks an LCS table so unchanged lines are shared and only
// the real edits are marked — enough for the `d` original→adapted review overlay
// without pulling in an external diff dependency.
func unifiedDiffLines(orig, adapted string) []string {
	a := strings.Split(orig, "\n")
	b := strings.Split(adapted, "\n")
	n, m := len(a), len(b)

	// LCS lengths: dp[i][j] = LCS length of a[i:] and b[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+b[j])
	}
	return out
}

// buildDiffLines renders the original→adapted unified diff as colored Wide lines
// for the `d` overlay: a heading, then each diff line hunk-styled (additions green,
// deletions red, context plain) via the same diffLineStyle the render path uses.
func buildDiffLines(slug, orig, adapted string) []Line {
	head := lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true).
		Render("▓▓▓ original → adapted (" + slug + ")")
	lines := []Line{{Text: head, Wide: true}, {Text: "", Wide: true}}
	for _, dl := range unifiedDiffLines(orig, adapted) {
		fg, _ := diffLineStyle(dl)
		lines = append(lines, Line{Text: lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Render(dl), Wide: true})
	}
	return lines
}

// diffView renders the full-screen original→adapted diff overlay (raised by the `d`
// keybind while an adapted playbook is shown). It mirrors the normal document
// chrome — a title row, a blank, the scrollable windowed diff body, and the status
// bar — but draws the diff lines instead of the playbook.
func (m model) diffView() string {
	cw := m.contentWidth()
	rows := Window(m.diffLines, m.xOff, m.diffYOff, cw, m.body())
	pos, size := vthumb(len(m.diffLines), m.body(), m.diffYOff)
	pad := func(s string) string { return padTo(s, m.width) }
	var sb strings.Builder
	sb.WriteString(pad("") + "\n")
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true).
		Render("▓▓▓ diff — " + adaptedBanner(m.adaptedFrom))
	sb.WriteString(pad(title) + "\n")
	sb.WriteString(pad("") + "\n")
	for i := 0; i < m.body(); i++ {
		if i < len(rows) {
			sb.WriteString("  " + padTo(rows[i], cw) + vscrollCell(i, pos, size) + "\n")
		} else {
			sb.WriteString(pad("") + "\n")
		}
	}
	sb.WriteString("  " + m.statusBar())
	return sb.String()
}

// clampDiffScroll keeps the diff overlay's vertical offset within bounds.
func (m *model) clampDiffScroll() {
	maxY := len(m.diffLines) - m.body()
	if maxY < 0 {
		maxY = 0
	}
	if m.diffYOff > maxY {
		m.diffYOff = maxY
	}
	if m.diffYOff < 0 {
		m.diffYOff = 0
	}
}
