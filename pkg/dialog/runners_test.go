package dialog

// runners_test.go — behavioral tests for the TTY-free half of the public
// runner API: the Measure* functions and the typed form-spec builders. The
// Run* functions wrap a live tea.Program on /dev/tty and stay on the live path.

import (
	"strings"
	"testing"
)

func TestMeasureDialogs(t *testing.T) {
	th := DefaultTheme()
	confirm := MeasureConfirm(ConfirmOptions{Theme: th, Title: "t", Prompt: "Proceed?", Affirmative: "Yes", Negative: "No", Width: 60})
	if confirm < 3 {
		t.Errorf("MeasureConfirm = %d, want a few rows", confirm)
	}
	line := MeasureLine(LineOptions{Theme: th, Title: "t", Prompt: "Name?", Width: 60})
	if line < 3 {
		t.Errorf("MeasureLine = %d, want a few rows", line)
	}
	text := MeasureText(TextOptions{Theme: th, Title: "t", Prompt: "Notes?", Height: 5, Width: 60})
	if text <= line {
		t.Errorf("MeasureText (%d) must exceed the one-line dialog (%d)", text, line)
	}
	small := MeasureChoose(ChooseOptions{Theme: th, Title: "t", Prompt: "Pick", Options: []string{"a"}, Width: 60})
	big := MeasureChoose(ChooseOptions{Theme: th, Title: "t", Prompt: "Pick", Options: []string{"a", "b", "c", "d"}, Width: 60})
	if big <= small {
		t.Errorf("MeasureChoose must grow with options: %d vs %d", small, big)
	}
	form := MeasureForm(FormOptions{Theme: th, Title: "t", Width: 60, Fields: []FormFieldSpec{
		{Key: "name", Type: "line", Prompt: "Name"},
		{Key: "ok", Type: "confirm"},
	}})
	if form < 4 {
		t.Errorf("MeasureForm = %d, want several rows", form)
	}
}

func TestBuildFormModelFromSpecs(t *testing.T) {
	th := DefaultTheme()
	m := buildFormModelFromSpecs(FormOptions{Theme: th, Title: "T", Fields: []FormFieldSpec{
		{Key: "name", Type: "line", Value: "v0", Prompt: "Your name"},
		{Key: "notes", Type: "text", Height: 0}, // height 0 → default 4
		{Key: "env", Type: "choose", Options: []string{"dev", "prod"}},
		{Key: "sure", Type: "confirm"},           // labels default Yes/No
		{Key: "odd", Type: "definitely-unknown"}, // unknown → one-line fallback
	}})
	if len(m.fields) != 5 || len(m.specs) != 5 {
		t.Fatalf("fields/specs = %d/%d, want 5/5", len(m.fields), len(m.specs))
	}
	// Prompt falls back to Key when empty.
	if m.specs[0].label != "Your name" || m.specs[1].label != "notes" {
		t.Errorf("labels = %q/%q, want prompt then key fallback", m.specs[0].label, m.specs[1].label)
	}
	// The line field carries its initial value; the confirm defaults to Yes.
	if m.fields[0].value() != "v0" {
		t.Errorf("line field value = %q, want v0", m.fields[0].value())
	}
	if v := strings.ToLower(m.fields[3].value()); v != "yes" {
		t.Errorf("confirm default value = %q, want yes", v)
	}
}
