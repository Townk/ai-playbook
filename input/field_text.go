package input

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// textField wraps a textarea.Model and implements the field interface. It
// covers both the "line" variant (singleLine=true, height=1, no scrollbar) and
// the "text" variant (singleLine=false, multiline with scrollbar).
type textField struct {
	ta          textarea.Model
	theme       Theme
	singleLine  bool
	taHeight    int
	placeholder string // original placeholder text (so viewWith can hide/restore it)
	iconGlyph   string // prompt-column glyph (defaults to promptIcon)

	// Shell-style history recall (stage 2). history holds the loaded entries,
	// oldest first / newest last. histIdx is -1 when showing the live draft,
	// else 0..len-1 while browsing. draft is the live text saved when browsing
	// begins (restored by paging DOWN past the newest entry).
	history []string
	histIdx int
	draft   string
}

// taStyle carries the focus-dependent colors used to render a textField box.
// It lets the choose "other" row recolor the whole widget (border, icon, text,
// background) per focus state, and hide the placeholder when unfocused+empty.
type taStyle struct {
	icon        string // icon glyph foreground
	border      string // box border foreground
	text        string // textarea text foreground
	bg          string // box interior background ("" = none/terminal default)
	placeholder bool   // show the placeholder when the field is empty
}

// newTextField constructs a textField. value is the initial text; placeholder
// is shown when empty; height is the textarea viewport rows; singleLine true
// disables newline insertion and the scrollbar.
func newTextField(theme Theme, value, placeholder string, height int, singleLine bool) *textField {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.DynamicHeight = false
	ta.Prompt = ""

	s := textarea.DefaultDarkStyles()
	text := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	s.Focused.Base = lipgloss.NewStyle()
	s.Blurred.Base = lipgloss.NewStyle()
	s.Focused.Text = text
	s.Blurred.Text = text
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)

	if value != "" {
		ta.SetValue(value)
		ta.MoveToEnd()
	}
	ta.Focus()
	if height < 1 {
		height = 1
	}
	ta.SetWidth(60)
	ta.SetHeight(height)

	return &textField{
		ta:          ta,
		theme:       theme,
		singleLine:  singleLine,
		taHeight:    height,
		placeholder: placeholder,
		iconGlyph:   promptIcon,
		histIdx:     -1,
	}
}

// SetHistory installs the recall entries (oldest first / newest last) and resets
// browsing back to the live draft. The model calls this in stage 3 via the
// existing m.fld.(*textField) type-assert; newTextField/newInputModel signatures
// are intentionally left unchanged.
func (f *textField) SetHistory(h []string) {
	f.history = h
	f.histIdx = -1
}

// setWidth sizes the textarea from the innerW (frame-chrome already removed by
// the caller). It subtracts the inner-box chrome (border + left pad + icon col,
// plus scroll columns for multiline).
func (f *textField) setWidth(innerW int) {
	taW := innerW - boxBorder - boxPadL - iconCol
	if !f.singleLine {
		taW -= scrollGap + scrollCol
	}
	if taW < 1 {
		taW = 1
	}
	f.ta.SetWidth(taW)
	f.ta.SetHeight(f.taHeight)
}

