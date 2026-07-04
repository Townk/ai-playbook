package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/reengage"
)

// Stage 4 (spec §C): the `w`-generate (manual finalize on a transcript) also AMENDS
// when serving — base==servedBase, change==the transcript content.
func TestWGenerateAmendsServedPlaybook(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# amended\n", "# amended\n")
	served := "# Playbook — Served\n\nstep A\n"
	transcript := "# Troubleshoot transcript\n"
	m.servedBase = served
	m.md = transcript
	m.finalDraft = false
	m.committed = false
	m.hadFollowup = true // diverged run → re-author (amend) path
	m.reflow()
	// Mark the verify block as passed so `w` saves directly (no unverified-confirm overlay).
	m.blockStates["verify"] = blockRunState{Status: "ok"}

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("w on a transcript must trigger generation")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotBase != served {
		t.Errorf("w-generate while serving must AMEND (base==servedBase), got %q want %q", fe.gotBase, served)
	}
	if fe.gotChange != transcript {
		t.Errorf("w-generate must thread the transcript as change, got %q", fe.gotChange)
	}
}

// `w` on a diverged run (hadFollowup=true) re-authors the final-playbook draft
// (REPLACE) — the same generation the confirm Yes triggers for the diverged path.
func TestManualWGeneratesFinalPlaybookDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot content\n"
	m.hadFollowup = true // diverged run → re-author path
	m.reflow()
	// Mark the verify block as passed so `w` saves directly (no unverified-confirm overlay).
	m.blockStates["verify"] = blockRunState{Status: "ok"}

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("w must trigger the final-playbook generation")
	}
	if m.md != "" {
		t.Errorf("w must REPLACE (reset m.md), got %q", m.md)
	}
	if !m.finalDraft || m.committed {
		t.Errorf("w must mark a draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("w kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// Stage 4b (spec §D): `w` on a DIRTY final-playbook DRAFT (finalDraft && !committed)
// re-persists it — it calls orchestrator.CommitPlaybook (save + cache-replace) and on
// success the playbookCommittedMsg result flips committed=true and shows "✓ saved playbook
// → <path>". It does NOT re-generate (the producer is not called and the draft is preserved).
// reauthored=true simulates a draft produced by beginFinalPlaybookGenerate so wFinalize
// skips the confirm gate and proceeds directly to saveDecision.
func TestWCommitsExistingDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Playbook — My Setup\n\n```bash {id=verify}\nclean playbook\n```\n"
	m.finalDraft = true
	m.committed = false
	m.reauthored = true // produced by beginFinalPlaybookGenerate; skips the confirm gate
	m.reflow()

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("w on a dirty draft must return the commit cmd")
	}
	// committed flips on the RESULT (not optimistically at the trigger).
	if m.committed {
		t.Error("w must not flip committed at the trigger — it flips on the persist result")
	}
	// The draft must be PRESERVED (not REPLACE-reset like a generation) and not regenerated.
	if !strings.Contains(m.md, "My Setup") {
		t.Errorf("w-commit must preserve the draft, got %q", m.md)
	}
	if m.thinking {
		t.Error("w-commit must NOT start a generation (thinking) — it just persists")
	}
	// Run the commit cmd: it persists via CommitPlaybook and yields a playbookCommittedMsg.
	msg := cmd()
	pc, ok := msg.(playbookCommittedMsg)
	if !ok {
		t.Fatalf("commit cmd must yield a playbookCommittedMsg, got %T", msg)
	}
	if pc.err != nil {
		t.Fatalf("commit must succeed, got %v", pc.err)
	}
	// Apply the result: committed flips true and the saved status shows.
	nm2, _ := m.Update(pc)
	m = nm2.(model)
	if !m.committed {
		t.Error("the commit result must flip committed=true")
	}
	if !strings.Contains(m.status, "saved playbook") {
		t.Errorf("commit status = %q, want a 'saved playbook' confirmation", m.status)
	}
	// The producer (final-playbook generation) must NOT have been called.
	if fe.calls != 0 {
		t.Errorf("w-commit must not generate (producer calls = %d, want 0)", fe.calls)
	}
}

