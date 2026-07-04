package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func key(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func TestChooseSingleSelect(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"alpha", "beta", "gamma"}, false, ""))
	f, _, _ = f.handle(key('j')) // move to beta
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone || f2.value() != "beta" {
		t.Fatalf("j then Enter must select beta: act=%d val=%q", act, f2.value())
	}
}

func TestChooseNumberShortcut(t *testing.T) {
	// Number shortcuts are removed — pressing a digit must NOT select/jump.
	f := field(newChooseField(defaultTheme(), "default", []string{"alpha", "beta", "gamma"}, false, ""))
	_, act, _ := f.handle(key('3'))
	if act == fieldDone {
		t.Fatalf("number keys must no longer select (act=%d)", act)
	}
}

func TestChooseMultiToggle(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b", "c"}, true, ""))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeySpace}) // toggle a
	f, _, _ = f.handle(key('j'))
	f, _, _ = f.handle(key('j'))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeySpace}) // toggle c
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone {
		t.Fatalf("Enter must submit multi, act=%d", act)
	}
	if got := f2.value(); got != "a\nc" {
		t.Fatalf("multi value = %q, want \"a\\nc\"", got)
	}
}

func TestChooseRendersListNoFuzzy(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"alpha", "beta"}, false, ""))
	out := strip(f.view(40, true, ""))
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatal("options must render")
	}
	// Number prefixes are gone; radio indicators must appear instead.
	if !strings.Contains(out, "󰄯") && !strings.Contains(out, "󰄰") {
		t.Fatal("rows must show radio indicators (󰄯 or 󰄰)")
	}
}

func TestChooseOtherFreeText(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…"))
	// navigate to the trailing other entry using arrow-down (focus-to-type: no Enter to activate)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // now on the other row
	// type a custom value directly (no activate step needed)
	for _, r := range "custom" {
		f, _, _ = f.handle(key(r))
	}
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone || f2.value() != "custom" {
		t.Fatalf("other free-text must yield the typed value: act=%d val=%q", act, f2.value())
	}
}

func TestChooseOtherFocusToType(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…"))
	// move highlight onto the "other" row (index 2 → key '3' navigates+… but we
	// want focus-to-type, so use arrow-down twice to land on it WITHOUT selecting)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // now on the other row
	// typing goes straight into the field (no Enter to activate)
	for _, r := range "custom" {
		f, _, _ = f.handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Enter submits the whole choose with the typed value
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone || f2.value() != "custom" {
		t.Fatalf("focus-to-type other must submit typed value: act=%d val=%q", act, f2.value())
	}
}

func TestChooseOtherShiftEnterNewline(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a"}, false, "Other…"))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // onto other row
	f, _, _ = f.handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if !strings.Contains(f.value(), "\n") {
		t.Fatalf("Shift+Enter in other must insert a newline: %q", f.value())
	}
}

func TestChooseEscCancel(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a"}, false, ""))
	_, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	if act != fieldCancel {
		t.Fatal("Esc must cancel")
	}
}

func TestChooseOtherFilledWithActiveBuffer(t *testing.T) {
	// Focus the other row, type text, do NOT press Enter → value() is non-empty and filled() is true.
	// This covers the Tab-away-wedges-form bug: the form intercepts Tab before the field
	// commits, so otherText never gets set; filled() must look at the in-progress buffer.
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…"))
	// navigate to "other" row via arrows (focus-to-type flow)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	for _, r := range "typed" {
		f, _, _ = f.handle(key(r))
	}
	// Do NOT send Enter — simulate Tab-away scenario.
	if f.value() == "" {
		t.Fatal("value() must return the in-progress buffer when other is active")
	}
	if !f.filled() {
		t.Fatal("filled() must return true when other is active with non-empty buffer")
	}
}

// A3b: chooseField.handle started with a KeyPressMsg type-assert that dropped
// any other message, so paste never reached the embedded "other" textField.
// Highlighting the other row and sending a PasteMsg must land the content.
func TestChooseOtherPasteDelivered(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…"))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // now on the other row
	f2, _, _ := f.handle(tea.PasteMsg{Content: "pasted-value"})
	if got := f2.value(); got != "pasted-value" {
		t.Fatalf("paste on the highlighted other row must land in its value, got %q", got)
	}
}

// A3b: chooseField.initCmd unconditionally returned nil, so the embedded
// "other" textField never got its cursor-blink command wired up. When an
// other field exists, initCmd must delegate to it.
func TestChooseInitCmdBlinksWhenOtherFieldExists(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"a"}, false, "Other…")
	if cmd := f.initCmd(); cmd == nil {
		t.Fatal("chooseField.initCmd must return the other field's blink cmd when an other field exists")
	}
}

