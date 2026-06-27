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
		{"tab", actToggle}, {"shift+tab", actToggle},
		{"left", actFocusLeft}, {"right", actFocusRight},
		{"z", actNone},
	}
	for _, c := range cases {
		if got := resolveConfirmKey(c.key, aff, neg); got != c.want {
			t.Fatalf("resolveConfirmKey(%q)=%d want %d", c.key, got, c.want)
		}
	}
}

func TestAliasConflict(t *testing.T) {
	// affirmative "Nope" → affKey 'n'; the 'n' accelerator must affirm, and must
	// NOT be reinterpreted as the negate alias.
	if got := resolveConfirmKey("n", 'n', 'c'); got != actAffirm {
		t.Fatalf("'n' should hit the affirmative accelerator, got %d", got)
	}
}