// Stage 4b (spec §D efficiency): `w` on an ALREADY-SAVED draft (finalDraft && committed)
// is a no-op — it does NOT call CommitPlaybook (no wasted metadata round-trip) and just
// confirms "✓ already saved".
func TestWAlreadySavedIsNoOp(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Playbook — My Setup\n\nclean playbook\n"
	m.finalDraft = true
	m.committed = true // already persisted (baseline or a prior w)
	m.reflow()

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd != nil {
		t.Fatalf("w on an already-saved draft must be a no-op (no commit cmd), got %T", cmd())
	}
	if !strings.Contains(m.status, "already saved") {
		t.Errorf("w on an already-saved draft must show 'already saved', got %q", m.status)
	}
	if fe.calls != 0 {
		t.Errorf("w no-op must not generate (producer calls = %d, want 0)", fe.calls)
	}
}

// `w` on a transcript when the run diverged (hadFollowup=true) GENERATES the
// final-playbook draft — re-authoring to fold in the resolution.
func TestWGeneratesOnTranscript(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot transcript\n"
	m.finalDraft = false
	m.committed = false
	m.hadFollowup = true // diverged run → re-author path
	m.reflow()
	// Mark the verify block as passed so `w` saves directly (no unverified-confirm overlay).
	m.blockStates["verify"] = blockRunState{Status: "ok"}

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("w on a transcript must trigger generation")
	}
	if m.md != "" {
		t.Errorf("w-generate must REPLACE (reset m.md), got %q", m.md)
	}
	if !m.finalDraft || m.committed {
		t.Errorf("w-generate must mark a draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}
	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 || fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("w-generate must call the final-playbook producer once, got calls=%d kind=%v", fe.calls, fe.gotKind)
	}
}

// An `f` AMEND → generate → stream-EOF does NOT auto-persist. EOF fires no commit
// and committed stays false (an unsaved tweak the `w`/quit-guard handle).
func TestFAmendDoesNotAutoPersistAtEOF(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — tweaked\nrevised\n", "# Playbook — tweaked\nrevised\n")
	// Start from a committed baseline so the amend is the thing under test:
	// it must make the doc dirty again.
	m.md = "# Playbook — fix\n\nbody\n"
	m.finalDraft = true
	m.committed = true
	m.reflow()

	// Drive the `f` amend directly via its message (the asker float is off in tests).
	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "add a cleanup step", submitted: true})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("a submitted f amend must trigger generation")
	}
	if m.committed {
		t.Error("the amend re-arm must reset committed=false (an unsaved tweak)")
	}

	m, eofCmd := pumpReArmEOFCmd(t, m, cmd)
	if eofCmd != nil {
		t.Fatalf("an f-amend stream-EOF must NOT auto-persist, got cmd %T", eofCmd())
	}
	if m.committed {
		t.Error("an f amend must leave committed=false after EOF (no auto-persist)")
	}
	if fe.calls != 1 || fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("f amend must call the final-playbook producer once, got calls=%d kind=%v", fe.calls, fe.gotKind)
	}
}

