package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/autorun"
)

func TestStartAssisted_SetsCursorToFirstRunnable(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	if m.readyID != "a" || m.assistedFooter != "step" {
		t.Fatalf("start: readyID=%q footer=%q, want a/step", m.readyID, m.assistedFooter)
	}
}

func TestAssisted_AdvanceOnOk_ThenDone(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "running", Action: "run"}
	m2 := mustModel(m.Update(resultMsg{ID: "a", Exit: 0}))
	if m2.readyID != "b" {
		t.Fatalf("after a ok, cursor should be b; got %q", m2.readyID)
	}
	m2.blockStates["b"] = blockRunState{Status: "running", Action: "run"}
	m3 := mustModel(m2.Update(resultMsg{ID: "b", Exit: 0}))
	if m3.assistedFooter != "done" || m3.readyID != "" {
		t.Fatalf("after b ok, should be done; got footer=%q ready=%q", m3.assistedFooter, m3.readyID)
	}
}

func TestAssisted_FailureRaisesFailureFooter(t *testing.T) {
	m := newModel("T", "```bash {id=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "running", Action: "run"}
	m2 := mustModel(m.Update(resultMsg{ID: "a", Exit: 1}))
	if m2.assistedFooter != "failure" || m2.assistedFailedID != "a" || m2.exitCode != 1 {
		t.Fatalf("failure: footer=%q failed=%q exit=%d", m2.assistedFooter, m2.assistedFailedID, m2.exitCode)
	}
}

func TestAssisted_SkipMarksSkippedAndAdvances(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m2 := m.assistedSkip()
	if m2.blockStates["a"].Status != autorun.StatusSkipped {
		t.Error("a must be skipped")
	}
	if m2.readyID != "b" {
		t.Errorf("cursor should advance to b; got %q", m2.readyID)
	}
}

func TestAssistedFooter_StepButtons(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	out := strip(m.viewString())
	for _, w := range []string{"Run", "Skip", "Quit"} {
		if !strings.Contains(out, w) {
			t.Errorf("step footer missing %q:\n%s", w, out)
		}
	}
	// Screen buttons registered for click.
	if buttonForBlock(m.buttons, "assist", "assist-run") == nil {
		t.Error("no assist-run Screen button")
	}
}

func TestAssistedFooter_FailureButtons(t *testing.T) {
	m := newModel("T", "```bash {id=a rollback=undo-a}\ntrue\n```\n\n```bash {id=undo-a}\ntrue\n```\n\n```bash {id=boom needs=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.assistedFooter = "failure"
	m.assistedFailedID = "boom"
	m.reflow()
	out := strip(m.viewString())
	for _, w := range []string{"Roll back", "Leave as-is", "Quit"} {
		if !strings.Contains(out, w) {
			t.Errorf("failure footer missing %q:\n%s", w, out)
		}
	}
}

func TestAssisted_RunActivatesReadyBlock(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m2, _ := m.assistedActivate("assist-run")
	if m2.blockStates["a"].Status != "running" {
		t.Errorf("Run must mark ready block running; got %q", m2.blockStates["a"].Status)
	}
	if m2.assistedFooter != "" {
		t.Error("footer must hide while the step runs")
	}
}

func TestAssisted_RollbackResolvesFailure(t *testing.T) {
	m := newModel("T", "```bash {id=a rollback=undo-a}\ntrue\n```\n\n```bash {id=undo-a}\ntrue\n```\n\n```bash {id=boom needs=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.assistedFooter = "failure"
	m.assistedFailedID = "boom"
	m.exitCode = 1
	m2, _ := m.assistedActivate("assist-rollback")
	if m2.blockStates["a"].Status != "rolledback" {
		t.Errorf("rollback must fire (a→rolledback); got %q", m2.blockStates["a"].Status)
	}
	if m2.exitCode != 0 {
		t.Errorf("Roll back resolves the failure → exit 0; got %d", m2.exitCode)
	}
}

func TestAssisted_LeaveAsIsKeepsNonZeroExit(t *testing.T) {
	m := newModel("T", "```bash {id=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	m.assistedFooter = "failure"
	m.assistedFailedID = "a"
	m.exitCode = 1
	m2, _ := m.assistedActivate("assist-leave")
	if m2.exitCode != 1 {
		t.Errorf("Leave as-is keeps exit 1; got %d", m2.exitCode)
	}
}

// Regression (review finding): the assisted footer's keyboard gate must capture
// ONLY its own nav keys (left/h, right/l, tab, enter/space) and let everything
// else — most importantly ctrl+c — fall through to the global key handling.
// Previously an unconditional `return m, nil` after the footer's switch swallowed
// ctrl+c for as long as any footer (Run/Skip/Quit, Roll back/Leave/Quit) was on
// screen, making it impossible to abort an assisted session.
func TestAssisted_CtrlCWhileFooterAborts(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	if m.assistedFooter != "step" {
		t.Fatalf("setup: expected a step footer, got %q", m.assistedFooter)
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m2 := nm.(model)
	if m2.exitCode != 1 {
		t.Errorf("ctrl+c while the footer is active must set exitCode=1; got %d", m2.exitCode)
	}
	if cmd == nil {
		t.Fatal("ctrl+c while the footer is active must return a quit cmd, got nil (key was swallowed)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c while the footer is active must return tea.QuitMsg, got %T", cmd())
	}
}

// Regression (review finding): the doc must stay scrollable while an assisted
// footer is on screen ("the user can scroll freely to read the step before
// running it") — a scroll key must NOT be captured by the footer's keyboard gate.
func TestAssisted_ScrollKeyFallsThroughWhileFooter(t *testing.T) {
	var body strings.Builder
	body.WriteString("intro line\n\n")
	for i := 0; i < 60; i++ {
		body.WriteString("filler paragraph line to force scrolling\n\n")
	}
	body.WriteString("```bash {id=a}\ntrue\n```\n\n")
	for i := 0; i < 60; i++ {
		body.WriteString("more filler paragraph line after the block\n\n")
	}
	m := newModel("T", body.String())
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	if m.assistedFooter != "step" {
		t.Fatalf("setup: expected a step footer, got %q", m.assistedFooter)
	}
	before := m.yOff
	focusBefore := m.footerFocus
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m2 := nm.(model)
	if m2.yOff == before {
		t.Errorf("j while the footer is active must scroll (yOff unchanged at %d) — key was swallowed", before)
	}
	if m2.footerFocus != focusBefore {
		t.Errorf("j must not be treated as a footer-nav key; footerFocus changed %d -> %d", focusBefore, m2.footerFocus)
	}
}
