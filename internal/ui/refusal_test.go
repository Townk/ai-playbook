package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/reengage"
)

// Refuse-solution (spec §1/§2a): a submitted, non-empty refine note is recorded
// verbatim (trimmed) into m.refusals as a session constraint — recorded BEFORE the
// amend re-author fires — so the rejected approach cannot resurface in later
// re-engagements. The amend re-author still triggers, unchanged.
func TestFChangeSubmittedRecordsRefusal(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# amended\n", "# amended\n")
	m.md = "# current\n"
	m.streaming = false
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "  no docker  ", submitted: true})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("a submitted non-empty refine note must still trigger the amend re-author")
	}
	if len(m.refusals) != 1 || m.refusals[0] != "no docker" {
		t.Fatalf("refine note must be recorded trimmed into m.refusals, got %#v", m.refusals)
	}
}

// Refuse-solution (spec Testing): a cancelled refine (submitted=false) records nothing.
func TestFChangeCancelledRecordsNothing(t *testing.T) {
	m, _ := newReengageEventsModel(t, "x", "x")
	m.md = "# current\n"
	m.streaming = false
	m.reflow()

	nm, _ := m.Update(fChangeMsg{base: m.md, value: "no docker", submitted: false})
	m = nm.(model)
	if len(m.refusals) != 0 {
		t.Fatalf("a cancelled refine must record nothing, got %#v", m.refusals)
	}
}

// Refuse-solution (spec Testing): a whitespace-only submitted refine records nothing.
func TestFChangeWhitespaceRecordsNothing(t *testing.T) {
	m, _ := newReengageEventsModel(t, "x", "x")
	m.md = "# current\n"
	m.streaming = false
	m.reflow()

	nm, _ := m.Update(fChangeMsg{base: m.md, value: "   \n\t ", submitted: true})
	m = nm.(model)
	if len(m.refusals) != 0 {
		t.Fatalf("a whitespace-only refine must record nothing, got %#v", m.refusals)
	}
}

// Refuse-solution (spec §2, pinned): `r` while streaming is a no-op — it records
// nothing and issues no cmd (amends only apply to settled content).
func TestRefineWhileStreamingRecordsNothing(t *testing.T) {
	m, _ := newReengageEventsModel(t, "x", "x")
	m.md = "# current\n"
	m.streaming = true
	fk := &fakeAsker{value: "no docker", submitted: true}
	m.asker = fk.fn
	m.reflow()

	nm, cmd := m.Update(key("r"))
	m = nm.(model)
	if cmd != nil {
		t.Error("`r` while streaming must be a no-op (nil cmd)")
	}
	if fk.calls != 0 {
		t.Errorf("`r` while streaming must not call the asker, calls=%d", fk.calls)
	}
	if len(m.refusals) != 0 {
		t.Fatalf("`r` while streaming must record nothing, got %#v", m.refusals)
	}
}

// Refuse-solution (spec §1): the amend re-author threads the accumulated session
// constraints (including the just-recorded note) into FinalPlaybook.
func TestFinalPlaybookThreadsRefusals(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# amended\n", "# amended\n")
	m.md = "# current\n"
	m.streaming = false
	m.refusals = []string{"no docker"}
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "use podman", submitted: true})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("a submitted refine must trigger the amend re-author")
	}
	m = pumpReArm(t, m, cmd)
	if len(fe.gotConstraints) != 2 || fe.gotConstraints[0] != "no docker" || fe.gotConstraints[1] != "use podman" {
		t.Fatalf("FinalPlaybook must receive the session constraints incl. the new note, got %#v", fe.gotConstraints)
	}
}

// Refuse-solution (spec §1): a cache-bypassed regenerate threads the session constraints.
func TestRegenerateThreadsRefusals(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# regen\n", "# regen\n")
	m.refusals = []string{"no docker", "no kubernetes"}
	m.reflow()

	cmd := m.beginRegenerate()
	if cmd == nil {
		t.Fatal("beginRegenerate returned nil (re-engagement not wired?)")
	}
	m = pumpReArm(t, m, cmd)
	if len(fe.gotConstraints) != 2 || fe.gotConstraints[0] != "no docker" || fe.gotConstraints[1] != "no kubernetes" {
		t.Fatalf("Regenerate must receive the session constraints, got %#v", fe.gotConstraints)
	}
}