// handle processes one message while the field is focused, returning the
// (possibly updated) field, a fieldAction, and any bubbletea Cmd.
func (f *textField) handle(msg tea.Msg) (field, fieldAction, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		f.ta.InsertString(msg.Content)
		return f, fieldNone, nil
	case tea.KeyPressMsg:
		key := msg.Key()
		switch {
		case key.Code == tea.KeyEscape:
			return f, fieldCancel, nil
		case msg.String() == "ctrl+c":
			return f, fieldCancel, nil
		case key.Code == tea.KeyEnter && key.Mod.Contains(tea.ModShift):
			if !f.singleLine {
				f.ta.InsertRune('\n')
			}
			return f, fieldNone, nil
		case key.Code == tea.KeyEnter:
			return f, fieldDone, nil
		case key.Code == tea.KeyUp:
			// Recall the previous entry when on the first logical line and
			// history is non-empty; otherwise fall through to cursor-up.
			if len(f.history) > 0 && f.ta.Line() == 0 {
				switch {
				case f.histIdx == -1:
					f.draft = f.ta.Value()
					f.histIdx = len(f.history) - 1
				case f.histIdx > 0:
					f.histIdx--
				default:
					// already at the oldest entry — stay put
				}
				f.ta.SetValue(f.history[f.histIdx])
				f.ta.MoveToEnd()
				return f, fieldNone, nil
			}
		case key.Code == tea.KeyDown:
			// Go forward through history when browsing and on the last logical
			// line; otherwise fall through to cursor-down.
			if f.histIdx != -1 && f.ta.Line() == f.ta.LineCount()-1 {
				if f.histIdx < len(f.history)-1 {
					f.histIdx++
					f.ta.SetValue(f.history[f.histIdx])
				} else {
					// past the newest entry — restore the live draft
					f.histIdx = -1
					f.ta.SetValue(f.draft)
				}
				f.ta.MoveToEnd()
				return f, fieldNone, nil
			}
		}
	}
	var cmd tea.Cmd
	f.ta, cmd = f.ta.Update(msg)
	return f, fieldNone, cmd
}

// view renders the rounded inner box with icon column, textarea, and (for
// multiline) the scrollbar, using the field's default theme colors. innerW is
// the width available inside the outer frame (frame-chrome already subtracted).
func (f *textField) view(innerW int, focused bool) string {
	return f.viewWith(innerW, taStyle{
		icon:        f.theme.Accent,
		border:      f.theme.FieldBorder,
		text:        f.theme.Text,
		bg:          "",
		placeholder: true,
	})
}

// viewWith renders the box with explicit focus-dependent colors. The default
// view() delegates here with the theme defaults; the choose "other" row passes
// muted colors when unfocused (and hides the placeholder) and selected
// background + bright-white foregrounds when focused.
func (f *textField) viewWith(innerW int, st taStyle) string {
	// Size the textarea from innerW each render pass.
	taW := innerW - boxBorder - boxPadL - iconCol
	if !f.singleLine {
		taW -= scrollGap + scrollCol
	}
	if taW < 1 {
		taW = 1
	}
	f.ta.SetWidth(taW)
	f.ta.SetHeight(f.taHeight)

	// Toggle the placeholder (restored before return).
	saved := f.ta.Placeholder
	if st.placeholder {
		f.ta.Placeholder = f.placeholder
	} else {
		f.ta.Placeholder = ""
	}
	defer func() { f.ta.Placeholder = saved }()

	// withBg adds st.bg as the background when one is requested. Used on every
	// inner piece so no cell is left with the terminal-default background.
	withBg := func(s lipgloss.Style) lipgloss.Style {
		if st.bg != "" {
			return s.Background(lipgloss.Color(st.bg))
		}
		return s
	}

	// Apply the requested text foreground (and background, if any). Base is
	// inherited by Text/Placeholder/CursorLine/EndOfBuffer, so setting its
	// background fills the whole textarea (including empty rows).
	s := textarea.DefaultDarkStyles()
	base := withBg(lipgloss.NewStyle())
	textStyle := withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(st.text)))
	s.Focused.Base = base
	s.Blurred.Base = base
	s.Focused.Text = textStyle
	s.Blurred.Text = textStyle
	// The line under the cursor is drawn with CursorLine, not Text, so it must
	// carry the same foreground — otherwise the typed text on the active line
	// loses st.text and renders in an indeterminate default colour.
	s.Focused.CursorLine = textStyle
	s.Blurred.CursorLine = textStyle
	s.Focused.Placeholder = withBg(s.Focused.Placeholder)
	s.Blurred.Placeholder = withBg(s.Blurred.Placeholder)
	if st.bg != "" {
		// Make the (virtual) cursor visible against the selected background.
		s.Cursor.Color = lipgloss.Color(st.text)
	}
	f.ta.SetStyles(s)

	body := lipgloss.JoinHorizontal(lipgloss.Top, iconColumnColored(f.ta.Height(), f.iconGlyph, st.icon, st.bg), f.ta.View())
	if !f.singleLine {
		gap := strings.TrimRight(strings.Repeat(strings.Repeat(" ", scrollGap)+"\n", f.ta.Height()), "\n")
		if st.bg != "" {
			gap = withBg(lipgloss.NewStyle()).Render(gap)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, gap, scrollbarColored(f, st.bg))
	}
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(st.border)).
		Padding(0, 0, 0, boxPadL)
	if st.bg != "" {
		box = box.Background(lipgloss.Color(st.bg)).BorderBackground(lipgloss.Color(st.bg))
	}
	return box.Render(body)
}

