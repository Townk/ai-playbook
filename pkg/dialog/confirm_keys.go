package dialog

import "unicode"

type confirmAction int

const (
	actNone confirmAction = iota
	actAffirm
	actNegate
	actTertiary
	actSubmit
	actFocusLeft
	actFocusRight
	actToggleNext
	actTogglePrev
	actCancel
)

// deriveKeys returns the lowercased first-rune accelerators for the two labels.
// On collision, or an empty label, it falls back to y/n.
func deriveKeys(affirmative, negative string) (aff, neg rune) {
	aff = firstLower(affirmative)
	neg = firstLower(negative)
	if aff == 0 || neg == 0 || aff == neg {
		return 'y', 'n'
	}
	return aff, neg
}

// deriveTertiaryKey returns the label's lowercased first rune as an accelerator,
// unless it is empty or collides with either resolved primary accelerator, in
// which case it returns 0 (no accelerator — the button stays reachable via
// arrows/Tab/mouse).
func deriveTertiaryKey(label string, affKey, negKey rune) rune {
	k := firstLower(label)
	if k == 0 || k == affKey || k == negKey {
		return 0
	}
	return k
}

func firstLower(s string) rune {
	for _, r := range s {
		return unicode.ToLower(r)
	}
	return 0
}

// resolveConfirmKey maps a normalized key string to an action. affKey/negKey are
// the resolved accelerators. y/n are accepted as aliases only when they do not
// clash with the opposite side's accelerator. Navigation is arrows + tab only
// (no h/l, so letters never get stolen from accelerators). terKey is the
// optional tertiary-button accelerator; 0 means no tertiary button.
func resolveConfirmKey(key string, affKey, negKey, terKey rune) confirmAction {
	switch key {
	case "enter":
		return actSubmit
	case "esc", "ctrl+c":
		return actCancel
	case "tab":
		return actToggleNext
	case "shift+tab":
		return actTogglePrev
	case "left":
		return actFocusLeft
	case "right":
		return actFocusRight
	}
	r := []rune(key)
	if len(r) != 1 {
		return actNone
	}
	c := unicode.ToLower(r[0])
	switch {
	case c == affKey:
		return actAffirm
	case c == negKey:
		return actNegate
	case terKey != 0 && c == terKey:
		return actTertiary
	case c == 'y' && negKey != 'y':
		return actAffirm
	case c == 'n' && affKey != 'n':
		return actNegate
	}
	return actNone
}
