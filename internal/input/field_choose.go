package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Indicator glyphs (Nerd Font, each 1 cell wide).
const (
	// Single-select uses a radio whose glyph reflects FOCUS (the highlighted row).
	radioFocused   = "󰄯" // U+F012F — single-select, highlighted row
	radioUnfocused = "󰄰" // U+F0130 — single-select, non-highlighted row
	// Multi-select uses a checkbox whose glyph reflects CHECK status only
	// (independent of focus).
	checkboxChecked   = "󰄵" // U+F0135 — multi-select, checked
	checkboxUnchecked = "󰄱" // U+F0131 — multi-select, unchecked

	// gutterLen is the number of terminal columns used by the fixed gutter
	// that precedes every option label: " <indicator> " = 3 cells.
	gutterLen = 3

	// otherBoxMinW is the minimum width for the embedded "other" textField box.
	// Below it the textField renders wider than its column and the highlight
	// padding would re-wrap; at real dialog widths (innerW >= 12) it never binds.
	otherBoxMinW = boxBorder + boxPadL + iconCol + scrollGap + scrollCol + 1
)

const maxVisibleRows = 8

// chooseField implements the field interface for a themed list (no fuzzy).
// It supports single- and multi-select, windowed scroll, and
// an optional free-text "other" entry at the end.
type chooseField struct {
	theme      Theme
	variant    string
	options    []string // base options (excluding the "other" row)
	multi      bool
	otherLabel string // "" → no other row

	highlight int    // currently highlighted row index (0-based, over all rows)
	selected  int    // single mode: selected option index (-1 = none)
	toggled   []bool // multi mode: toggled[i] for options[i]
	// otherField is created eagerly when otherLabel != ""; it always renders its
	// 4-line box (static height, no resize on focus).
	otherField *textField
}

// newChooseField constructs a chooseField. other=="" → no free-text entry.
// When other is non-empty, the embedded textField is created eagerly so the
// other row always renders its 4-line box (no lazy creation, no resize on focus).
func newChooseField(theme Theme, variant string, options []string, multi bool, other string) *chooseField {
	toggled := make([]bool, len(options))
	var otherField *textField
	if other != "" {
		// The "other" label is rendered as a heading above the box, so the box
		// itself carries no placeholder.
		otherField = newTextField(theme, "", "", 2, false)
	}
	return &chooseField{
		theme:      theme,
		variant:    variant,
		options:    options,
		multi:      multi,
		otherLabel: other,
		highlight:  0,
		selected:   -1,
		toggled:    toggled,
		otherField: otherField,
	}
}

// totalRows returns the total number of visible rows (options + optional other).
func (f *chooseField) totalRows() int {
	n := len(f.options)
	if f.otherLabel != "" {
		n++
	}
	return n
}

// isOtherRow returns true if idx points to the trailing "other" row.
func (f *chooseField) isOtherRow(idx int) bool {
	return f.otherLabel != "" && idx == len(f.options)
}

