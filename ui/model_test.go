package ui

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
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

// TestRegenerateClickWithFifoPath verifies that clicking the regenerate button
// (when inputFifoPath is set) clears md, resets isCached, sets thinking=true,
// and returns a non-nil cmd batch.
func TestRegenerateClickWithFifoPath(t *testing.T) {
	m := newModel("T", "# Previous result\n")
	m.width, m.height = 80, 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	m.inputFifoPath = "/tmp/nonexistent-test-fifo-" + t.Name() // won't be opened until reArmReaderCmd runs
	m.reflow()

	// Find the regenerate button (screen-fixed).
	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("regenerate button not found after reflow with isCached=true")
	}

	// Simulate a mouse click at the pill's screen position.
	// Screen-fixed buttons: buttonAt uses absolute Y (no yOff adjustment).
	clickX := regenBtn.Col + 2
	clickY := regenBtn.Line
	m2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m3 := m2.(model)

	if m3.md != "" {
		t.Errorf("after regenerate click, md must be cleared; got %q", m3.md)
	}
	if m3.isCached {
		t.Error("after regenerate click, isCached must be false")
	}
	if !m3.thinking {
		t.Error("after regenerate click, thinking must be true")
	}
	if !m3.streaming {
		t.Error("after regenerate click, streaming must be true")
	}
	if m3.follow {
		t.Error("after regenerate click, follow must be false (fresh result starts at the top)")
	}
	if cmd == nil {
		t.Error("regenerate click with inputFifoPath set must return a non-nil cmd")
	}
}

// TestRegenerateClickNoFifoPath verifies that clicking the regenerate button
// when inputFifoPath is "" still flashes/emits and does NOT panic or clear md.
func TestRegenerateClickNoFifoPath(t *testing.T) {
	m := newModel("T", "# Previous result\n")
	m.width, m.height = 80, 24
	m.isCached = true
	m.cachedAt = time.Now().Add(-5 * time.Minute)
	m.inputFifoPath = "" // no fifo
	m.reflow()

	var regenBtn *Button
	for i := range m.buttons {
		if m.buttons[i].Kind == "regenerate" {
			regenBtn = &m.buttons[i]
			break
		}
	}
	if regenBtn == nil {
		t.Fatal("regenerate button not found")
	}

	clickX := regenBtn.Col + 2
	clickY := regenBtn.Line
	m2, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      clickX,
		Y:      clickY,
	})
	m3 := m2.(model)

	// Without fifo: md must NOT be cleared, isCached stays as-is (we didn't re-arm).
	// The flash must be set.
	if m3.flashKey != "cached:regenerate" {
		t.Errorf("flashKey = %q, want cached:regenerate", m3.flashKey)
	}
	// Must return a non-nil flashCmd.
	if cmd == nil {
		t.Error("regenerate click without fifo must still return a non-nil cmd (flash)")
	}
}

// TestReArmedMsgSuccess verifies that a reArmedMsg with err=nil sets m.reader
// and returns a non-nil cmd (the next readStream call).
func TestReArmedMsgSuccess(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.parser = &streamParser{}

	msg := reArmedMsg{reader: strings.NewReader("hello"), err: nil}
	m2, cmd := m.Update(msg)
	m3 := m2.(model)

	if m3.reader == nil {
		t.Error("reArmedMsg success must set m.reader")
	}
	if m3.parser == nil {
		t.Error("reArmedMsg success must set m.parser")
	}
	if cmd == nil {
		t.Error("reArmedMsg success must return a non-nil cmd (readStream)")
	}
}

// TestReArmedMsgError verifies that a reArmedMsg with err!=nil clears thinking
// and surfaces an error note in the rendered output.
func TestReArmedMsgError(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.thinking = true

	someErr := errors.New("open /tmp/test.fifo: no such file or directory")
	msg := reArmedMsg{reader: nil, err: someErr}
	m2, cmd := m.Update(msg)
	m3 := m2.(model)

	if m3.thinking {
		t.Error("reArmedMsg error must clear thinking")
	}
	if !strings.Contains(m3.md, "regenerate error") {
		t.Errorf("reArmedMsg error must surface error in md; got %q", m3.md)
	}
	if cmd != nil {
		t.Errorf("reArmedMsg error must return nil cmd, got %T", cmd)
	}
}

