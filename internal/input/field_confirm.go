package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

// confirmField implements the field interface for a two-or-three-button
// confirm row. It is variant-aware for focused button colors.
type confirmField struct {
	theme       Theme
	variant     string
	affirmative string
	negative    string
	tertiary    string // "" = two-button (default); non-empty adds a third button
	affKey      rune
	negKey      rune
	terKey      rune
	focus       int    // 0 = affirmative, 1 = negative, 2 = tertiary
	accepted    bool   // true once the user has submitted
	accepted_v  string // "yes" | "no" | "quit" — set on accept
}

// newConfirmField constructs a confirmField. affirmative and negative are the
// button labels; defaultNegative starts focus on the negative button.
func newConfirmField(theme Theme, variant, affirmative, negative string, defaultNegative bool) *confirmField {
	aff, neg := deriveKeys(affirmative, negative)
	focus := 0
	if defaultNegative {
		focus = 1
	}
	return &confirmField{
		theme:       theme,
		variant:     variant,
		affirmative: affirmative,
		negative:    negative,
		affKey:      aff,
		negKey:      neg,
		focus:       focus,
	}
}

// buttonCount is 2 by default, 3 when a tertiary label is set.
func (f *confirmField) buttonCount() int {
	if f.tertiary != "" {
		return 3
	}
	return 2
}

// focusValue maps a focus index to its submit value.
func focusValue(focus int) string {
	switch focus {
	case 0:
		return "yes"
	case 1:
		return "no"
	default:
		return "quit"
	}
}

// handle processes one key message while the field is focused.
func (f *confirmField) handle(msg tea.Msg) (field, fieldAction, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f, fieldNone, nil
	}
	n := f.buttonCount()
	switch resolveConfirmKey(confirmKeyString(kp), f.affKey, f.negKey, f.terKey) {
	case actAffirm:
		c := *f
		c.accepted = true
		c.accepted_v = "yes"
		return &c, fieldDone, nil
	case actNegate:
		c := *f
		c.accepted = true
		c.accepted_v = "no"
		return &c, fieldDone, nil
	case actTertiary:
		if f.tertiary == "" {
			return f, fieldNone, nil
		}
		c := *f
		c.accepted = true
		c.accepted_v = "quit"
		return &c, fieldDone, nil
	case actSubmit:
		c := *f
		c.accepted = true
		c.accepted_v = focusValue(f.focus)
		return &c, fieldDone, nil
	case actFocusLeft:
		c := *f
		if c.focus > 0 {
			c.focus--
		}
		return &c, fieldNone, nil
	case actFocusRight:
		c := *f
		if c.focus < n-1 {
			c.focus++
		}
		return &c, fieldNone, nil
	case actToggleNext:
		c := *f
		c.focus = (f.focus + 1) % n
		return &c, fieldNone, nil
	case actTogglePrev:
		c := *f
		c.focus = (f.focus - 1 + n) % n
		return &c, fieldNone, nil
	case actCancel:
		return f, fieldCancel, nil
	}
	return f, fieldNone, nil
}

// button renders a single button with focused/unfocused colors.
func (f *confirmField) button(label string, focused bool) string {
	st := lipgloss.NewStyle().Padding(0, 2)
	if focused {
		bg, fg := f.theme.ButtonSelBg, f.theme.ButtonSelFg
		switch f.variant {
		case "danger":
			bg = f.theme.Danger
		case "warning":
			bg, fg = f.theme.Warning, f.theme.Base
		}
		return st.Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(fg)).Render(label)
	}
	return st.Background(lipgloss.Color(f.theme.ButtonBg)).Foreground(lipgloss.Color(f.theme.ButtonFg)).Render(label)
}

// view renders the button row. When focused is false, all buttons are
// rendered unfocused. The inter-button gap is painted on the theme's Mantle
// background so it doesn't fall back to the terminal default after each
// button's own background + SGR reset.
func (f *confirmField) view(innerW int, focused bool) string {
	gap := lipgloss.NewStyle().Background(lipgloss.Color(theme.Mantle)).Render("    ")
	btns := []string{
		f.button(f.affirmative, focused && f.focus == 0),
		f.button(f.negative, focused && f.focus == 1),
	}
	if f.tertiary != "" {
		btns = append(btns, f.button(f.tertiary, focused && f.focus == 2))
	}
	return strings.Join(btns, gap)
}

// value returns "yes", "no", or "quit". Before submission, it returns the
// value corresponding to the current focus (so callers can inspect intent).
func (f *confirmField) value() string {
	if f.accepted {
		return f.accepted_v
	}
	return focusValue(f.focus)
}

// filled always returns true — a confirm always has a value.
func (f *confirmField) filled() bool { return true }

// initCmd returns nil — confirm needs no cursor blink.
func (f *confirmField) initCmd() tea.Cmd { return nil }

// hint returns the accelerator hint string for this field.
func (f *confirmField) hint(bg string) string {
	key, word := hintKW(f.theme, bg)
	seg := func(k, w string) string { return key.Render(k) + word.Render(" "+w) }
	sep := word.Render(" · ")
	segs := []string{
		seg("󱊷", "dismiss"),
		seg(string(f.affKey), strings.ToLower(f.affirmative)),
		seg(string(f.negKey), strings.ToLower(f.negative)),
	}
	if f.tertiary != "" && f.terKey != 0 {
		segs = append(segs, seg(string(f.terKey), strings.ToLower(f.tertiary)))
	}
	return strings.Join(segs, sep)
}
