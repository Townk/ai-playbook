package ui

import (
	"strings"
	"testing"

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