// Stage 4b (spec §D): the quit guard fires when finalDraft && !committed (a post-baseline
// `f` edit) and does NOT fire right after the auto-finish baseline (committed).
func TestQuitGuardAfterBaselineVsAmend(t *testing.T) {
	// After the baseline (committed): quit exits on the first press (no guard).
	committed := newModel("T", "# Playbook — draft\n")
	committed.width, committed.height = 80, 24
	committed.finalDraft = true
	committed.committed = true
	committed.reflow()
	_, cmd := committed.Update(key("q"))
	if cmd == nil {
		t.Fatal("quit on a committed baseline must exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("quit on a committed baseline must return tea.QuitMsg, got %T", cmd())
	}

	// After an `f` edit (dirty): quit warns instead of exiting.
	dirty := newModel("T", "# Playbook — tweaked\n")
	dirty.width, dirty.height = 80, 24
	dirty.finalDraft = true
	dirty.committed = false // an `f` amend made it dirty again
	dirty.reflow()
	nm, cmd2 := dirty.Update(key("q"))
	dirty = nm.(model)
	if cmd2 != nil {
		t.Fatalf("quit on a dirty post-amend draft must warn, not exit (cmd=%T)", cmd2())
	}
	if !dirty.quitGuard || !strings.Contains(dirty.status, "uncommitted") {
		t.Errorf("quit on a dirty draft must arm the guard + warn, got guard=%v status=%q", dirty.quitGuard, dirty.status)
	}
}

// Stage 3 (spec §E): quitting (q/esc/ctrl+c) with an UNCOMMITTED draft does NOT exit
// — it warns and requires a SECOND quit press to discard. A committed/absent draft
// quits normally on the first press.
func TestQuitGuardWithUncommittedDraft(t *testing.T) {
	m := newModel("T", "# Playbook — draft\n")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.committed = false
	m.reflow()

	// First quit: warns, no exit.
	nm, cmd := m.Update(key("q"))
	m = nm.(model)
	if cmd != nil {
		t.Fatalf("first quit with an uncommitted draft must NOT quit (cmd=%T)", cmd())
	}
	if !m.quitGuard {
		t.Error("first quit must arm the quit guard")
	}
	if !strings.Contains(m.status, "uncommitted") {
		t.Errorf("first quit must show the uncommitted warning, got %q", m.status)
	}

	// Second quit: exits.
	_, cmd2 := m.Update(key("q"))
	if cmd2 == nil {
		t.Fatal("second quit must exit")
	}
	if _, ok := cmd2().(tea.QuitMsg); !ok {
		t.Errorf("second quit must return tea.QuitMsg, got %T", cmd2())
	}
}

// Stage 3 (spec §E): a `w` commit between the two quit presses clears the guard, so
// a subsequent quit exits immediately (the draft is now persisted).
// reauthored=true simulates a draft produced by beginFinalPlaybookGenerate so wFinalize
// skips the confirm gate and proceeds directly to saveDecision.
func TestQuitGuardClearedByCommit(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Playbook — draft\n\n```bash {id=verify}\nbody\n```\n"
	m.finalDraft = true
	m.committed = false
	m.reauthored = true // produced by beginFinalPlaybookGenerate; skips the confirm gate
	m.reflow()

	// First quit arms the guard.
	nm, _ := m.Update(key("q"))
	m = nm.(model)
	if !m.quitGuard {
		t.Fatal("setup: first quit must arm the guard")
	}
	// `w` re-persists; the commit RESULT clears the guard + flips committed (spec §D).
	nm2, cmd := m.Update(key("w"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("w on a dirty draft must return the commit cmd")
	}
	pc, ok := cmd().(playbookCommittedMsg)
	if !ok {
		t.Fatalf("w-commit must yield a playbookCommittedMsg, got %T", cmd())
	}
	nm3, _ := m.Update(pc)
	m = nm3.(model)
	if m.quitGuard {
		t.Error("a w-commit must clear the quit guard")
	}
	if !m.committed {
		t.Error("w must commit the draft")
	}
	// A following quit exits immediately (committed → no guard).
	_, qcmd := m.Update(key("q"))
	if qcmd == nil {
		t.Fatal("quit after commit must exit")
	}
	if _, ok := qcmd().(tea.QuitMsg); !ok {
		t.Errorf("quit after commit must return tea.QuitMsg, got %T", qcmd())
	}
}

// Stage 3: quitting with NO draft (or a committed one) exits normally on the first press.
func TestQuitNormalWithoutDraft(t *testing.T) {
	m := newModel("T", "# some content\n")
	m.width, m.height = 80, 24
	m.reflow()
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("quit without a draft must exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("quit without a draft must return tea.QuitMsg, got %T", cmd())
	}

	// A committed draft also quits normally.
	m2 := newModel("T", "# Playbook — done\n")
	m2.width, m2.height = 80, 24
	m2.finalDraft = true
	m2.committed = true
	m2.reflow()
	_, cmd2 := m2.Update(key("q"))
	if cmd2 == nil {
		t.Fatal("quit with a committed draft must exit")
	}
	if _, ok := cmd2().(tea.QuitMsg); !ok {
		t.Errorf("quit with a committed draft must return tea.QuitMsg, got %T", cmd2())
	}
}

// Issue #3 (regression guard): a verify FAILURE still triggers the follow-up,
// unchanged by the verify-success wrap-up addition.
func TestVerifyFailStillTriggersFollowupNotWrapup(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.reflow()

	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: ""})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("verify failure must still auto-fire the follow-up")
	}
	if m.wrappedUp {
		t.Error("a verify FAILURE must not set wrappedUp")
	}
	if m.followups != 1 {
		t.Errorf("verify failure must increment followups, got %d", m.followups)
	}
}

