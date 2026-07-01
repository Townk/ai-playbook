package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestQuitEventReturnsTeaQuit verifies that a streamEventsMsg carrying a
// quitEvent causes Update to return tea.Quit.
func TestQuitEventReturnsTeaQuit(t *testing.T) {
	m := newModel("T", "some content")
	m.width, m.height = 80, 24
	m.reader = strings.NewReader("") // prevent nil-reader panic on further reads
	m.parser = &streamParser{}

	_, cmd := m.Update(streamEventsMsg{events: []streamEvent{quitEvent{}}})
	if cmd == nil {
		t.Fatal("quitEvent must return a non-nil cmd")
	}
	// Execute the command and verify the message is tea.QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("quitEvent cmd must return tea.QuitMsg, got %T (%v)", msg, msg)
	}
}

// TestQuitEventMixedWithText verifies that a quitEvent takes priority even
// when there is also a textEvent in the same batch.
func TestQuitEventMixedWithText(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.reader = strings.NewReader("")
	m.parser = &streamParser{}

	events := []streamEvent{textEvent{"extra"}, quitEvent{}}
	_, cmd := m.Update(streamEventsMsg{events: events})
	if cmd == nil {
		t.Fatal("mixed-event batch with quitEvent must return a non-nil cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("mixed-event quitEvent cmd must return tea.QuitMsg, got %T", msg)
	}
}

// TestRegenerateButtonAbsentWhenNoPathWired verifies the defense gate: a cached
// result with NO regenerate path wired (no orchestrator re-engagement, no
// answerRegen seam) is NOT clickable — canRegenerate is false, so
// appendCachedButton adds no button and the dead reload is never shown. This is the
// fix for the pre-seam answer pane, whose reload no-op'd.
func TestRegenerateButtonAbsentWhenNoPathWired(t *testing.T) {
	m := newModel("T", "# Previous result\n")
	m.width, m.height = 80, 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	// no orch, no answerRegen → nothing wired
	m.reflow()

	if m.canRegenerate() {
		t.Fatal("canRegenerate must be false with no regenerate path wired")
	}
	for _, b := range m.buttons {
		if b.Kind == "regenerate" {
			t.Fatalf("no regenerate button must be registered when nothing is wired, got %+v", b)
		}
	}
}

// A create-style viewer marks its already-rendered playbook a final draft
// (finalDraft=true, not yet committed). Pressing `w` must PERSIST it via
// CommitPlaybook — NOT re-run the final-playbook generation pass (which would reset
// m.md and stream a second playbook in, producing a duplicate H1 + the spurious
// "ai-playbook — agent" header the create flow regressed on).
// reauthored=true simulates a draft that was produced by beginFinalPlaybookGenerate:
// the confirm gate is skipped, and wFinalize delegates directly to commitPlaybookCmd.
func TestWrapUpPersistsFinalDraftNotGenerate(t *testing.T) {
	m, _ := newReengageModel(t, "SHOULD-NOT-BE-GENERATED")
	m.width = 100
	m.md = "# Restore the wrapper\n\n```bash {id=fix}\necho hi\n```\n"
	m.finalDraft = true
	m.committed = false
	m.reauthored = true // the draft was generated (not an unrun proposal)
	m.reflow()
	before := m.md
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	got := nm.(model)
	if got.md != before {
		t.Fatalf("persist must not reset md (generation would); md = %q", got.md)
	}
	if got.streaming {
		t.Fatal("persist must not start streaming (generation would)")
	}
	if cmd == nil {
		t.Fatal("w persist should return the commit cmd")
	}
}

// With no orchestrator wired, `w` + hadFollowup=true reaches saveDecision → the
// re-author path, which returns nil (beginFinalPlaybookGenerate early-return). The
// verify block is marked ok so wFinalize skips the confirm gate and reaches saveDecision.
func TestWReauthorNoOrchIsNoOp(t *testing.T) {
	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = false
	// verify=ok so wFinalize passes the gate; hadFollowup=true → saveDecision takes
	// the re-author path, which returns nil when no orch is wired.
	m.blockStates = map[string]blockRunState{"verify": {Status: "ok"}}
	m.hadFollowup = true
	m.reflow()

	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	m3 := m2.(model)

	if m3.thinking {
		t.Error("w key with no in-process reengage must not start a thinking session")
	}
	if cmd != nil {
		t.Errorf("w key with no in-process reengage must return nil cmd, got %T", cmd)
	}
}

// TestWrapUpKeyWhileStreaming verifies that pressing "w" while streaming is a no-op:
// no wrapup action is emitted and state is unchanged.
func TestWrapUpKeyWhileStreaming(t *testing.T) {
	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = true // already streaming
	m.reflow()

	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	m3 := m2.(model)

	// State must remain unchanged (streaming stays true, no new thinking session started).
	if !m3.streaming {
		t.Error("streaming state must not be cleared by w key during streaming")
	}
	if cmd != nil {
		t.Errorf("w key while streaming must return nil cmd, got %T", cmd)
	}
}

// TestWrapUpKeyHintMode verifies that pressing "w" while in hint mode does not
// trigger a wrap-up (hint mode consumes the key for label resolution).
func TestWrapUpKeyHintMode(t *testing.T) {
	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = false
	m.hintMode = true
	m.hintLabels = map[string]Button{"w": {Kind: "toggle", BlockID: "x"}}
	m.reflow()

	// Must not panic and must not start a thinking session.
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if m2.(model).thinking {
		t.Error("w key in hint mode must not start a wrap-up thinking session")
	}
}

// TestStatusBarHasNoShortcutKeys verifies the status bar does NOT advertise the
// wrap-up key (shortcuts live in the ? modal, not the status bar).
func TestStatusBarHasNoShortcutKeys(t *testing.T) {
	m := newModel("T", "some content")
	m.width, m.height = 80, 24
	m.reflow()

	bar := m.statusBar()
	if strings.Contains(bar, "wrap") {
		t.Errorf("statusBar must NOT contain the wrap-up shortcut; got %q", bar)
	}
}

// TestHelpModalDocumentsWrapUp verifies the ? modal documents the w (finalize) key
// and includes a Buttons section.
func TestHelpModalDocumentsWrapUp(t *testing.T) {
	out := joinText(buildHelpLines())
	if !strings.Contains(out, "wrap-up work in the playbook") {
		t.Errorf("help modal must document the w (wrap-up) key; got:\n%s", out)
	}
	if !strings.Contains(out, "Buttons") {
		t.Errorf("help modal must have a Buttons section; got:\n%s", out)
	}
}

// TestNonVerifyFailureDoesNotAutoFire verifies a non-verify failed run result
// does NOT auto-emit a followup; the block instead renders a followup button.
// The manual followup affordance only exists in a re-engagement-capable session, so
// the model is wired with an orchestrator whose Reengage is set (canReengageInProc).
func TestNonVerifyFailureDoesNotAutoFire(t *testing.T) {
	m := newModel("T", "```bash {id=fix}\nmake build\n```\n")
	m.orch = orchWithReengage(t) // reengage available → the manual followup button renders
	m.width, m.height = 80, 24
	m.reflow()

	m2, cmd := m.Update(resultMsg{ID: "fix", Exit: 1, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd != nil {
		t.Errorf("non-verify failure must not auto-fire, got cmd %T", cmd)
	}
	if buttonForBlock(m3.buttons, "fix", "followup") == nil {
		t.Error("non-verify failed run block must render a followup button")
	}
}

// TestVerifySuccessNoFollowup verifies that a verify result with exit 0 does not
// fire a followup.
func TestVerifySuccessNoFollowup(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.reflow()

	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: "/tmp/x.log"})
	if cmd != nil {
		t.Errorf("verify success must not fire a followup, got cmd %T", cmd)
	}
}

// TestStopClickSetsStopped verifies that clicking the stop button on a running
// block marks that block Stopped (a pager-local state change; with no orchestrator
// wired the action itself is a no-op).
func TestStopClickSetsStopped(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	// Mark the block running so the stop button renders, then reflow to register it.
	m.blockStates["verify"] = blockRunState{Status: "running"}
	m.reflow()

	b := buttonForBlock(m.buttons, "verify", "stop")
	if b == nil {
		t.Fatal("stop button must be present on a running block")
	}

	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	m3 := m2.(model)

	if !m3.blockStates["verify"].Stopped {
		t.Error("clicking stop must mark the block Stopped")
	}
}

// TestStoppedResultYieldsStoppedStatusNoFollowup verifies that a result arriving
// for a Stopped block resolves to Status "stopped", clears the Stopped flag, and
// never auto-fires a followup — even for the verify block at a signal exit (143).
func TestStoppedResultYieldsStoppedStatusNoFollowup(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.reflow()
	m.blockStates["verify"] = blockRunState{Status: "running", Stopped: true}

	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 143, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd != nil {
		t.Errorf("a stopped block result must not auto-fire a followup, got cmd %T", cmd)
	}
	if m3.blockStates["verify"].Status != "stopped" {
		t.Errorf("stopped block status = %q, want stopped", m3.blockStates["verify"].Status)
	}
	if m3.blockStates["verify"].Stopped {
		t.Error("the Stopped flag must be cleared once the result is consumed")
	}
	if m3.thinking {
		t.Error("a stopped block must not begin a followup stream (thinking)")
	}
}

// TestVerifySignalKilledDoesNotAutoFire verifies the belt-and-suspenders guard
// (in-process re-engagement wired): a verify result with a signal exit (>128, e.g.
// 143/SIGTERM) does NOT auto-fire even when the Stopped flag is absent; an ordinary
// exit 1 still DOES (regression).
func TestVerifySignalKilledDoesNotAutoFire(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised fix\n")
	m.md = "```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("test setup: expected in-process re-engagement to be available")
	}

	// exit 143, no Stopped flag: the exit>128 guard must suppress the auto-fire.
	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 143, Logpath: "/tmp/x.log"})
	if cmd != nil {
		t.Errorf("verify signal-kill (exit 143) must not auto-fire, got cmd %T", cmd)
	}

	// exit 1, fresh model: an ordinary non-zero exit still auto-fires.
	m2, _ := newReengageModel(t, "# Revised fix\n")
	m2.md = "```bash {id=verify}\nmake build\n```\n"
	m2.width, m2.height = 80, 24
	m2.reflow()
	_, cmd = m2.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd == nil {
		t.Error("verify ordinary failure (exit 1) must still auto-fire a followup")
	}
}