// handle processes one message while the field is focused.
func (f *chooseField) handle(msg tea.Msg) (field, fieldAction, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f, fieldNone, nil
	}

	c := *f
	total := c.totalRows()

	// When the highlight is on the other row, the embedded textField is implicitly
	// active. Route keys to it EXCEPT for navigation/submit/cancel keys that belong
	// to the choose layer. otherField is always non-nil when otherLabel != "".
	if c.isOtherRow(c.highlight) {
		switch {
		case kp.Code == tea.KeyEscape:
			return &c, fieldCancel, nil

		case kp.String() == "ctrl+c":
			return &c, fieldCancel, nil

		// Arrow keys leave the other row (navigate the list); they do NOT type.
		case kp.Code == tea.KeyUp:
			if c.highlight > 0 {
				c.highlight--
			}
			return &c, fieldNone, nil

		case kp.Code == tea.KeyDown:
			if c.highlight < total-1 {
				c.highlight++
			}
			return &c, fieldNone, nil

		// Shift+Enter → newline into the embedded field.
		case kp.Code == tea.KeyEnter && kp.Mod.Contains(tea.ModShift):
			f2, _, cmd := c.otherField.handle(msg)
			c.otherField = f2.(*textField)
			return &c, fieldNone, cmd

		// Plain Enter → submit the whole choose with the field's current value.
		case kp.Code == tea.KeyEnter:
			return &c, fieldDone, nil

		default:
			// Forward everything else (printable chars, Backspace, etc.) to the field.
			f2, _, cmd := c.otherField.handle(msg)
			c.otherField = f2.(*textField)
			return &c, fieldNone, cmd
		}
	}

	// Highlight is NOT the other row — original navigation/selection behavior.
	switch {
	case kp.Code == tea.KeyEscape:
		return f, fieldCancel, nil

	case kp.String() == "ctrl+c":
		return f, fieldCancel, nil

	case kp.Code == 'j' || kp.Code == tea.KeyDown:
		if c.highlight < total-1 {
			c.highlight++
		}
		return &c, fieldNone, nil

	case kp.Code == 'k' || kp.Code == tea.KeyUp:
		if c.highlight > 0 {
			c.highlight--
		}
		return &c, fieldNone, nil

	case (kp.Code == tea.KeySpace || kp.Code == ' ') && c.multi:
		if !c.isOtherRow(c.highlight) {
			c.toggled[c.highlight] = !c.toggled[c.highlight]
		}
		return &c, fieldNone, nil

	case kp.Code == tea.KeyEnter:
		if c.multi {
			return &c, fieldDone, nil
		}
		// Single: select highlighted and done.
		c.selected = c.highlight
		return &c, fieldDone, nil
	}

	return f, fieldNone, nil
}

// windowBounds returns the viewport slice [start, end) for the visible window,
// and whether up/down scroll indicators should be shown.
// This is the single source of truth used by both view() and lines().
func (f *chooseField) windowBounds() (viewStart, viewEnd int, showUp, showDown bool) {
	total := f.totalRows()
	maxVis := maxVisibleRows

	viewStart = 0
	viewEnd = total
	if total > maxVis {
		viewStart = f.highlight - maxVis/2
		if viewStart < 0 {
			viewStart = 0
		}
		viewEnd = viewStart + maxVis
		if viewEnd > total {
			viewEnd = total
			viewStart = viewEnd - maxVis
			if viewStart < 0 {
				viewStart = 0
			}
		}
		showUp = viewStart > 0
		showDown = viewEnd < total
	}
	return
}

// wrapLabel wraps labelText into lines of at most colW visible characters.
// It returns one or more strings; if the text fits in colW, it returns a
// single-element slice.  Wrapping is done at word boundaries (spaces); long
// words that exceed colW are broken at the column boundary.
func wrapLabel(labelText string, colW int) []string {
	if colW <= 0 {
		return []string{labelText}
	}
	wrapped := lipgloss.Wrap(labelText, colW, " ")
	return strings.Split(wrapped, "\n")
}

// renderOptionRow builds the visual lines for a single list option (not the
// "other" row).  It returns the lines that should be appended to rows.
//
// Layout per first visual line:
//
//	" " + <indicator> + " " + text
//
// The gutter is always exactly gutterLen (3) terminal columns:
// 1 leading space + 1 indicator glyph + 1 trailing space.
// Continuation lines are indented gutterLen spaces to align under the label.
// Highlighted rows are padded to innerW with the highlight background.
func (f *chooseField) renderOptionRow(
	i, innerW int,
	isHL bool,
	hlStyle, mutedStyle, markerSelStyle lipgloss.Style,
) []string {
	opt := f.options[i]

	// Choose indicator glyph.
	var indicator string
	if f.multi {
		if f.toggled[i] {
			indicator = checkboxChecked
		} else {
			indicator = checkboxUnchecked
		}
	} else {
		if isHL {
			indicator = radioFocused
		} else {
			indicator = radioUnfocused
		}
	}

	// Width available for the label text.
	textColW := innerW - gutterLen
	if textColW < 1 {
		textColW = 1
	}

	// Wrap the option text.
	textLines := wrapLabel(opt, textColW)

	// Continuation indent: gutterLen spaces.
	contIndent := strings.Repeat(" ", gutterLen)

	var resultLines []string
	for li, tl := range textLines {
		var lineText string
		if li == 0 {
			lineText = " " + indicator + " " + tl
		} else {
			lineText = contIndent + tl
		}

		if isHL {
			// Pad the whole line to innerW with the highlight background.
			resultLines = append(resultLines, hlStyle.Width(innerW).Render(lineText))
		} else {
			// Non-highlighted rows use the muted (unselected-tab) foreground so
			// they read as clearly dimmer than the highlighted row. A toggled
			// multi checkbox keeps the accent fill so its state stays visible.
			if li == 0 {
				var indStyled string
				if f.multi && f.toggled[i] {
					indStyled = markerSelStyle.Render(indicator)
				} else {
					indStyled = mutedStyle.Render(indicator)
				}
				resultLines = append(resultLines,
					mutedStyle.Render(" ")+indStyled+mutedStyle.Render(" ")+mutedStyle.Render(tl))
			} else {
				resultLines = append(resultLines, mutedStyle.Render(lineText))
			}
		}
	}
	return resultLines
}