// Refine (spec §D): pressing `r` with a (mux) asker wired issues a cmd that calls the
// asker; the resulting fChangeMsg carries the snapshotted pager content as the base
// and the typed value, and is submitted.
func TestRefineKeyIssuesAskCmd(t *testing.T) {
	m, _ := newReengageEventsModel(t, "", "")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.reflow()
	fk := &fakeAsker{value: "also configure the NDK", submitted: true}
	m.asker = fk.fn

	nm, cmd := m.Update(key("r"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("`r` with an asker must issue a cmd")
	}
	msg := cmd()
	fc, ok := msg.(fChangeMsg)
	if !ok {
		t.Fatalf("`r` cmd must yield an fChangeMsg, got %T", msg)
	}
	if fk.calls != 1 {
		t.Fatalf("asker calls = %d, want 1", fk.calls)
	}
	if fk.gotPrompt != "What should I change?" {
		t.Errorf("asker prompt = %q, want \"What should I change?\"", fk.gotPrompt)
	}
	if !fc.submitted {
		t.Error("fChangeMsg.submitted must reflect the asker's submit")
	}
	if fc.value != "also configure the NDK" {
		t.Errorf("fChangeMsg.value = %q, want the typed value", fc.value)
	}
	if fc.base != "# Playbook — current\n\nstep\n" {
		t.Errorf("fChangeMsg.base must snapshot the displayed content, got %q", fc.base)
	}
}

// Stage 5 (spec §D): a submitted, non-empty fChangeMsg triggers the AMEND generation —
// the producer is called with base==m.md (the snapshotted content) and change==the
// typed value, in REPLACE mode, marked a DRAFT (finalDraft set, committed false).
func TestFChangeSubmittedTriggersAmend(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — amended\n\n```bash {id=verify}\nbase + ndk\n```\n", "# Playbook — amended\n\n```bash {id=verify}\nbase + ndk\n```\n")
	base := "# Playbook — current\n\nstep\n"
	change := "also configure the NDK"
	m.md = base
	m.streaming = false
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: base, value: change, submitted: true})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("a submitted non-empty fChangeMsg must trigger the amend generation")
	}
	if !m.finalDraft || m.committed {
		t.Errorf("`f` amend must mark a draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}
	// REPLACE: the displayed content was reset on the trigger.
	if m.md != "" {
		t.Errorf("`f` amend must REPLACE (reset m.md), got %q", m.md)
	}

	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", fe.calls)
	}
	if fe.gotKind != reengage.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != base {
		t.Errorf("`f` amend base must be the displayed content, got %q want %q", fe.gotBase, base)
	}
	if fe.gotChange != change {
		t.Errorf("`f` amend change must be the typed value, got %q want %q", fe.gotChange, change)
	}
	if !strings.Contains(m.md, "base + ndk") {
		t.Errorf("the amended playbook must stream into m.md, got %q", m.md)
	}
}

// Stage 5 (spec §D): a cancelled `f` (submitted=false) is a no-op — no generation,
// no draft.
func TestFChangeCancelledIsNoOp(t *testing.T) {
	m, fe := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "ignored", submitted: false})
	m = nm.(model)
	if cmd != nil {
		t.Error("a cancelled `f` must not trigger a cmd")
	}
	if m.finalDraft {
		t.Error("a cancelled `f` must not mark a draft")
	}
	if fe.calls != 0 {
		t.Errorf("a cancelled `f` must not call the producer, calls=%d", fe.calls)
	}
}

// Stage 5 (spec §D): a submitted but EMPTY/whitespace `f` value is a no-op (nothing
// to amend with).
func TestFChangeEmptyValueIsNoOp(t *testing.T) {
	m, fe := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "   \n", submitted: true})
	m = nm.(model)
	if cmd != nil {
		t.Error("an empty submitted `f` must not trigger a cmd")
	}
	if m.finalDraft {
		t.Error("an empty submitted `f` must not mark a draft")
	}
	if fe.calls != 0 {
		t.Errorf("an empty submitted `f` must not call the producer, calls=%d", fe.calls)
	}
}

// Refine (spec §D): with no asker AND no overlay bridge, `r` is a no-op (a brief
// status, no cmd, no draft).
func TestRefineKeyNilAskerNoOp(t *testing.T) {
	m, fe := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.asker = nil
	m.askBridge = nil // no float asker AND no overlay bridge → refine is a genuine no-op
	m.reflow()

	nm, cmd := m.Update(key("r"))
	m = nm.(model)
	if cmd != nil {
		t.Error("`r` with no asker/bridge must be a no-op (nil cmd)")
	}
	if m.finalDraft {
		t.Error("`r` with no asker/bridge must not mark a draft")
	}
	if fe.calls != 0 {
		t.Errorf("`r` with no asker/bridge must not call the producer, calls=%d", fe.calls)
	}
}