// Refuse-solution (spec §1): a followup re-engagement threads the session constraints.
func TestFollowupThreadsRefusals(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# fix\n", "# fix\n")
	m.refusals = []string{"no docker"}
	m.reflow()

	cmd := m.beginFollowupInProc("boom")
	if cmd == nil {
		t.Fatal("beginFollowupInProc returned nil (re-engagement not wired?)")
	}
	m = pumpReArm(t, m, cmd)
	if len(fe.gotConstraints) != 1 || fe.gotConstraints[0] != "no docker" {
		t.Fatalf("Followup must receive the session constraints, got %#v", fe.gotConstraints)
	}
}

// Refuse-solution (spec §1): the drift-regen re-engagement threads the session
// constraints through orchestrator.DriftRegen. Needs a real target file for the patch.
func TestDriftRegenThreadsRefusals(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Absolute paths in the patch so DriftTargetPath resolves directly to our temp file
	// (the shared driver's cwd is not this dir).
	stalePatch := fmt.Sprintf("--- %s\n+++ %s\n@@ -1 +1 @@\n-stale\n+fixed\n", target, target)
	fe := &fakeEventsProducer{final: fmt.Sprintf("--- %s\n+++ %s\n@@ -1 +1 @@\n-current\n+fixed\n", target, target)}
	m := newModel("agent", "old playbook content")
	m.width, m.height = 80, 24
	m.orch = orchestrator.New(sharedDriver, &cliMux{})
	m.reeng = reengage.New(&reengage.Reengage{
		Req:      capture.Request{ProjectRoot: dir},
		Events:   fe.fn,
		DataRoot: t.TempDir(),
	}, m.orch.DriftTargetPath)
	m.refusals = []string{"no docker"}

	cmd := m.driftRegenCmd("blk", stalePatch)
	if cmd == nil {
		t.Fatal("driftRegenCmd returned nil (re-engagement not wired?)")
	}
	_ = cmd() // run off the event loop; DriftRegen calls the fake EventsFunc synchronously
	if len(fe.gotConstraints) != 1 || fe.gotConstraints[0] != "no docker" {
		t.Fatalf("DriftRegen must receive the session constraints, got %#v", fe.gotConstraints)
	}
}

// Refuse-solution (spec §3): the status line surfaces a persistent `N constraint(s)`
// indicator while any constraints are active — pluralized, absent when there are none.
func TestStatusBarConstraintIndicator(t *testing.T) {
	m := newModel("T", "some content")
	m.width, m.height = 80, 24
	m.reflow()

	if strings.Contains(strip(m.statusBar()), "constraint") {
		t.Errorf("statusBar must NOT show a constraint indicator with none active; got %q", strip(m.statusBar()))
	}

	m.refusals = []string{"no docker"}
	if !strings.Contains(strip(m.statusBar()), "1 constraint") {
		t.Errorf("statusBar must show the singular indicator; got %q", strip(m.statusBar()))
	}

	m.refusals = []string{"no docker", "no kubernetes"}
	if !strings.Contains(strip(m.statusBar()), "2 constraints") {
		t.Errorf("statusBar must show the plural indicator; got %q", strip(m.statusBar()))
	}
}

// Refuse-solution (spec §3): recording a refine note sets the confirmation flash.
func TestFChangeSetsRecordedFlash(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# amended\n", "# amended\n")
	m.md = "# current\n"
	m.streaming = false
	m.reflow()

	nm, _ := m.Update(fChangeMsg{base: m.md, value: "no docker", submitted: true})
	m = nm.(model)
	if !strings.Contains(m.status, "noted") {
		t.Errorf("recording a refine note must set a confirmation flash, got status %q", m.status)
	}
}
