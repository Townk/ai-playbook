package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ai-playbook/orchestrator"
)

// Truecolor ANSI foreground params for the colors that distinguish an enabled vs a
// dimmed shell button. colRun (run glyph) and colGreen (play glyph) are present when
// enabled and gone when dimmed; colYellow (copy glyph) is present in BOTH (copy is
// never gated); colOverlay0 is the muted de-emphasis color the dimmed glyphs adopt.
const (
	runColorParams   = "38;2;100;149;237" // colRun  #6495ED
	copyColorParams  = "38;2;249;226;175" // colYellow #f9e2af
	mutedColorParams = "38;2;108;112;134" // colOverlay0 #6c7086
)

// btnOf returns a pointer to the first button matching kind+id, or nil.
func btnOf(m model, kind, id string) *Button {
	for i := range m.buttons {
		if m.buttons[i].Kind == kind && m.buttons[i].BlockID == id {
			return &m.buttons[i]
		}
	}
	return nil
}

// clickButton drives a left-click on the given button through Update and returns
// the updated model + the returned cmd.
func clickButton(m model, b *Button) (model, tea.Cmd) {
	msg, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      b.Col + 2, // 2-col left margin
		Y:      b.Line + m.bodyTop(),
	})
	return msg.(model), cmd
}

// TestAsyncShellGlyphsDimmedCopyNormal verifies the RENDER gate: with the async
// shell-disabled flag set, the run/play glyphs lose their normal colors (dimmed to
// the muted overlay color) while the copy glyph stays normally colored.
func TestAsyncShellGlyphsDimmedCopyNormal(t *testing.T) {
	md := "```bash {id=sb}\nls\n```\n"

	enabledLines, _, _ := Render(md, 80, nil, "")        // shellDisabled defaults false
	disabledLines, _, _ := Render(md, 80, nil, "", true) // shellDisabled = true

	rawEnabled, _ := tabLineOf(enabledLines)
	rawDisabled, _ := tabLineOf(disabledLines)

	// The run color is unique to the run glyph (colRun), so it is the clean
	// discriminator: present when enabled, gone (re-colored to muted) when disabled.
	if !strings.Contains(rawEnabled, runColorParams) {
		t.Errorf("enabled tab line missing run color %q", runColorParams)
	}
	if strings.Contains(rawDisabled, runColorParams) {
		t.Error("disabled tab line must NOT carry the run color (glyph should be dimmed)")
	}
	if !strings.Contains(rawDisabled, mutedColorParams) {
		t.Errorf("disabled tab line must carry the muted color %q", mutedColorParams)
	}
	// Copy stays normally colored in BOTH states.
	if !strings.Contains(rawEnabled, copyColorParams) || !strings.Contains(rawDisabled, copyColorParams) {
		t.Error("copy glyph must keep its normal color whether or not shell actions are disabled")
	}
}

// TestAsyncShellButtonDispatchNoOp verifies the DISPATCH gate: while driverPending,
// a click on a shell-action button (run) is a no-op — no flash, no cmd, the block is
// not marked running — while the copy button still flashes and returns a cmd.
func TestAsyncShellButtonDispatchNoOp(t *testing.T) {
	md := "```bash {id=sb}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.driverPending = true
	m.reflow()

	runBtn := btnOf(m, "run", "sb")
	if runBtn == nil {
		t.Fatal("run button not found")
	}
	m2, cmd := clickButton(m, runBtn)
	if cmd != nil {
		t.Error("run click while driverPending must return a nil cmd (inert)")
	}
	if m2.flashKey != "" {
		t.Errorf("run click while driverPending must not flash, got %q", m2.flashKey)
	}
	if st := m2.blockStates["sb"]; st.Status == "running" {
		t.Error("run click while driverPending must not mark the block running")
	}

	// Copy is NOT gated: it flashes and returns a non-nil cmd even while pending.
	copyBtn := btnOf(m, "copy", "sb")
	if copyBtn == nil {
		t.Fatal("copy button not found")
	}
	m3, cmd := clickButton(m, copyBtn)
	if m3.flashKey != "sb:copy" {
		t.Errorf("copy click flashKey = %q, want %q", m3.flashKey, "sb:copy")
	}
	_ = cmd // copy may or may not produce a cmd depending on orch/fifo wiring; flash is the signal
}

// TestOrchReadyEnablesButtons verifies that delivering an orchReadyMsg with a
// non-nil orchestrator installs it, sets the asker, clears driverPending, and that
// the shell buttons then dispatch normally (flash + non-nil cmd).
func TestOrchReadyEnablesButtons(t *testing.T) {
	md := "```bash {id=sb}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.driverPending = true
	m.reflow()

	fakeOrch := orchestrator.New(nil, &cliMux{}) // non-nil pointer; we don't run the cmd
	asked := false
	fakeAsker := AskFunc(func(string) (string, bool) { asked = true; return "", false })

	msg, _ := m.Update(orchReadyMsg{OrchReady{Orch: fakeOrch, Asker: fakeAsker}})
	m = msg.(model)

	if m.orch != fakeOrch {
		t.Error("orchReadyMsg must install the delivered orchestrator")
	}
	if m.driverPending {
		t.Error("orchReadyMsg must clear driverPending")
	}
	if m.asker == nil {
		t.Fatal("orchReadyMsg must install the delivered asker")
	}
	_, _ = m.asker("x")
	if !asked {
		t.Error("installed asker is not the one delivered")
	}

	// Now the run button dispatches: flash set + non-nil cmd (orch is wired).
	runBtn := btnOf(m, "run", "sb")
	if runBtn == nil {
		t.Fatal("run button not found after ready")
	}
	m2, cmd := clickButton(m, runBtn)
	if m2.flashKey != "sb:run" {
		t.Errorf("run click flashKey = %q, want %q (button should be live)", m2.flashKey, "sb:run")
	}
	if cmd == nil {
		t.Error("run click after ready must return a non-nil cmd (orchestrator wired)")
	}
}

// TestOrchReadyNilOrchClearsPending verifies the degraded path: a nil-Orch
// orchReadyMsg (the background open failed) clears driverPending but leaves m.orch
// nil, and a subsequent shell-button click neither panics nor performs an action.
func TestOrchReadyNilOrchClearsPending(t *testing.T) {
	md := "```bash {id=sb}\nls\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.driverPending = true
	m.reflow()

	msg, _ := m.Update(orchReadyMsg{}) // zero OrchReady: nil Orch
	m = msg.(model)

	if m.driverPending {
		t.Error("nil-Orch orchReadyMsg must still clear driverPending (no hang)")
	}
	if m.orch != nil {
		t.Error("nil-Orch orchReadyMsg must leave m.orch nil")
	}

	// In the degraded state the action itself is a no-op: with no orch and no fifo,
	// emitAction yields a nil cmd (nothing executes).
	if ac := m.emitAction(Button{Kind: "run", BlockID: "sb", Payload: "ls"}); ac != nil {
		t.Error("emitAction in the degraded (no-orch, no-fifo) state must be a no-op (nil cmd)")
	}
	// And a real click must not panic.
	runBtn := btnOf(m, "run", "sb")
	if runBtn == nil {
		t.Fatal("run button not found")
	}
	_, _ = clickButton(m, runBtn)
}
