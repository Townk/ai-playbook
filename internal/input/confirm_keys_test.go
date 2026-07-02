package input

import "testing"

func TestDeriveKeys(t *testing.T) {
	cases := []struct {
		aff, neg string
		wa, wn   rune
	}{
		{"Quit", "Cancel", 'q', 'c'},
		{"Yes", "No", 'y', 'n'},
		{"Save", "Skip", 'y', 'n'}, // collision → fallback
		{"", "No", 'y', 'n'},       // empty → fallback
		{"Dismiss", "Keep", 'd', 'k'},
	}
	for _, c := range cases {
		if a, n := deriveKeys(c.aff, c.neg); a != c.wa || n != c.wn {
			t.Fatalf("deriveKeys(%q,%q)=%c,%c want %c,%c", c.aff, c.neg, a, n, c.wa, c.wn)
		}
	}
}

func TestResolveConfirmKey(t *testing.T) {
	aff, neg := 'q', 'c'
	cases := []struct {
		key  string
		want confirmAction
	}{
		{"q", actAffirm}, {"Q", actAffirm},
		{"c", actNegate}, {"C", actNegate},
		{"y", actAffirm}, // alias, negKey != 'y'
		{"n", actNegate}, // alias, affKey != 'n'
		{"enter", actSubmit},
		{"esc", actCancel}, {"ctrl+c", actCancel},
		{"tab", actToggleNext}, {"shift+tab", actTogglePrev},
		{"left", actFocusLeft}, {"right", actFocusRight},
		{"z", actNone},
	}
	for _, c := range cases {
		if got := resolveConfirmKey(c.key, aff, neg, 0); got != c.want {
			t.Fatalf("resolveConfirmKey(%q)=%d want %d", c.key, got, c.want)
		}
	}
}

func TestAliasConflict(t *testing.T) {
	// affirmative "Nope" → affKey 'n'; the 'n' accelerator must affirm, and must
	// NOT be reinterpreted as the negate alias.
	if got := resolveConfirmKey("n", 'n', 'c', 0); got != actAffirm {
		t.Fatalf("'n' should hit the affirmative accelerator, got %d", got)
	}
}

func TestResolveConfirmKey_Tertiary(t *testing.T) {
	// terKey 'q' resolves to actTertiary; Tab/Shift-Tab map to the directional actions.
	if got := resolveConfirmKey("q", 'y', 'n', 'q'); got != actTertiary {
		t.Errorf("q -> %v, want actTertiary", got)
	}
	if got := resolveConfirmKey("tab", 'y', 'n', 'q'); got != actToggleNext {
		t.Errorf("tab -> %v, want actToggleNext", got)
	}
	if got := resolveConfirmKey("shift+tab", 'y', 'n', 'q'); got != actTogglePrev {
		t.Errorf("shift+tab -> %v, want actTogglePrev", got)
	}
	// With no tertiary (terKey 0), 'q' is inert.
	if got := resolveConfirmKey("q", 'y', 'n', 0); got != actNone {
		t.Errorf("q with no tertiary -> %v, want actNone", got)
	}
}
