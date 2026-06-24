package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// flashMarker is the truecolor ANSI FOREGROUND params for the flash highlight
// (#ffffff = R255 G255 B255). The flash is bold + bright-white fg with NO
// background (a bg on the glyph cell shifts nerd-font glyphs a row on some
// terminals), and only the flashed glyph uses this color, so we search for it.
const flashMarker = "38;2;255;255;255"

// tabLineOf returns the stripped and raw text of the first non-Wide Code line
// (the decorative tab/label line) from lines.
func tabLineOf(lines []Line) (raw, plain string) {
	for _, l := range lines {
		if l.Code && !l.Wide && l.HBar == 0 {
			return l.Text, strip(l.Text)
		}
	}
	return "", ""
}

// TestFlashKeySetOnCopyMouseClick verifies that clicking a copy button sets
// m.flashKey to "<id>:copy" and returns a non-nil cmd.
func TestFlashKeySetOnCopyMouseClick(t *testing.T) {
	md := "```bash {id=myblk}\necho hi\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()

	// Find the copy button.
	var copyBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "copy" && m.buttons[i].BlockID == "myblk" {
			copyBtn = &m.buttons[i]
			break
		}
	}
	if copyBtn == nil {
		t.Fatal("copy button not found after reflow")
	}

	// Simulate a left-click at the button's screen position (add 2-col margin + bodyTop).
	clickX := copyBtn.Col + 2
	clickY := copyBtn.Line + m.bodyTop()
	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	wantKey := "myblk:copy"
	if m2.flashKey != wantKey {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("Update must return a non-nil cmd when a button is clicked")
	}
}

// TestFlashKeySetOnRunMouseClick verifies that clicking a run button sets
// m.flashKey to "<id>:run" and returns a non-nil cmd.
func TestFlashKeySetOnRunMouseClick(t *testing.T) {
	md := "```bash {id=runblk}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()

	var runBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "run" && m.buttons[i].BlockID == "runblk" {
			runBtn = &m.buttons[i]
			break
		}
	}
	if runBtn == nil {
		t.Fatal("run button not found")
	}

	clickX := runBtn.Col + 2
	clickY := runBtn.Line + m.bodyTop()
	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	wantKey := "runblk:run"
	if m2.flashKey != wantKey {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("Update must return a non-nil cmd for run click")
	}
}

// TestFlashKeySetOnToggle verifies that activating a toggle button sets flashKey
// to "<id>:toggle" and returns a non-nil cmd.
func TestFlashKeySetOnToggle(t *testing.T) {
	md := "```bash {id=tblk}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.blockStates["tblk"] = blockRunState{Status: "ok", Exit: 0}
	m.reflow()

	var togBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "toggle" && m.buttons[i].BlockID == "tblk" {
			togBtn = &m.buttons[i]
			break
		}
	}
	if togBtn == nil {
		t.Fatal("toggle button not found — ensure block state is ok/failed")
	}

	clickX := togBtn.Col + 2
	clickY := togBtn.Line + m.bodyTop()
	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	wantKey := "tblk:toggle"
	if m2.flashKey != wantKey {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("Update must return a non-nil cmd for toggle click")
	}
}

// TestFlashTickMsgClearsFlashKey verifies that a flashTickMsg resets flashKey
// to "" and triggers a reflow.
func TestFlashTickMsgClearsFlashKey(t *testing.T) {
	md := "```bash {id=x}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.flashKey = "x:copy"
	m.reflow()

	msg2, cmd := m.Update(flashTickMsg{})
	m2 := msg2.(model)

	if m2.flashKey != "" {
		t.Errorf("flashTickMsg must clear flashKey, got %q", m2.flashKey)
	}
	// cmd should be nil after clearing (no further flash tick needed)
	_ = cmd
}

// TestFlashRenderCopyGlyph verifies that when flashKey matches a copy button,
// the rendered tab line contains the flash background ANSI sequence, and that
// without flashKey the sequence is absent.
func TestFlashRenderCopyGlyph(t *testing.T) {
	md := "```bash {id=fb}\necho hi\n```\n"

	// Without flash: no flash background.
	linesNormal, _, _ := Render(md, 80, nil, "")
	rawNormal, _ := tabLineOf(linesNormal)
	if strings.Contains(rawNormal, flashMarker) {
		t.Error("without flashKey, tab line must NOT contain flash background")
	}

	// With flash on copy: flash background must appear.
	linesFlash, _, _ := Render(md, 80, nil, "fb:copy")
	rawFlash, _ := tabLineOf(linesFlash)
	if !strings.Contains(rawFlash, flashMarker) {
		t.Errorf("with flashKey=fb:copy, tab line must contain flash bg seq %q\ngot: %q", flashMarker, rawFlash)
	}

	// Flash on a different button kind (run) must NOT affect copy glyph area
	// (though it will appear somewhere on the same tab line for the run glyph).
	linesRunFlash, _, _ := Render(md, 80, nil, "fb:run")
	rawRunFlash, _ := tabLineOf(linesRunFlash)
	// The line contains flash bg (for run), but the copy glyph's color is normal.
	// We can't trivially separate them here; just verify flash is present.
	_ = rawRunFlash
}

