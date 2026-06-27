package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