// view renders the list rows with optional windowed scroll.
// innerW is the width available inside the outer frame.
func (f *chooseField) view(innerW int, focused bool) string {
	viewStart, viewEnd, showUp, showDown := f.windowBounds()

	selBg, selFg := f.theme.ButtonSelBg, f.theme.ButtonSelFg
	switch f.variant {
	case "danger":
		selBg = f.theme.Danger
	case "warning":
		selBg = f.theme.Warning
		selFg = f.theme.Base
	}

	hlStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(selBg)).
		Foreground(lipgloss.Color(selFg))
	mutedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(f.theme.Muted))
	markerSelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(f.theme.Accent))

	var rows []string

	// Scroll indicator at top if clipped.
	if showUp {
		rows = append(rows, mutedStyle.Render("  ↑ more"))
	}

	for i := viewStart; i < viewEnd; i++ {
		isHL := focused && i == f.highlight

		if f.isOtherRow(i) {
			// The "other" entry renders as a heading line ("<indicator> Label:")
			// followed by a 4-line input box indented under the label. The cursor
			// is only shown when this row is focused, so blur the embedded textarea
			// otherwise.
			if isHL {
				f.otherField.ta.Focus()
			} else {
				f.otherField.ta.Blur()
			}

			// Indicator: single → checked radio when highlighted; multi → checked
			// checkbox when the other field has text.
			var otherIndicator string
			if f.multi {
				if f.otherField.value() != "" {
					otherIndicator = checkboxChecked
				} else {
					otherIndicator = checkboxUnchecked
				}
			} else {
				if isHL {
					otherIndicator = radioFocused
				} else {
					otherIndicator = radioUnfocused
				}
			}

			// Reserve one trailing column so the highlight extends a space past
			// the box's right border (matching the gutter indent on the left).
			boxW := innerW - gutterLen - 1
			if boxW < otherBoxMinW {
				boxW = otherBoxMinW
			}
			hlW := gutterLen + boxW + 1
			indent := strings.Repeat(" ", gutterLen)

			// Focus-dependent styling:
			//  - focused   → selected background, bright-white (selFg) label,
			//                border, icon, and text; whole item highlighted.
			//  - unfocused → label/border/icon/text in the muted (unselected-tab)
			//                colour, no background.
			var st taStyle
			if isHL {
				st = taStyle{icon: selFg, border: selFg, text: selFg, bg: selBg, placeholder: false}
			} else {
				st = taStyle{icon: f.theme.Muted, border: f.theme.Muted, text: f.theme.Muted, bg: "", placeholder: false}
			}

			// Heading line: " <indicator> <label>:".
			labelLine := " " + otherIndicator + " " + f.otherLabel + ":"
			if isHL {
				rows = append(rows, hlStyle.Width(hlW).Render(labelLine))
			} else {
				rows = append(rows, mutedStyle.Render(labelLine))
			}

			// Input box, indented to align under the label text.
			boxView := f.otherField.viewWith(boxW, st)
			for _, bl := range strings.Split(boxView, "\n") {
				if isHL {
					// Full-item highlight: the indent carries the background too,
					// and the line is padded to hlW so it spans the whole item.
					rows = append(rows, hlStyle.Width(hlW).Render(hlStyle.Render(indent)+bl))
				} else {
					rows = append(rows, indent+bl)
				}
			}
			continue
		}

		optRows := f.renderOptionRow(i, innerW, isHL, hlStyle, mutedStyle, markerSelStyle)
		rows = append(rows, optRows...)
	}

	// Scroll indicator at bottom if clipped.
	if showDown {
		rows = append(rows, mutedStyle.Render("  ↓ more"))
	}

	return strings.Join(rows, "\n")
}

