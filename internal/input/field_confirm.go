package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// confirmField implements the field interface for a two-button confirm row.
// It is variant-aware for focused button colors.
type confirmField struct {
	theme       Theme
	variant     string
	affirmative string
	negative    string
	affKey      rune
	negKey      rune
	focus       int    // 0 = affirmative (left), 1 = negative (right)
	accepted    bool   // true once the user has submitted
	accepted_v  string // "yes" | "no" — set on accept
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

// handle processes one key message while the field is focused.
func (f *confirmField) handle(msg tea.Msg) (field, fieldAction, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f, fieldNone, nil
	}
	switch resolveConfirmKey(confirmKeyString(kp), f.affKey, f.negKey) {
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
	case actSubmit:
		c := *f
		c.accepted = true
		if f.focus == 0 {
			c.accepted_v = "yes"
		} else {
			c.accepted_v = "no"
		}
		return &c, fieldDone, nil
	case actFocusLeft:
		c := *f
		c.focus = 0
		return &c, fieldNone, nil
	case actFocusRight:
		c := *f
		c.focus = 1
		return &c, fieldNone, nil
	case actToggle:
		c := *f
		c.focus = 1 - f.focus
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

// view renders the two-button row. When focused is false, both buttons are
// rendered unfocused.
func (f *confirmField) view(innerW int, focused bool) string {
	var affFocused, negFocused bool
	if focused {
		affFocused = f.focus == 0
		negFocused = f.focus == 1
	}
	return f.button(f.affirmative, affFocused) + "    " + f.button(f.negative, negFocused)
}

// value returns "yes" or "no". Before submission, it returns the value
// corresponding to the current focus (so callers can inspect intent).
func (f *confirmField) value() string {
	if f.accepted {
		return f.accepted_v
	}
	if f.focus == 0 {
		return "yes"
	}
	return "no"
}

// filled always returns true — a confirm always has a value.
func (f *confirmField) filled() bool { return true }

// lines always returns 1 — the buttons row is a single line.
func (f *confirmField) lines(innerW int) int { return 1 }

// initCmd returns nil — confirm needs no cursor blink.
func (f *confirmField) initCmd() tea.Cmd { return nil }

// hint returns the accelerator hint string for this field.
func (f *confirmField) hint() string {
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.Key))
	word := lipgloss.NewStyle().Foreground(lipgloss.Color(f.theme.Muted))
	seg := func(k, w string) string { return key.Render(k) + word.Render(" "+w) }
	sep := word.Render(" · ")
	return strings.Join([]string{
		seg("󱊷", "dismiss"),
		seg(string(f.affKey), strings.ToLower(f.affirmative)),
		seg(string(f.negKey), strings.ToLower(f.negative)),
	}, sep)
}
