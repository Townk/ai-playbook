package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestConfirmFieldAcceleratorDone(t *testing.T) {
	f := field(newConfirmField(defaultTheme(), "default", "Quit", "Cancel", false))
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if act != fieldDone || f2.value() != "yes" {
		t.Fatalf("q must accept affirmative: act=%d val=%q", act, f2.value())
	}
}

func TestConfirmFieldEnterUsesFocus(t *testing.T) {
	f := field(newConfirmField(defaultTheme(), "danger", "Quit", "Cancel", true)) // focus=negative
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone || f2.value() != "no" {
		t.Fatalf("Enter with default-negative must yield no: act=%d val=%q", act, f2.value())
	}
}

func TestConfirmFieldAlwaysFilled(t *testing.T) {
	if !newConfirmField(defaultTheme(), "default", "Yes", "No", false).filled() {
		t.Fatal("confirm field is always filled")
	}
}
