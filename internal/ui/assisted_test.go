package ui

import (
	"github.com/Townk/ai-playbook/internal/autorun"
	"testing"
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