// Stage 2 (spec §E): the `w` key now MANUALLY FINALIZES by generating the final
// playbook draft — an IN-PROCESS-only action (it needs Reengage). With no
// orchestrator wired (FIFO/standalone mode), `w` is a no-op: the old FIFO `wrapup`
// emission is retired (the native confirm + FinalPlaybook replaces the agent-ask
// wrap-up). It must NOT write a FIFO record, must NOT start a thinking session, and
// must return a nil cmd.
func TestWrapUpKeyNoOrchIsNoOp(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = false
	m.fifoPath = fifo
	m.inputFifoPath = "" // no input fifo; no orch either → no in-process reengage
	m.reflow()

	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	m3 := m2.(model)

	// No FIFO record (the retired wrapup emission must not fire).
	if f, err := os.Open(fifo); err == nil {
		defer f.Close()
		b := make([]byte, 1)
		if n, _ := f.Read(b); n > 0 {
			t.Error("w key with no orchestrator must not emit any FIFO action (wrapup retired)")
		}
	}
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
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = true // already streaming
	m.fifoPath = fifo
	m.inputFifoPath = "/tmp/nonexistent-wrapup-fifo-streaming-" + t.Name()
	m.reflow()

	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	m3 := m2.(model)

	// No record should have been written to the fifo.
	f, err := os.Open(fifo)
	if err == nil {
		defer f.Close()
		// If the file was created by emitAction, it means we accidentally emitted.
		b := make([]byte, 1)
		n, _ := f.Read(b)
		if n > 0 {
			t.Error("w key while streaming must not emit any action")
		}
	}
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
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "# Playbook\n")
	m.width, m.height = 80, 24
	m.streaming = false
	m.hintMode = true
	m.hintLabels = map[string]Button{"w": {Kind: "toggle", BlockID: "x"}}
	m.fifoPath = fifo
	m.inputFifoPath = "/tmp/nonexistent-wrapup-fifo-hint-" + t.Name()
	m.reflow()

	m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})

	// No wrapup record should have been emitted.
	f, err := os.Open(fifo)
	if err == nil {
		defer f.Close()
		b := make([]byte, 1)
		n, _ := f.Read(b)
		if n > 0 {
			t.Error("w key in hint mode must not emit a wrapup action")
		}
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
	if !strings.Contains(out, "finalize") {
		t.Errorf("help modal must document the w (finalize) key; got:\n%s", out)
	}
	if !strings.Contains(out, "Buttons") {
		t.Errorf("help modal must have a Buttons section; got:\n%s", out)
	}
}

// readFirstAction reads the first framed action record from the actions fifo at
// path and returns its kind, blockID and payload.
func readFirstAction(t *testing.T, path string) (kind, blockID, payload string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open actions fifo: %v", err)
	}
	defer f.Close()
	rec, _ := bufio.NewReader(f).ReadString('\x1e')
	rec = strings.TrimSuffix(rec, "\x1e")
	parts := strings.SplitN(rec, "\x1f", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// TestVerifyFailureAutoFiresFollowup verifies that a resultMsg for block id
// "verify" with a non-zero exit (and inputFifoPath set) auto-emits a followup
// record, sets the block failed, sets thinking, appends the "---" separator,
// and returns a non-nil cmd.
func TestVerifyFailureAutoFiresFollowup(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "# Playbook\n\n```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo") // never opened (reArm runs in a cmd goroutine)
	m.reflow()                                         // populate m.blocks so blockCommand works

	originalMd := m.md

	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd == nil {
		t.Fatal("verify failure with inputFifoPath set must return a non-nil cmd")
	}
	kind, blockID, payload := readFirstAction(t, fifo)
	if kind != "followup" {
		t.Errorf("emitted kind = %q, want followup", kind)
	}
	if blockID != "verify" {
		t.Errorf("emitted blockID = %q, want verify", blockID)
	}
	if payload != "make build" {
		t.Errorf("emitted payload = %q, want the verify command %q", payload, "make build")
	}
	if m3.blockStates["verify"].Status != "failed" {
		t.Errorf("verify block status = %q, want failed", m3.blockStates["verify"].Status)
	}
	if !m3.thinking {
		t.Error("auto-fire must set thinking=true")
	}
	if !m3.streaming {
		t.Error("auto-fire must set streaming=true")
	}
	if !strings.Contains(m3.md, originalMd) {
		t.Error("auto-fire must keep prior md content")
	}
	if !strings.Contains(m3.md, "---") {
		t.Error("auto-fire must append the --- separator")
	}
}

