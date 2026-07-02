package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
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

func TestConfirmField_GapPaintedOnMantle(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	out := f.view(51, true)
	// The 4-space inter-button gap must carry the Mantle background SGR, not reset
	// to the terminal default. Mantle's truecolor bg sequence:
	mantle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Mantle)).Render("    ")
	if !strings.Contains(out, mantle) {
		t.Fatalf("button gap is not painted on the Mantle background:\n%q", out)
	}
}

func TestConfirmField_TertiaryButtonAndValue(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	// Two-button by default:
	if got := f.buttonCount(); got != 2 {
		t.Fatalf("buttonCount default = %d, want 2", got)
	}
	// Opt in to the third button via the Ask wrapper (mirrors how the gate wires it):
	a := &Ask{m: model{fld: f}}
	a.WithTertiaryButton("Quit")
	cf := a.m.fld.(*confirmField)
	if got := cf.buttonCount(); got != 3 {
		t.Fatalf("buttonCount with tertiary = %d, want 3", got)
	}
	if !strings.Contains(cf.view(51, true), "Quit") {
		t.Fatal("three-button view must render the Quit label")
	}
	// The 'q' accelerator selects Quit:
	nf, act, _ := cf.handle(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if act != fieldDone {
		t.Fatalf("q accelerator action = %v, want fieldDone", act)
	}
	if got := nf.value(); got != "quit" {
		t.Fatalf("value after Quit = %q, want \"quit\"", got)
	}
}

func TestConfirmField_FocusCyclesThroughThree(t *testing.T) {
	f := newConfirmField(defaultTheme(), "default", "Confirm", "Customize", false)
	a := &Ask{m: model{fld: f}}
	a.WithTertiaryButton("Quit")
	cf := a.m.fld.(*confirmField)
	// Tab cycles 0 -> 1 -> 2 -> 0.
	step := func(fld field) field {
		nf, _, _ := fld.handle(tea.KeyPressMsg{Code: tea.KeyTab})
		return nf
	}
	var cur field = cf
	for want := 1; want <= 3; want++ {
		cur = step(cur)
		got := cur.(*confirmField).focus
		if got != want%3 {
			t.Fatalf("after %d tabs focus = %d, want %d", want, got, want%3)
		}
	}
	// Right arrow clamps at the last button (does not wrap).
	cur.(*confirmField).focus = 2
	nf, _, _ := cur.handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := nf.(*confirmField).focus; got != 2 {
		t.Fatalf("right arrow at end focus = %d, want 2 (clamped)", got)
	}
}