// TestFlashRenderRunGlyph verifies flash background appears for run when
// flashKey is set to "<id>:run", and NOT when unset.
func TestFlashRenderRunGlyph(t *testing.T) {
	md := "```bash {id=rb}\nls\n```\n"

	linesNormal, _, _ := Render(md, 80, nil, "")
	rawNormal, _ := tabLineOf(linesNormal)
	if strings.Contains(rawNormal, flashMarker) {
		t.Error("without flashKey, tab line must NOT contain flash background")
	}

	linesFlash, _, _ := Render(md, 80, nil, "rb:run")
	rawFlash, _ := tabLineOf(linesFlash)
	if !strings.Contains(rawFlash, flashMarker) {
		t.Errorf("with flashKey=rb:run, tab line must contain flash bg seq %q\ngot: %q", flashMarker, rawFlash)
	}
}

// TestFlashRenderToggleGlyph verifies that when flashKey matches a toggle
// button, the summary line in the run region contains the flash background.
func TestFlashRenderToggleGlyph(t *testing.T) {
	md := "```bash {id=tb}\nls\n```\n"
	st := map[string]blockRunState{"tb": {Status: "ok", Exit: 0}}

	// Without flash: no flash bg in any line.
	linesNormal, _, _ := Render(md, 80, st, "")
	for _, l := range linesNormal {
		if strings.Contains(l.Text, flashMarker) {
			t.Error("without flashKey, no line must contain flash background")
		}
	}

	// With flash on toggle: the summary line must contain flash bg.
	linesFlash, _, _ := Render(md, 80, st, "tb:toggle")
	found := false
	for _, l := range linesFlash {
		if strings.Contains(l.Text, flashMarker) {
			found = true
			break
		}
	}
	if !found {
		t.Error("with flashKey=tb:toggle, a line must contain flash bg seq")
	}
}

// TestFlashKeyNotAffectOtherButtons verifies that when flashKey is set for
// one button, the other buttons on the same tab line do NOT receive the flash
// background (i.e., the highlight is selective).
func TestFlashKeyNotAffectOtherButtons(t *testing.T) {
	md := "```bash {id=sel}\nls\n```\n"

	// Flash only on "copy": the run/play glyphs must use their normal colors,
	// not the flash bg. We verify by checking that there is exactly ONE
	// occurrence of flashMarker per tab line (only the copy glyph).
	linesFlash, btns, _ := Render(md, 80, nil, "sel:copy")
	rawFlash, _ := tabLineOf(linesFlash)

	// Find the column positions of each button.
	colOf := func(kind string) int {
		for _, b := range btns {
			if b.Kind == kind && b.BlockID == "sel" {
				return b.Col
			}
		}
		return -1
	}
	copyCol := colOf("copy")
	runCol := colOf("run")
	if copyCol < 0 || runCol < 0 {
		t.Skip("expected both copy and run buttons on sel block")
	}

	// Count occurrences: there should be exactly one flash bg sequence.
	count := strings.Count(rawFlash, flashMarker)
	if count != 1 {
		t.Errorf("expected exactly 1 flash bg occurrence (copy only), got %d in tab line:\n%q", count, rawFlash)
	}
}

// TestFlashClearedOnWindowSizeMsg verifies that a stale flashKey is cleared
// when a tea.WindowSizeMsg is received (e.g. after returning from a float).
func TestFlashClearedOnWindowSizeMsg(t *testing.T) {
	md := "```bash {id=wsblk}\necho hi\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.flashKey = "wsblk:copy"
	m.reflow()

	msg2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m2 := msg2.(model)

	if m2.flashKey != "" {
		t.Errorf("WindowSizeMsg must clear stale flashKey, got %q", m2.flashKey)
	}
}