// TestVerifyExit127DoesNotAutoFire verifies that a verify result with exit 127
// ("command not found" — the verify command itself couldn't run) does NOT
// auto-fire a followup, while an ordinary exit 1 still DOES (regression). Driven
// with in-process re-engagement wired so the exit-1 control case genuinely fires.
func TestVerifyExit127DoesNotAutoFire(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised fix\n")
	m.md = "```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("test setup: expected in-process re-engagement to be available")
	}

	// exit 127: command not found — the exit==127 guard must suppress the auto-fire.
	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 127, Logpath: "/tmp/x.log"})
	m3 := m2.(model)
	if cmd != nil {
		t.Errorf("verify exit 127 (command not found) must not auto-fire, got cmd %T", cmd)
	}
	if m3.thinking {
		t.Error("verify exit 127 must not begin a followup stream (thinking)")
	}
	// The block still resolves to failed so the manual "try another fix" button appears.
	if m3.blockStates["verify"].Status != "failed" {
		t.Errorf("verify exit 127 block status = %q, want failed", m3.blockStates["verify"].Status)
	}

	// exit 1, fresh model: an ordinary non-zero exit still auto-fires (regression).
	mf, _ := newReengageModel(t, "# Revised fix\n")
	mf.md = "```bash {id=verify}\nmake build\n```\n"
	mf.width, mf.height = 80, 24
	mf.reflow()
	_, cmd = mf.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd == nil {
		t.Error("verify ordinary failure (exit 1) must still auto-fire a followup")
	}
}

