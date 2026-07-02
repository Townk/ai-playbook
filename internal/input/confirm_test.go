package input

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

// mantleBgSGR is the truecolor background escape for theme.Mantle (#181825 =
// R24 G24 B37), used to assert a section's cells carry the dialog background
// instead of bleeding to the terminal default.
const mantleBgSGR = "\x1b[48;2;24;24;37m"

// TestPromptStyle_HasMantleBackground pins the shared prompt style (used by
// both the standalone confirm dialog and the embedded Ask overlay) to carry
// the dialog's Mantle background — a structural assertion that doesn't rely
// on brittle SGR-byte matching.
func TestPromptStyle_HasMantleBackground(t *testing.T) {
	got := promptStyle(defaultTheme()).GetBackground()
	want := lipgloss.Color(theme.Mantle)
	if got != want {
		t.Fatalf("promptStyle(...).GetBackground() = %v, want %v (theme.Mantle)", got, want)
	}
}

// TestConfirmDialog_PromptHasDialogBackground renders a confirm dialog and
// asserts the prompt LINE itself (not just some cell in the whole frame)
// carries the Mantle background SGR — the frame's own Background(Mantle)
// wrapper does not prevent an inner foreground-only style from resetting to
// the terminal default (see internal/input/frame.go).
func TestConfirmDialog_PromptHasDialogBackground(t *testing.T) {
	m := newConfirmModel(defaultTheme(), "default", "T", "Delete it?", "Delete", "Cancel", true, 1, 1)
	m.width = 50
	out := m.render()
	promptLine := findLine(t, out, "Delete it?")
	if !strings.Contains(promptLine, mantleBgSGR) {
		t.Fatalf("confirm prompt line missing Mantle background SGR:\n%q", promptLine)
	}
}

// findLine returns the raw (unstripped) line of out whose stripped content
// contains want, failing the test if no such line exists.
func findLine(t *testing.T, out, want string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(strip(l), want) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, strip(out))
	return ""
}

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
