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
			{"r", "refine the playbook"},
			{"w", "wrap-up work in the playbook"},
			{"c", "generate a playbook for the solution"},
			{"d", "view original → adapted diff (adapted run)"},
			{"?", "toggle this help"},
			{"q / 󱊷", "quit/dismiss"},
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