// Refine (spec §D): `r` while streaming is a no-op — amends only apply to settled
// content (and must not be issued while a generation is in flight).
func TestRefineKeyWhileStreamingNoOp(t *testing.T) {
	m, _ := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = true
	fk := &fakeAsker{value: "x", submitted: true}
	m.asker = fk.fn
	m.reflow()

	nm, cmd := m.Update(key("r"))
	_ = nm.(model)
	if cmd != nil {
		t.Error("`r` while streaming must be a no-op (nil cmd)")
	}
	if fk.calls != 0 {
		t.Errorf("`r` while streaming must not call the asker, calls=%d", fk.calls)
	}
}

// Stage 5 (spec §E): an `f` draft sets finalDraft && !committed, so the existing
// uncommitted-draft quit guard covers it — the first quit warns, a `w` commit clears
// it. This verifies `f` drafts ride the same guard as confirm/`w` drafts.
func TestFDraftCoveredByQuitGuard(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# amended\n", "# amended\n\n```bash {id=verify}\nmake build\n```\n")
	base := "# Playbook — current\n\nstep\n"
	m.md = base
	m.streaming = false
	m.width, m.height = 80, 24
	m.reflow()

	nm, cmd := m.Update(fChangeMsg{base: base, value: "tweak it", submitted: true})
	m = nm.(model)
	m = pumpReArm(t, m, cmd)
	if !m.finalDraft || m.committed {
		t.Fatalf("setup: `f` must produce an uncommitted draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}

	// First quit press: the guard warns instead of quitting.
	nm2, qcmd := m.Update(key("q"))
	m = nm2.(model)
	if qcmd != nil {
		t.Error("first quit over an `f` draft must NOT quit (guard warns)")
	}
	if !m.quitGuard {
		t.Error("first quit over an `f` draft must arm the quit guard")
	}
}
func TestHadFollowup_SetByFollowup(t *testing.T) {
	m := &model{reeng: reengWithFake(t)} // an orch whose Reengage != nil so beginFollowupStream proceeds
	_ = m.beginFollowupStream("verify", "false")
	if !m.hadFollowup {
		t.Fatal("a follow-up must set hadFollowup")
	}
}
func TestSaveDecision_NoFollowupPersists(t *testing.T) {
	m := &model{hadFollowup: false, md: "# P\n\n```bash {id=fix}\ntrue\n```\n",
		reeng: reengWithFake(t)}
	cmd := m.saveDecision()
	if cmd == nil {
		t.Fatal("no-followup save must return the commit cmd")
	}
	// commit path must NOT start a generation (streaming stays false)
	if m.streaming {
		t.Fatal("no-followup save must NOT re-author (streaming must be false)")
	}
}
func TestSaveDecision_FollowupReauthors(t *testing.T) {
	m := &model{hadFollowup: true, reeng: reengWithFake(t)}
	_ = m.saveDecision()
	// beginFinalPlaybookGenerate resets hadFollowup so the re-authored doc is then final
	if m.hadFollowup {
		t.Fatal("re-author must reset hadFollowup")
	}
	// beginFinalPlaybookGenerate sets streaming=true to signal a generation is underway
	if !m.streaming {
		t.Fatal("re-author path must set streaming=true")
	}
}

// Task 6: `w` on a troubleshoot transcript without a verified run raises the
// "save unverified" confirm overlay instead of saving immediately.
func TestW_NotVerifiedRaisesConfirm(t *testing.T) {
	m := model{md: "# P\n\n```bash {id=verify}\ntrue\n```\n", reeng: reengWithFake(t)}
	m.width, m.height = 80, 24
	m.reflow()
	// blockStates is empty → not verified
	nm, _ := m.Update(key("w"))
	m = nm.(model)
	if !m.askMode {
		t.Fatal("w on an unverified run must raise the confirm overlay")
	}
}

// Task 6: `w` on a verified troubleshoot transcript saves directly (no overlay).
func TestW_VerifiedSavesDirectly(t *testing.T) {
	m := model{md: "# P\n\n```bash {id=verify}\ntrue\n```\n", reeng: reengWithFake(t)}
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{"verify": {Status: "ok"}}
	m.reflow()
	nm, _ := m.Update(key("w"))
	m = nm.(model)
	if m.askMode {
		t.Fatal("w on a verified run must NOT raise the confirm overlay")
	}
}

// Task 6: saveConfirmMsg{ok:true} drives saveDecision (a non-nil cmd is returned).
func TestW_SaveConfirmMsgOk(t *testing.T) {
	m := model{md: "# P\n", reeng: reengWithFake(t)}
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{}
	// hadFollowup=false → saveDecision takes the commit path (commitPlaybookCmd).
	_, cmd := m.Update(saveConfirmMsg{ok: true})
	if cmd == nil {
		t.Fatal("saveConfirmMsg{ok:true} must invoke saveDecision and return a non-nil cmd")
	}
}

// Task 6: saveConfirmMsg{ok:false} is a no-op (the user cancelled the confirm).
func TestW_SaveConfirmMsgCancel(t *testing.T) {
	m := model{md: "# P\n", reeng: reengWithFake(t)}
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{}
	nm, cmd := m.Update(saveConfirmMsg{ok: false})
	m = nm.(model)
	if cmd != nil {
		t.Fatalf("saveConfirmMsg{ok:false} must be a no-op, got %T", cmd)
	}
	if m.streaming {
		t.Error("cancelled save confirm must not start a stream")
	}
}

// B3: finalDraft with hadFollowup=true (diverged run) + verify ok → `w` → wFinalize
// passes the gate → saveDecision → beginFinalPlaybookInProc → re-authors (streaming
// path), NOT a plain commit. hadFollowup is reset by beginFinalPlaybookGenerate.
func TestW_DivergedReauthors(t *testing.T) {
	m, _ := newReengageModel(t, "# Re-authored\n\n```bash {id=fix}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.committed = false
	m.hadFollowup = true
	m.blockStates = map[string]blockRunState{"verify": {Status: "ok"}}
	m.reflow()

	nm, cmd := m.Update(key("w"))
	got := nm.(model)

	// wFinalize passes the gate (verify=ok) → saveDecision → re-author path.
	if got.hadFollowup {
		t.Error("beginFinalPlaybookGenerate must reset hadFollowup")
	}
	if !got.streaming {
		t.Error("re-author path must set streaming=true")
	}
	if cmd == nil {
		t.Fatal("re-author path must return a non-nil cmd")
	}
}

// B3: finalDraft with reauthored=false + verify NOT ok → `w` → wFinalize raises the
// "save unverified" confirm gate (askMode=true). The user must acknowledge before saving.
func TestW_UnrunProposalWarns(t *testing.T) {
	m, _ := newReengageModel(t, "")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.committed = false
	m.reauthored = false // an unrun proposal
	// blockStates empty → verify not ok
	m.blockStates = map[string]blockRunState{}
	m.reflow()

	nm, _ := m.Update(key("w"))
	got := nm.(model)

	if !got.askMode {
		t.Fatal("w on an unrun finalDraft proposal must raise the confirm overlay")
	}
}

// B3: finalDraft with reauthored=true + verify NOT ok → `w` → wFinalize skips the
// gate (reauthored=true) and goes straight to saveDecision without showing the confirm.
func TestW_ReauthoredNoWarn(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised\n\n```bash {id=fix}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.committed = false
	m.reauthored = true // produced by beginFinalPlaybookGenerate
	// blockStates empty → verify not ok; reauthored bypasses the gate
	m.blockStates = map[string]blockRunState{}
	m.reflow()

	nm, cmd := m.Update(key("w"))
	got := nm.(model)

	if got.askMode {
		t.Fatal("w on a reauthored finalDraft must NOT raise the confirm overlay")
	}
	if cmd == nil {
		t.Fatal("reauthored path must return a non-nil saveDecision cmd")
	}
}

// B3: finalDraft with hadFollowup=false + verify ok → `w` → wFinalize passes the gate
// → saveDecision → commit path (no re-author, no confirm), streaming stays false.
func TestW_CleanProposalVerifiedPersists(t *testing.T) {
	m, _ := newReengageModel(t, "")
	m.width, m.height = 80, 24
	m.finalDraft = true
	m.committed = false
	m.hadFollowup = false
	m.blockStates = map[string]blockRunState{"verify": {Status: "ok"}}
	m.md = "# P\n\n```bash {id=fix}\ntrue\n```\n"
	m.reflow()

	nm, cmd := m.Update(key("w"))
	got := nm.(model)

	if got.askMode {
		t.Fatal("verified finalDraft must NOT raise the confirm overlay")
	}
	if got.streaming {
		t.Fatal("no-followup commit path must NOT start streaming")
	}
	if cmd == nil {
		t.Fatal("commit path must return a non-nil cmd")
	}
}