func (f *textField) value() string { return f.ta.Value() }
func (f *textField) filled() bool  { return f.value() != "" }

// lines returns the total rendered height of this field (textarea rows + box
// border rows of 2).
func (f *textField) lines(innerW int) int { return f.taHeight + boxBorder }

func (f *textField) initCmd() tea.Cmd { return textarea.Blink }

// --- helpers (moved from input.go) ------------------------------------------

func visualLineCount(f *textField) int {
	w := f.ta.Width()
	if w < 1 {
		return f.ta.LineCount()
	}
	total := 0
	for _, line := range strings.Split(f.ta.Value(), "\n") {
		rows := (lipgloss.Width(line) + w - 1) / w
		if rows < 1 {
			rows = 1
		}
		total += rows
	}
	return total
}

func scrollbar(f *textField) string { return scrollbarColored(f, "") }

// scrollbarColored renders the scroll column; bg (when non-empty) backs every
// cell with the selected background so the focused "other" box has no gaps.
func scrollbarColored(f *textField, bg string) string {
	h := f.ta.Height()
	if h < 1 {
		h = 1
	}
	off := f.ta.ScrollYOffset()
	total := visualLineCount(f)
	if total < off+h {
		total = off + h
	}
	blank := lipgloss.NewStyle()
	track := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.Rule))
	thumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.ScrollThumb))
	if bg != "" {
		blank = blank.Background(lipgloss.Color(bg))
		track = track.Background(lipgloss.Color(bg))
		thumbStyle = thumbStyle.Background(lipgloss.Color(bg))
	}
	if total <= h {
		rows := make([]string, h)
		for i := range rows {
			rows[i] = blank.Render(" ")
		}
		return strings.Join(rows, "\n")
	}
	thumb := h * h / total
	if thumb < 1 {
		thumb = 1
	}
	maxOff := total - h
	pos := 0
	if maxOff > 0 {
		pos = (h - thumb) * off / maxOff
	}
	rows := make([]string, h)
	for i := range rows {
		if i >= pos && i < pos+thumb {
			rows[i] = thumbStyle.Render("┃")
		} else {
			rows[i] = track.Render("│")
		}
	}
	return strings.Join(rows, "\n")
}

// iconColumnColored renders the prompt-icon column with an explicit glyph,
// foreground color, and optional background (so the choose "other" row can
// recolor the icon per focus state and keep the selected background unbroken,
// and callers can override the glyph via --icon).
func iconColumnColored(h int, glyph, fg, bg string) string {
	if h < 1 {
		h = 1
	}
	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	blankStyle := lipgloss.NewStyle()
	if bg != "" {
		iconStyle = iconStyle.Background(lipgloss.Color(bg))
		blankStyle = blankStyle.Background(lipgloss.Color(bg))
	}
	rows := make([]string, h)
	rows[0] = iconStyle.Render(glyph) + blankStyle.Render("  ")
	for i := 1; i < h; i++ {
		rows[i] = blankStyle.Render(strings.Repeat(" ", iconCol))
	}
	return strings.Join(rows, "\n")
}
