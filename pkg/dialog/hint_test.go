package dialog

import "testing"

// TestKeyHintChordSpacing pins the status-bar hint convention at the helper
// level: a chord's glyphs are joined by a single space, then a space and the
// label. Callers can no longer forget the modifier/key separator.
func TestKeyHintChordSpacing(t *testing.T) {
	key, word := hintKW(defaultTheme(), "")
	if got := strip(KeyHint(key, word, "newline", "󰘶", "󰌑")); got != "󰘶 󰌑 newline" {
		t.Fatalf("chord hint = %q, want %q", got, "󰘶 󰌑 newline")
	}
	if got := strip(KeyHint(key, word, "submit", "󰌑")); got != "󰌑 submit" {
		t.Fatalf("single-key hint = %q, want %q", got, "󰌑 submit")
	}
}