// Guards the existing no-other-field contract: initCmd must stay nil when
// there is no embedded "other" textField.
func TestChooseInitCmdNilWithoutOtherField(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"a"}, false, "")
	if cmd := f.initCmd(); cmd != nil {
		t.Fatal("chooseField.initCmd must stay nil when there is no other field")
	}
}

func TestChooseRowSpacingAndFullWidthHighlight(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"alpha", "beta"}, false, "")
	// highlight is row 0 by default
	out := strip(f.view(30, true, ""))
	first := strings.Split(out, "\n")[0]
	// Layout is now " <indicator> <label> " — leading space + indicator glyph + space.
	// The highlighted row must contain the label and be padded to the inner width.
	if !strings.Contains(first, "alpha") {
		t.Fatalf("row must contain the label 'alpha': %q", first)
	}
	if !strings.HasPrefix(first, " ") {
		t.Fatalf("row must start with a leading space: %q", first)
	}
	if lipgloss.Width(first) < 28 {
		t.Fatalf("highlighted row must span ~inner width, got width %d: %q", lipgloss.Width(first), first)
	}
}

func TestChooseLongOptionWraps(t *testing.T) {
	long := "this is a very long option label that must wrap onto a second visual line"
	f := newChooseField(defaultTheme(), "default", []string{long, "b"}, false, "")
	out := strip(f.view(24, true, ""))
	lines := strings.Split(out, "\n")
	// the long option occupies >1 visual line, and the continuation is indented
	// under the label text (past the "  N " number column), not under the number.
	if len(lines) < 3 {
		t.Fatalf("long option must wrap to multiple lines: %q", out)
	}
	// continuation line starts with the label-column indent (spaces), not a digit
	cont := lines[1]
	if strings.TrimLeft(cont, " ") == cont {
		t.Fatalf("wrapped continuation must be indented under the label: %q", cont)
	}
}

func TestChooseMultiSpaceAsRune(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b", "c"}, true, ""))
	// a real space keypress arrives as Code=' ' (0x20), Text=" "
	f2, _, _ := f.handle(tea.KeyPressMsg{Code: ' ', Text: " "})
	if f2.value() != "a" {
		t.Fatalf("space (rune) must toggle the highlighted row; value=%q", f2.value())
	}
}

func TestChooseMultiSelectionsPreservedWhenEmptyOtherFocused(t *testing.T) {
	// Regression: toggling options then arrowing onto the (empty) other row and
	// pressing Enter must return the toggled options, NOT "".
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b", "c"}, true, "Other…"))
	// Toggle "a" (highlight=0)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeySpace})
	// Move to "c" and toggle it
	f, _, _ = f.handle(key('j'))
	f, _, _ = f.handle(key('j'))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeySpace})
	// Arrow down onto the "other" row (index 3), leaving its buffer empty
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	// Press Enter — should submit with "a\nc", not ""
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone {
		t.Fatalf("Enter must submit, act=%d", act)
	}
	if got := f2.value(); got != "a\nc" {
		t.Fatalf("multi value with empty other row = %q, want \"a\\nc\"", got)
	}
}

// Task 3: the "other" row must always render as a 4-line box (2-row textarea
// + top/bottom border), regardless of whether it is focused. Height must be
// identical focused vs unfocused, and the box border must appear even when
// the other row is not highlighted.
func TestChooseOtherIsAlwaysFourLines(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…")
	unfocused := strip(f.view(40, true, "")) // highlight on row 0 → other (idx 2) unfocused
	g, _, _ := field(f).handle(tea.KeyPressMsg{Code: tea.KeyDown})
	g, _, _ = g.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // onto other row
	focused := strip(g.view(40, true, ""))
	if len(strings.Split(unfocused, "\n")) != len(strings.Split(focused, "\n")) {
		t.Fatalf("other height must NOT change on focus: %d vs %d",
			len(strings.Split(unfocused, "\n")), len(strings.Split(focused, "\n")))
	}
	if !strings.Contains(unfocused, "╭") || !strings.Contains(unfocused, "╰") {
		t.Fatalf("other input box must render even unfocused: %q", unfocused)
	}
}