// TestVerifyFailureRepeatsUntilCap verifies that successive verify failures
// auto-fire a follow-up on EACH failure (not just the first) until the attempt
// cap is reached; past the cap auto-firing stops and the verify block surfaces the
// manual "try another fix" button. This is the issue-#3 repeat-until-success
// behavior; it replaces the old once-only guard.
func TestVerifyFailureRepeatsUntilCap(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.maxFollowups = 2 // small cap so the test is quick + explicit
	m.reflow()

	// First verify failure: auto-fires (attempt 1).
	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m2.(model)
	if cmd == nil {
		t.Fatal("first verify failure must auto-fire")
	}
	if m.followups != 1 {
		t.Fatalf("followups after first = %d, want 1", m.followups)
	}

	// Second verify failure (re-armed playbook's verify also fails): auto-fires
	// again (attempt 2) — the old once-only guard would have suppressed this.
	m3, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m3.(model)
	if cmd2 == nil {
		t.Fatal("second verify failure must ALSO auto-fire (repeat-until-success)")
	}
	if m.followups != 2 {
		t.Fatalf("followups after second = %d, want 2", m.followups)
	}

	// Third verify failure: cap reached → does NOT auto-fire; the verify block now
	// shows the manual "try another fix" button.
	m4, cmd3 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m4.(model)
	if cmd3 != nil {
		t.Errorf("at the cap, verify failure must NOT auto-fire, got %T", cmd3)
	}
	if !m.blockStates["verify"].FollowupExhausted {
		t.Error("at the cap, the verify block must be marked FollowupExhausted")
	}
	var hasManual bool
	for _, b := range m.buttons {
		if b.BlockID == "verify" && b.Kind == "followup" {
			hasManual = true
		}
	}
	if !hasManual {
		t.Error("at the cap, the verify block must show the manual 'try another fix' button")
	}
}

// TestNonVerifyFailureDoesNotAutoFire verifies a non-verify failed run result
// does NOT auto-emit a followup; the block instead renders a followup button.
func TestNonVerifyFailureDoesNotAutoFire(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=fix}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()

	m2, cmd := m.Update(resultMsg{ID: "fix", Exit: 1, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd != nil {
		t.Errorf("non-verify failure must not auto-fire, got cmd %T", cmd)
	}
	if _, err := os.Stat(fifo); err == nil {
		t.Error("non-verify failure must not emit any action")
	}
	if buttonForBlock(m3.buttons, "fix", "followup") == nil {
		t.Error("non-verify failed run block must render a followup button")
	}
}

// TestVerifySuccessNoFollowup verifies that a verify result with exit 0 does not
// fire a followup.
func TestVerifySuccessNoFollowup(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()

	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: "/tmp/x.log"})
	if cmd != nil {
		t.Errorf("verify success must not fire a followup, got cmd %T", cmd)
	}
	if _, err := os.Stat(fifo); err == nil {
		t.Error("verify success must not emit any action")
	}
}

// TestFollowupButtonClickStartsStream verifies that clicking the followup button
// emits a followup record and starts the append + re-arm stream.
func TestFollowupButtonClickStartsStream(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=fix}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	// Mark fix as failed so the followup button renders, then reflow to register it.
	m.blockStates["fix"] = blockRunState{Status: "failed", Exit: 1}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "followup")
	if b == nil {
		t.Fatal("followup button must be present on the failed fix block")
	}

	originalMd := m.md
	// Click at the button's screen position.
	x := b.Col + 2 // +2 for the left margin buttonAt strips
	y := m.bodyTop() + (b.Line - m.yOff)
	m2, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	m3 := m2.(model)

	if cmd == nil {
		t.Fatal("clicking followup must return a non-nil cmd")
	}
	kind, blockID, payload := readFirstAction(t, fifo)
	if kind != "followup" {
		t.Errorf("emitted kind = %q, want followup", kind)
	}
	if blockID != "fix" {
		t.Errorf("emitted blockID = %q, want fix", blockID)
	}
	if payload != "make build" {
		t.Errorf("emitted payload = %q, want %q", payload, "make build")
	}
	if !m3.thinking {
		t.Error("clicking followup must set thinking=true")
	}
	if !strings.Contains(m3.md, originalMd) || !strings.Contains(m3.md, "---") {
		t.Error("clicking followup must append the --- separator below prior md")
	}
}