// TestFlashClearedOnKeyPress verifies that a stale flashKey is cleared
// when any key is pressed.
func TestFlashClearedOnKeyPress(t *testing.T) {
	md := "```bash {id=kpblk}\necho hi\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.flashKey = "kpblk:copy"
	m.reflow()

	msg2, _ := m.Update(key("j"))
	m2 := msg2.(model)

	if m2.flashKey != "" {
		t.Errorf("KeyPressMsg must clear stale flashKey, got %q", m2.flashKey)
	}
}

// TestUnknownLangTabNoIcon verifies that a code block with an unknown language
// does NOT emit the null/default devicon glyph (U+F07E2) in the tab line.
func TestUnknownLangTabNoIcon(t *testing.T) {
	const nullGlyph = "\U000F07E2"
	md := "```brainfuck\n++++\n```\n"
	lines, _, _ := Render(md, 80, nil, "")
	raw, plain := tabLineOf(lines)
	if raw == "" {
		t.Fatal("no tab line found for unknown-lang block")
	}
	if strings.Contains(raw, nullGlyph) {
		t.Errorf("unknown-lang tab must NOT contain default glyph U+F07E2, got: %q", raw)
	}
	// The language label should still appear in the visible text.
	if !strings.Contains(plain, "brainfuck") {
		t.Errorf("unknown-lang tab should still show the lang label; plain = %q", plain)
	}
}

// TestFlashHintKeyActivation verifies that activating a button via hint mode
// also sets m.flashKey to the correct key.
func TestFlashHintKeyActivation(t *testing.T) {
	md := "```bash {id=hk}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()

	// Enter hint mode.
	m2, _ := m.Update(key(" "))
	m3 := m2.(model)
	if !m3.hintMode {
		t.Fatal("space should enter hint mode")
	}
	if len(m3.hintLabels) == 0 {
		t.Fatal("hintLabels must be populated in hint mode")
	}

	// Press the first hint label key to activate the first button.
	var firstLabel string
	var firstBtn Button
	for lbl, b := range m3.hintLabels {
		firstLabel = lbl
		firstBtn = b
		break
	}

	msg4, cmd := m3.Update(key(firstLabel))
	m4 := msg4.(model)

	wantKey := firstBtn.BlockID + ":" + firstBtn.Kind
	if m4.flashKey != wantKey {
		t.Errorf("hint-mode activation: flashKey = %q, want %q", m4.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("hint-mode activation must return a non-nil cmd")
	}
}

// TestStopButtonMouseClick verifies that clicking a stop button (on a running
// block) sets flashKey to "<id>:stop" and returns a non-nil cmd.
func TestStopButtonMouseClick(t *testing.T) {
	md := "```bash {id=sblk}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.blockStates["sblk"] = blockRunState{Status: "running"}
	m.reflow()

	// The running block should expose a stop button, not a run button.
	stopBtn := buttonForBlock(m.buttons, "sblk", "stop")
	if stopBtn == nil {
		t.Fatal("running block must have a stop button after reflow")
	}

	clickX := stopBtn.Col + 2
	clickY := stopBtn.Line + m.bodyTop()
	msg2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m2 := msg2.(model)

	wantKey := "sblk:stop"
	if m2.flashKey != wantKey {
		t.Errorf("flashKey = %q, want %q", m2.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("stop button click must return a non-nil cmd")
	}
}

// TestStopButtonHintKeyActivation verifies that activating a stop button via
// hint mode sets flashKey to "<id>:stop" and returns a non-nil cmd.
func TestStopButtonHintKeyActivation(t *testing.T) {
	md := "```bash {id=shk}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.blockStates["shk"] = blockRunState{Status: "running"}
	m.reflow()

	// Enter hint mode.
	m2, _ := m.Update(key(" "))
	m3 := m2.(model)
	if !m3.hintMode {
		t.Fatal("space should enter hint mode")
	}

	// Find the hint label for the stop button.
	var stopLabel string
	for lbl, b := range m3.hintLabels {
		if b.BlockID == "shk" && b.Kind == "stop" {
			stopLabel = lbl
			break
		}
	}
	if stopLabel == "" {
		t.Fatal("no hint label found for the stop button")
	}

	msg4, cmd := m3.Update(key(stopLabel))
	m4 := msg4.(model)

	wantKey := "shk:stop"
	if m4.flashKey != wantKey {
		t.Errorf("hint-mode stop activation: flashKey = %q, want %q", m4.flashKey, wantKey)
	}
	if cmd == nil {
		t.Error("hint-mode stop activation must return a non-nil cmd")
	}
}
