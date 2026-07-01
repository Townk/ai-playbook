package ui

import (
	"strings"
	"testing"
)

// TestBlockVisualNumbers verifies actionable blocks get sequential circled numbers while
// static blocks are skipped (so the count doesn't advance on them).
func TestBlockVisualNumbers(t *testing.T) {
	md := "```bash {id=a}\ntrue\n```\n\n" +
		"```text {static}\nreference\n```\n\n" +
		"```bash {id=b}\ntrue\n```\n"
	lines, _, _ := Render(md, 60, nil, "")
	txt := joinText(lines)
	if !strings.Contains(txt, "1") {
		t.Error("first actionable block should be numbered 1")
	}
	if !strings.Contains(txt, "2") {
		t.Error("second actionable block should be numbered 2 (static block skipped)")
	}
	if strings.Contains(txt, "3") {
		t.Error("a static block must not consume a number (no 3 expected)")
	}
}

// TestRollbackIndicatorOnBottomBorder verifies a rollback-target block shows
// "rollback ⟨N⟩" (N = the forward block's number) on its bottom border, and no number of
// its own.
func TestRollbackIndicatorOnBottomBorder(t *testing.T) {
	md := "```bash {id=stage rollback=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=undo-stage}\ntrue\n```\n"
	lines, _, _ := Render(md, 60, nil, "")
	txt := joinText(lines)
	if !strings.Contains(txt, "rollback (1)") {
		t.Errorf("undo-stage's bottom border should read 'rollback (1)' (stage is 1); got:\n%s", txt)
	}
	// stage is 1; undo-stage must NOT get its own top-left number (no 2).
	if strings.Contains(txt, "2") {
		t.Error("a rollback-target block must not receive its own visual number")
	}
}

// TestRollbackAttrParsed verifies rollback=<id> parses into Block.Rollback.
func TestRollbackAttrParsed(t *testing.T) {
	md := "```bash {id=stage rollback=undo-stage}\ntouch x\n```\n"
	_, _, blocks := Render(md, 80, nil, "")
	var got string
	for _, b := range blocks {
		if b.ID == "stage" {
			got = b.Rollback
		}
	}
	if got != "undo-stage" {
		t.Fatalf("rollback= must parse into Block.Rollback; got %q", got)
	}
}

// TestRollbackTargetNotIndependentlyRunnable verifies a block referenced as a rollback=
// target has no run/play button of its own (it runs only via the Rollback chain), while
// its paired forward step still runs normally.
func TestRollbackTargetNotIndependentlyRunnable(t *testing.T) {
	md := "```bash {id=stage rollback=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=undo-stage}\ntrue\n```\n"
	// muxActive=true so the "no play" assertion on the rollback target is meaningful.
	_, buttons, _ := Render(md, 80, map[string]blockRunState{}, "", false, true, false, true)

	if buttonForBlock(buttons, "undo-stage", "run") != nil {
		t.Error("a rollback-target block must not have an independent run button")
	}
	if buttonForBlock(buttons, "undo-stage", "play") != nil {
		t.Error("a rollback-target block must not have a play button")
	}
	if buttonForBlock(buttons, "stage", "run") == nil {
		t.Error("the forward step (stage) must still have its run button")
	}
}

// TestAutoRollback_FiresOnFailure verifies that with --auto-rollback (m.autoRollback),
// a step failure auto-fires the rollback chain: the rolled-back origin is reset and its
// rollback target is marked running.
func TestAutoRollback_FiresOnFailure(t *testing.T) {
	body := "```bash {id=a rollback=undo-a}\ntrue\n```\n\n" +
		"```bash {id=undo-a}\ntrue\n```\n\n" +
		"```bash {id=boom needs=a}\nfalse\n```\n"
	m := newModel("T", body)
	m.width, m.height = 80, 24
	m.autoRollback = true
	m.reflow()
	m.blockStates["a"] = blockRunState{Status: "ok"} // a applied → rollbackable
	m.reflow()

	m2 := mustModel(m.Update(resultMsg{ID: "boom", Exit: 1, Logpath: "/tmp/x.log"}))

	if _, ok := m2.blockStates["a"]; ok {
		t.Error("auto-rollback must reset the rolled-back origin a")
	}
	if m2.blockStates["undo-a"].Status != "running" {
		t.Errorf("auto-rollback must mark undo-a running; got %q", m2.blockStates["undo-a"].Status)
	}
}