// value returns the selected value(s).
// Single: selected option or typed other text.
// Multi: \n-joined selected options (+ other text if non-empty, regardless of highlight).
func (f *chooseField) value() string {
	// Single-select: the other row is highlight-driven (only when focused there do
	// we return the typed text; otherwise the selected option index governs).
	if !f.multi {
		if f.isOtherRow(f.highlight) && f.otherField != nil {
			otherVal := f.otherField.value()
			if otherVal != "" {
				return otherVal
			}
		}
		if f.selected >= 0 && f.selected < len(f.options) {
			return f.options[f.selected]
		}
		return ""
	}

	// Multi-select: collect all toggled options, then append non-empty other text
	// regardless of whether the other row is currently highlighted.  This prevents
	// typed other text from being silently dropped when the user navigates away.
	var parts []string
	for i, opt := range f.options {
		if f.toggled[i] {
			parts = append(parts, opt)
		}
	}
	if f.otherField != nil {
		if otherVal := f.otherField.value(); otherVal != "" {
			parts = append(parts, otherVal)
		}
	}
	return strings.Join(parts, "\n")
}

// filled returns true if a selection has been made (single) or ≥1 selected (multi).
// For multi mode, a non-empty other-field buffer counts as filled regardless of
// which row is highlighted — this prevents the typed text from being silently
// dropped and ensures form required-field validation sees it correctly.
// For single mode, the other field only counts when it is the highlighted row
// (highlight-driven semantics are unchanged).
func (f *chooseField) filled() bool {
	if f.multi {
		for _, t := range f.toggled {
			if t {
				return true
			}
		}
		// Also filled if other text was typed, even when not currently focused.
		if f.otherField != nil && f.otherField.value() != "" {
			return true
		}
		return false
	}
	// Single: other row fills only when highlighted.
	if f.isOtherRow(f.highlight) && f.otherField != nil && f.otherField.value() != "" {
		return true
	}
	return f.selected >= 0
}

// optionLineCount returns the number of visual lines a single option row at
// index i occupies given innerW, accounting for label wrapping.
// The "other" row always occupies 4 lines (2-row textarea + top/bottom border).
func (f *chooseField) optionLineCount(i, innerW int) int {
	if f.isOtherRow(i) {
		// 1 heading line + the static 4-line box (otherField.lines() = taHeight +
		// boxBorder = 4). Apply the same floor as view() so lines() == view().
		boxW := innerW - gutterLen
		if boxW < otherBoxMinW {
			boxW = otherBoxMinW
		}
		return 1 + f.otherField.lines(boxW)
	}
	textColW := innerW - gutterLen
	if textColW < 1 {
		textColW = 1
	}
	ls := wrapLabel(f.options[i], textColW)
	return len(ls)
}

// lines returns the rendered height of this field.
// It mirrors the row count that view() emits: window rows + indicator rows.
// The "other" row always counts as its box height (4 lines) regardless of
// focus, so lines() is static for a given option set and width.
// When option labels wrap, each extra visual line is counted.
func (f *chooseField) lines(innerW int) int {
	viewStart, viewEnd, showUp, showDown := f.windowBounds()
	count := 0
	for i := viewStart; i < viewEnd; i++ {
		count += f.optionLineCount(i, innerW)
	}
	if showUp {
		count++
	}
	if showDown {
		count++
	}
	return count
}

// initCmd returns nil — the choose field needs no cursor blink.
func (f *chooseField) initCmd() tea.Cmd { return nil }
