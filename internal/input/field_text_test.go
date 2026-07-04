package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

func TestTextFieldEnterIsDone(t *testing.T) {
	f := field(newTextField(defaultTheme(), "hi", "", 1, true))
	_, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone {
		t.Fatalf("Enter on a single-line field must be fieldDone, got %d", act)
	}
}

func TestTextFieldEscIsCancel(t *testing.T) {
	f := field(newTextField(defaultTheme(), "", "", 1, true))
	_, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	if act != fieldCancel {
		t.Fatalf("Esc must be fieldCancel, got %d", act)
	}
}

func TestTextFieldShiftEnterNewline(t *testing.T) {
	f := field(newTextField(defaultTheme(), "", "", 3, false))
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if act != fieldNone {
		t.Fatalf("Shift+Enter must not complete the field, got %d", act)
	}
	if !strings.Contains(f2.value(), "\n") {
		t.Fatal("Shift+Enter must insert a newline")
	}
}

func TestTextFieldFilled(t *testing.T) {
	if newTextField(defaultTheme(), "", "", 1, true).filled() {
		t.Fatal("empty field must not be filled")
	}
	if !newTextField(defaultTheme(), "x", "", 1, true).filled() {
		t.Fatal("non-empty field must be filled")
	}
}

// TestTextField_FrameBG_MantleVsEmpty mirrors TestConfirmField_HintBackground
// (field_confirm_test.go): it exercises view() directly (no outer dialog
// frame wrapping) so the check isolates the actual property under test. A
// full-frame check would be confounded — renderFrame's own
// Background(Mantle) wrap re-supplies the Mantle SGR at the very start of
// every physical line regardless of the box's own background, so scanning a
// fully-framed render for the SGR passes even with the frameBG unwired
// (verified against a reverted build). Testing view()'s own unwrapped output
// proves the frameBG parameter actually controls the box's background, not an
// artifact of framing.
func TestTextField_FrameBG_MantleVsEmpty(t *testing.T) {
	const mantleBG = "48;2;24;24;37"

	f := newTextField(defaultTheme(), "hi", "", 1, true)
	if got := f.view(40, true, ""); strings.Contains(got, mantleBG) {
		t.Errorf("frameBG=\"\" must not paint the Mantle background; got %q", got)
	}
	if got := f.view(40, true, theme.Mantle); !strings.Contains(got, mantleBG) {
		t.Errorf("frameBG=theme.Mantle must paint the Mantle background; got %q", got)
	}
}