// TestAutoRollback_OffKeepsManualPath verifies that WITHOUT --auto-rollback, a step
// failure leaves the applied step in place (manual "Rollback playbook" button path).
func TestAutoRollback_OffKeepsManualPath(t *testing.T) {
	body := "```bash {id=a rollback=undo-a}\ntrue\n```\n\n" +
		"```bash {id=undo-a}\ntrue\n```\n\n" +
		"```bash {id=boom needs=a}\nfalse\n```\n"
	m := newModel("T", body)
	m.width, m.height = 80, 24
	m.autoRollback = false
	m.reflow()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.reflow()

	m2 := mustModel(m.Update(resultMsg{ID: "boom", Exit: 1, Logpath: "/tmp/x.log"}))

	if m2.blockStates["a"].Status != "ok" {
		t.Error("without --auto-rollback, the applied step a must stay applied")
	}
	if m2.blockStates["undo-a"].Status == "running" {
		t.Error("without --auto-rollback, no rollback target should run")
	}
	if m2.blockStates["boom"].Status != "failed" {
		t.Errorf("boom must be failed; got %q", m2.blockStates["boom"].Status)
	}
}

// TestRollbackButtonGating verifies the "Rollback playbook" button shows on a failed
// step only when rollback is available and re-engagement is not; re-engagement (an
// authoring session) takes precedence over rollback when both are possible.
func TestRollbackButtonGating(t *testing.T) {
	md := "```bash {id=stage rollback=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=undo-stage}\ntrue\n```\n\n" +
		"```bash {id=boom}\nfalse\n```\n"
	states := map[string]blockRunState{
		"stage": {Status: "ok"},
		"boom":  {Status: "failed", Exit: 1},
	}

	// run context (no reengage) + something rollbackable → rollback button shows.
	_, btns, _ := Render(md, 80, states, "", false, false, true)
	if buttonForBlock(btns, "boom", "rollback") == nil {
		t.Error("failed block must show a Rollback button when a run step is rollbackable")
	}

	// nothing rollbackable → no rollback button.
	_, btns2, _ := Render(md, 80, states, "", false, false, false)
	if buttonForBlock(btns2, "boom", "rollback") != nil {
		t.Error("no Rollback button when nothing is rollbackable")
	}

	// reengage available → followup wins, rollback suppressed.
	_, btns3, _ := Render(md, 80, states, "", false, true, true)
	if buttonForBlock(btns3, "boom", "followup") == nil {
		t.Error("re-engagement should take precedence (followup shows when both available)")
	}
	if buttonForBlock(btns3, "boom", "rollback") != nil {
		t.Error("rollback must not also show when followup is shown")
	}
}

// TestBeginRollbackResetsAndRuns verifies beginRollback resets the run steps that had a
// rollback= target and marks each target running (the actual undo runs via the returned
// cmd). No orchestrator is wired, so emitAction is a no-op — we assert the state model.
func TestBeginRollbackResetsAndRuns(t *testing.T) {
	body := "```bash {id=a rollback=undo-a}\ntrue\n```\n\n" +
		"```bash {id=undo-a}\ntrue\n```\n\n" +
		"```bash {id=b rollback=undo-b}\ntrue\n```\n\n" +
		"```bash {id=undo-b}\ntrue\n```\n"
	m := newModel("T", body)
	m.width, m.height = 80, 24
	m.reflow()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.blockStates["b"] = blockRunState{Status: "ok"}
	m.reflow()

	m2, _ := m.beginRollback()

	if _, ok := m2.blockStates["a"]; ok {
		t.Error("origin a must be reset (its forward effect is being undone)")
	}
	if _, ok := m2.blockStates["b"]; ok {
		t.Error("origin b must be reset")
	}
	if m2.blockStates["undo-a"].Status != "running" {
		t.Errorf("undo-a must be marked running; got %q", m2.blockStates["undo-a"].Status)
	}
	if m2.blockStates["undo-b"].Status != "running" {
		t.Errorf("undo-b must be marked running; got %q", m2.blockStates["undo-b"].Status)
	}
	if m2.blockStates["undo-a"].Action != "rollback" || m2.blockStates["undo-b"].Action != "rollback" {
		t.Error("rollback targets must carry Action=rollback")
	}
}