// TestStopClickSetsStopped verifies that clicking the stop button on a running
// block marks that block Stopped.
func TestStopClickSetsStopped(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
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
	// The stop action itself is emitted to the action fifo.
	kind, blockID, _ := readFirstAction(t, fifo)
	if kind != "stop" || blockID != "verify" {
		t.Errorf("stop click emitted (%q,%q), want (stop,verify)", kind, blockID)
	}
}

// TestStoppedResultYieldsStoppedStatusNoFollowup verifies that a result arriving
// for a Stopped block resolves to Status "stopped", clears the Stopped flag, and
// never auto-fires a followup — even for the verify block at a signal exit (143).
func TestStoppedResultYieldsStoppedStatusNoFollowup(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()
	m.blockStates["verify"] = blockRunState{Status: "running", Stopped: true}

	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 143, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd != nil {
		t.Errorf("a stopped block result must not auto-fire a followup, got cmd %T", cmd)
	}
	if _, err := os.Stat(fifo); err == nil {
		t.Error("a stopped block result must not emit any action")
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

// TestVerifySignalKilledDoesNotAutoFire verifies the belt-and-suspenders guard:
// a verify result with a signal exit (>128, e.g. 143/SIGTERM) does NOT auto-fire
// even when the Stopped flag is absent; an ordinary exit 1 still DOES (regression).
func TestVerifySignalKilledDoesNotAutoFire(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()

	// exit 143, no Stopped flag: the exit>128 guard must suppress the auto-fire.
	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 143, Logpath: "/tmp/x.log"})
	if cmd != nil {
		t.Errorf("verify signal-kill (exit 143) must not auto-fire, got cmd %T", cmd)
	}
	if _, err := os.Stat(fifo); err == nil {
		t.Error("verify signal-kill must not emit any action")
	}

	// exit 1, fresh model: an ordinary non-zero exit still auto-fires.
	m = newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()
	_, cmd = m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd == nil {
		t.Error("verify ordinary failure (exit 1) must still auto-fire a followup")
	}
	kind, blockID, _ := readFirstAction(t, fifo)
	if kind != "followup" || blockID != "verify" {
		t.Errorf("exit 1 emitted (%q,%q), want (followup,verify)", kind, blockID)
	}
}

// TestVerifyExit127DoesNotAutoFire verifies that a verify result with exit 127
// ("command not found" — the verify command itself couldn't run) does NOT
// auto-fire a followup, while an ordinary exit 1 still DOES (regression).
func TestVerifyExit127DoesNotAutoFire(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")

	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()

	// exit 127: command not found — the exit==127 guard must suppress the auto-fire.
	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 127, Logpath: "/tmp/x.log"})
	m3 := m2.(model)
	if cmd != nil {
		t.Errorf("verify exit 127 (command not found) must not auto-fire, got cmd %T", cmd)
	}
	if _, err := os.Stat(fifo); err == nil {
		t.Error("verify exit 127 must not emit any action")
	}
	if m3.thinking {
		t.Error("verify exit 127 must not begin a followup stream (thinking)")
	}
	// The block still resolves to failed so the manual "try another fix" button appears.
	if m3.blockStates["verify"].Status != "failed" {
		t.Errorf("verify exit 127 block status = %q, want failed", m3.blockStates["verify"].Status)
	}

	// exit 1, fresh model: an ordinary non-zero exit still auto-fires (regression).
	m = newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.fifoPath = fifo
	m.inputFifoPath = filepath.Join(dir, "input-fifo")
	m.reflow()
	_, cmd = m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd == nil {
		t.Error("verify ordinary failure (exit 1) must still auto-fire a followup")
	}
	kind, blockID, _ := readFirstAction(t, fifo)
	if kind != "followup" || blockID != "verify" {
		t.Errorf("exit 1 emitted (%q,%q), want (followup,verify)", kind, blockID)
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