// TestStartTickSingleLoop verifies the spinner tick guard: once a tick loop is
// live (startTick returned a cmd and set tickRunning), a second startTick returns
// nil so overlapping loops never stack; and the spinTickMsg stop-path (neither
// thinking nor any block running) clears tickRunning so a future startTick can
// arm a fresh loop.
func TestStartTickSingleLoop(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24

	// First startTick arms the single loop and returns a non-nil cmd.
	if cmd := m.startTick(); cmd == nil {
		t.Fatal("first startTick must return a non-nil tick cmd")
	}
	if !m.tickRunning {
		t.Fatal("startTick must set tickRunning=true")
	}
	// A second startTick while the loop is live must be a no-op (nil).
	if cmd := m.startTick(); cmd != nil {
		t.Errorf("second startTick with a live loop must return nil, got %T", cmd)
	}

	// spinTickMsg stop-path: neither thinking nor any block running -> clear tickRunning.
	m.thinking = false
	m2, cmd := m.Update(spinTickMsg{})
	m3 := m2.(model)
	if cmd != nil {
		t.Errorf("spinTickMsg stop-path must return nil cmd, got %T", cmd)
	}
	if m3.tickRunning {
		t.Error("spinTickMsg stop-path must clear tickRunning")
	}
	// After the stop-path cleared the flag, startTick can arm a fresh loop again.
	if cmd := m3.startTick(); cmd == nil {
		t.Error("startTick must arm a fresh loop after the stop-path cleared tickRunning")
	}
}