// Task 3: focus-to-type on the other row must still accept typed text and
// submit it on Enter.
func TestChooseOtherFocusToTypeStillWorks(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a"}, false, "Other…"))
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // onto other row (idx 1)
	for _, r := range "hi" {
		f, _, _ = f.handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	f2, act, _ := f.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if act != fieldDone || f2.value() != "hi" {
		t.Fatalf("focus-to-type other still submits typed value: act=%d val=%q", act, f2.value())
	}
}

func TestChooseHintRangeAndEscGlyph(t *testing.T) {
	h := chooseHint(defaultTheme(), 3 /*rows*/, false /*multi*/, "")
	plain := strip(h)
	// Number range is gone.
	if strings.Contains(plain, "1-3") || strings.Contains(plain, "pick") {
		t.Fatalf("hint must not mention a number range/pick: %q", plain)
	}
	// Must still have move and dismiss glyph.
	if !strings.Contains(plain, "move") {
		t.Fatalf("hint must still show 'move': %q", plain)
	}
	if !strings.Contains(plain, "󱊷") {
		t.Fatalf("hint must use the 󱊷 ESC glyph: %q", plain)
	}
	if strings.Contains(plain, "⎋") {
		t.Fatalf("hint must not use the ⎋ glyph: %q", plain)
	}
}

func TestChooseSingleShowsRadio(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"alpha", "beta"}, false, "")
	// highlight is row 0; single-select radio reflects focus: focused glyph on the
	// highlighted row, unfocused glyph elsewhere.
	out := strip(f.view(30, true, ""))
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "󰄯") { // focused radio on the highlighted row
		t.Fatalf("highlighted single row must show the focused radio 󰄯: %q", lines[0])
	}
	if !strings.Contains(lines[1], "󰄰") { // unfocused radio on the other row
		t.Fatalf("non-highlighted single row must show the unfocused radio 󰄰: %q", lines[1])
	}
}

func TestChooseMultiShowsAndTogglesCheckbox(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b", "c"}, true, ""))
	// initially all unchecked checkboxes
	if !strings.Contains(strip(f.view(30, true, "")), "󰄱") {
		t.Fatal("multi rows must show the unchecked checkbox 󰄱")
	}
	// space toggles the highlighted row → checked checkbox visible
	f2, _, _ := f.handle(tea.KeyPressMsg{Code: ' ', Text: " "})
	if !strings.Contains(strip(f2.view(30, true, "")), "󰄵") {
		t.Fatalf("after toggling, a checked checkbox 󰄵 must be visible: %q", strip(f2.view(30, true, "")))
	}
}

func TestChooseNoNumberShortcuts(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b", "c"}, false, ""))
	// pressing "2" must NOT select/jump (numbers are gone) — value stays unset, no fieldDone
	_, act, _ := f.handle(tea.KeyPressMsg{Code: '2', Text: "2"})
	if act == fieldDone {
		t.Fatal("number keys must no longer select")
	}
	// the rendered rows must not contain a digit prefix
	out := strip(f.view(30, true, ""))
	if strings.Contains(out, "1 ") || strings.Contains(out, "2 ") {
		t.Fatalf("rows must not show number prefixes: %q", out)
	}
}

func TestChooseHintNoNumberRange(t *testing.T) {
	h := strip(chooseHint(defaultTheme(), 3, false, ""))
	if strings.Contains(h, "1-3") || strings.Contains(h, "pick") {
		t.Fatalf("hint must not mention a number range/pick: %q", h)
	}
	if !strings.Contains(h, "move") || !strings.Contains(h, "󱊷") {
		t.Fatalf("hint must still show move + 󱊷 dismiss: %q", h)
	}
}

// Finding A: multi choose with --other; toggle an option, navigate onto the
// other row, type text, navigate UP off the other row, then submit.
// value() must include BOTH the toggled option AND the typed other text.
// filled() must be true throughout (the typed text is not silently dropped).
func TestChooseMultiOtherTextPreservedAfterNavAway(t *testing.T) {
	f := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, true, "Other…"))
	// Toggle "a" (highlight=0)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeySpace})
	// Navigate down twice to land on the other row (index 2)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyDown})
	// Type "xyz" into the other field
	for _, r := range "xyz" {
		f, _, _ = f.handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Navigate UP off the other row (now highlight=1, not the other row)
	f, _, _ = f.handle(tea.KeyPressMsg{Code: tea.KeyUp})
	// value() must include both "a" and "xyz"
	got := f.value()
	if !strings.Contains(got, "a") || !strings.Contains(got, "xyz") {
		t.Fatalf("value() must contain both toggled option and typed other text after nav away: %q", got)
	}
	if !f.filled() {
		t.Fatal("filled() must be true when other text was typed even after nav away")
	}
}
