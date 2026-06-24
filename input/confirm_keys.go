package input

import "unicode"

type confirmAction int

const (
	actNone confirmAction = iota
	actAffirm
	actNegate
	actSubmit
	actFocusLeft
	actFocusRight
	actToggle
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

func firstLower(s string) rune {
	for _, r := range s {
		return unicode.ToLower(r)
	}
	return 0
}

// resolveConfirmKey maps a normalized key string to an action. affKey/negKey are
// the resolved accelerators. y/n are accepted as aliases only when they do not
// clash with the opposite side's accelerator. Navigation is arrows + tab only
// (no h/l, so letters never get stolen from accelerators).
func resolveConfirmKey(key string, affKey, negKey rune) confirmAction {
	switch key {
	case "enter":
		return actSubmit
	case "esc", "ctrl+c":
		return actCancel
	case "tab", "shift+tab":
		return actToggle
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
	case c == 'y' && negKey != 'y':
		return actAffirm
	case c == 'n' && affKey != 'n':
		return actNegate
	}
	return actNone
}
