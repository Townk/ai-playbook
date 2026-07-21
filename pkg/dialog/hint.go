package dialog

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// KeyHint renders one keybinding hint segment in the status-bar style: the
// key glyphs joined by single spaces (a chord's modifier and key are always
// space-separated), rendered in the key style, then a space and the label in
// the word style. It is the single place the spacing convention lives —
// hint lines must compose segments through it rather than hand-formatting
// glyph strings. Exported so internal/ui's status bar shares the convention
// (the reverse import would cycle: internal/ui already imports this package).
func KeyHint(key, word lipgloss.Style, label string, keys ...string) string {
	return key.Render(strings.Join(keys, " ")) + word.Render(" "+label)
}