// TestStartTickContinuePathKeepsRunning verifies the spinTickMsg CONTINUE path
// (still thinking or a block running) re-issues a tick cmd and keeps tickRunning
// set — so two starts (e.g. a run click then an auto-followup) cannot stack loops.
func TestStartTickContinuePathKeepsRunning(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.startTick()     // arm the single loop
	m.thinking = true // still thinking -> CONTINUE path

	m2, cmd := m.Update(spinTickMsg{})
	m3 := m2.(model)
	if cmd == nil {
		t.Error("spinTickMsg continue-path (thinking) must re-issue the tick cmd")
	}
	if !m3.tickRunning {
		t.Error("spinTickMsg continue-path must keep tickRunning set")
	}
	// A startTick issued by another handler while the loop continues is a no-op.
	if c := m3.startTick(); c != nil {
		t.Errorf("startTick during a continuing loop must return nil, got %T", c)
	}
}

// TestStructuredStreamRendersBodyOnEOF verifies that in structured mode:
// (a) narration textEvents are drained (not accumulated as m.md), and
// (b) on stream EOF, m.md is set from bodyProvider() so the existing
// finalDraft processing runs on the captured rendered playbook.
func TestStructuredStreamRendersBodyOnEOF(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.streaming, m.thinking = true, true
	m.finalDraft = true
	m.structured = true
	m.bodyProvider = func() string { return "# Restore wrapper\n\n```bash {id=fix}\necho hi\n```\n" }
	// A narration textEvent must NOT become the playbook in structured mode.
	m1, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{text: "let me diagnose…"}}})
	m = m1.(model)
	if strings.Contains(m.md, "diagnose") {
		t.Fatalf("structured mode must drain narration, not accumulate it: md=%q", m.md)
	}
	// On EOF, m.md becomes the captured rendered playbook.
	m2, _ := m.Update(streamEventsMsg{eof: true})
	m = m2.(model)
	if !strings.Contains(m.md, "# Restore wrapper") || !strings.Contains(m.md, "{id=fix}") {
		t.Fatalf("structured EOF must render bodyProvider(): md=%q", m.md)
	}
}

func TestModelThinkingRendersWidget(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.thinking = true
	m.progress.SetActivity("diagnosing the failure")
	out := m.viewString()
	if !strings.Contains(out, "diagnosing the failure") {
		t.Fatalf("thinking block must render the ProgressWidget activity, got %q", out)
	}
}

func TestHelpModal_BlueBorder(t *testing.T) {
	m := newModel("T", "hi")
	m.width, m.height = 80, 24
	v := m.helpModal()
	// colBlue = #89b4fa = R137 G180 B250; lipgloss emits it as \x1b[38;2;137;180;250m
	if !strings.Contains(v, "#89b4fa") && !strings.Contains(v, "137;180;250") {
		t.Fatalf("help modal border not blue:\n%q", v)
	}
}
