package input

import (
	"strings"
	"testing"
)

func TestConfirmRender(t *testing.T) {
	m := newConfirmModel(defaultTheme(), "danger", "Quit Session", "Delete it?", "Delete", "Cancel", true, 1, 1)
	m.width = 50
	plain := strip(m.render())
	if !strings.HasPrefix(strings.Split(plain, "\n")[0], "╭") {
		t.Fatal("outer border missing")
	}
	for _, want := range []string{"▓▓▓ Quit Session", "Delete it?", "Delete", "Cancel", "d delete", "c cancel", "󱊷 dismiss"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("confirm render missing %q:\n%s", want, plain)
		}
	}
}

func TestConfirmDefaultNegativeFocus(t *testing.T) {
	m := newConfirmModel(defaultTheme(), "danger", "T", "P", "Quit", "Cancel", true, 1, 1)
	if m.focus != 1 {
		t.Fatal("default-negative must focus the negative (right) button")
	}
}

func TestConfirmAcceleratorHint(t *testing.T) {
	// Yes/No labels → y/n accelerators in the hint.
	m := newConfirmModel(defaultTheme(), "default", "T", "Proceed?", "Yes", "No", false, 1, 1)
	m.width = 40
	if !strings.Contains(strip(m.render()), "y yes") || !strings.Contains(strip(m.render()), "n no") {
		t.Fatal("hint must show derived y/n accelerators")
	}
}
