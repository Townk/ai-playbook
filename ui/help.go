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

var helpGroups = []helpGroup{
	// Buttons appear inside blocks; click them (mouse) or activate via hint mode.
	{"Buttons", []helpBind{
		{glyphCopy, "copy the block to your clipboard"},
		{glyphPlay, "send the command to your shell (review, then run)"},
		{glyphRun, "run the block in the assistant's shell"},
		{glyphStop, "stop a running block"},
		{glyphViewDiff, "view the diff side-by-side in a float"},
		{glyphApply, "apply the patch"},
		{glyphUndo, "undo an applied patch"},
		{"\U0010F1DA", "cached pill — click to regenerate (re-run, no cache)"},
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
	{"Actions", []helpBind{
		{"󱁐", "hint mode — activate a button"},
		{"󰳽", "activate a button (mouse)"},
		{"y / n", "answer the verify-success confirm (solved?)"},
		{"f", "follow-up — amend the displayed playbook"},
		{"w", "finalize — generate the final playbook draft"},
		{"?", "toggle this help"},
		{"q / 󱊷", "quit"},
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

	// Compute max widths of left/right sides of each key split for /-alignment.
	maxLeftW := 0
	maxRightW := 0
	for _, g := range helpGroups {
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

	var out []Line
	add := func(s string) { out = append(out, Line{Text: s, Wide: true}) }
	// The title scrolls with the content (no separate fixed header line).
	add(lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true).Render("Pager guide"))
	add("") // blank between the title and the first section
	for gi, g := range helpGroups {
		if gi > 0 {
			add("")
		}
		// All section titles share one color (headingColor(2) = colPeach) — they
		// are all the same "level" under the colMauve modal title.
		sectionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(headingColor(2))).Bold(true)
		add(sectionStyle.Render(g.title))
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
			// No sub-indent: the widest binding's leftmost symbol aligns with the
			// modal title and the section headers (all at the content's left edge).
			add(keyStyle.Render(leftPadded+sep+rightPadded) + "  " + descStyle.Render(b.desc))
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
