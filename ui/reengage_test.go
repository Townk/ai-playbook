package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ai-playbook/capture"
	"ai-playbook/driver"
	"ai-playbook/orchestrator"
)

// fakeAgent returns a canned stream and records calls. Injected as author.Agent.
type fakeAgent struct {
	canned string
	calls  int
}

func (f *fakeAgent) agent(systemPrompt, userMessage string) (io.ReadCloser, error) {
	f.calls++
	return io.NopCloser(strings.NewReader(f.canned)), nil
}

// newReengageModel wires an in-process model to an orchestrator whose Reengage uses
// a fake agent, so regenerate/followup/wrapup re-author deterministically.
func newReengageModel(t *testing.T, canned string) (model, *fakeAgent) {
	t.Helper()
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	fa := &fakeAgent{canned: canned}
	m := newModel("agent", "old playbook content")
	m.orch = orchestrator.New(d, &cliMux{}).WithReengage(&orchestrator.Reengage{
		Req: capture.Request{
			Command:     "make build",
			Exit:        "2",
			UserRequest: "fix my build",
			ProjectRoot: t.TempDir(),
		},
		Agent:    fa.agent,
		DataRoot: t.TempDir(),
	})
	return m, fa
}

// collectMsgs runs a tea.Cmd and flattens any tea.BatchMsg it yields into a slice
// of concrete messages (re-running nested batch cmds).
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	msg := cmd()
	switch mm := msg.(type) {
	case tea.BatchMsg:
		for _, c := range mm {
			out = append(out, collectMsgs(c)...)
		}
	default:
		out = append(out, msg)
	}
	return out
}

// pumpStream runs the re-engage trigger's batched cmd, routes the reArmStreamMsg
// through Update to swap the reader, then pumps readStream/streamEventsMsg until
// EOF so the fresh stream is fully rendered into m.md — mirroring the live event
// loop without a TTY.
func pumpStream(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	// Find the reArmStreamMsg in the trigger's batch and apply it.
	var rearm tea.Cmd
	for _, msg := range collectMsgs(cmd) {
		if rs, ok := msg.(reArmStreamMsg); ok {
			nm, c := m.Update(rs)
			m = nm.(model)
			rearm = c
			break
		}
	}
	if rearm == nil {
		t.Fatal("no reArmStreamMsg produced by the trigger cmd")
	}
	// rearm is readStream; pump it (and its continuations) until EOF.
	next := rearm
	for i := 0; i < 1000 && next != nil; i++ {
		msg := next()
		ev, ok := msg.(streamEventsMsg)
		if !ok {
			break
		}
		nm, c := m.Update(ev)
		m = nm.(model)
		if ev.eof {
			break
		}
		next = c
	}
	return m
}

// Triggering regenerate in-process re-arms the parser with the fake agent's stream
// in REPLACE mode: the old content is cleared and the fresh playbook streams in.
func TestInProcessRegenerateReArmsReplace(t *testing.T) {
	m, fa := newReengageModel(t, "FRESH REGENERATED PLAYBOOK\n")

	cmd := m.beginRegenerate()
	if cmd == nil {
		t.Fatal("beginRegenerate returned nil cmd with Reengage wired")
	}
	// REPLACE: the rendered content was reset on the trigger.
	if m.md != "" {
		t.Errorf("REPLACE did not reset m.md → %q", m.md)
	}

	m = pumpStream(t, m, cmd)

	if fa.calls != 1 {
		t.Fatalf("agent calls = %d, want 1", fa.calls)
	}
	if !strings.Contains(m.md, "FRESH REGENERATED PLAYBOOK") {
		t.Errorf("regenerate did not stream the fresh playbook into m.md → %q", m.md)
	}
}

// Triggering wrap-up in-process appends the `## Solution` stream below the existing
// playbook (APPEND mode).
func TestInProcessWrapupReArmsAppend(t *testing.T) {
	m, fa := newReengageModel(t, "## Solution\nrun make -B\n")

	cmd := m.beginWrapupInProc(m.runLog())
	if cmd == nil {
		t.Fatal("beginWrapupInProc returned nil cmd with Reengage wired")
	}
	// APPEND: the existing content is kept (a separator was appended).
	if !strings.Contains(m.md, "old playbook content") {
		t.Errorf("APPEND dropped the existing playbook → %q", m.md)
	}

	m = pumpStream(t, m, cmd)

	if fa.calls != 1 {
		t.Fatalf("agent calls = %d, want 1", fa.calls)
	}
	if !strings.Contains(m.md, "old playbook content") {
		t.Errorf("APPEND must keep the original playbook → %q", m.md)
	}
	if !strings.Contains(m.md, "## Solution") {
		t.Errorf("wrap-up did not append the Solution section → %q", m.md)
	}
}

// A failed VERIFY result must AUTO-fire the in-process follow-up when Reengage is
// wired but there is NO input FIFO — the live session path. This is the stage-4c-ii
// regression: the resultMsg guard previously suppressed the auto-fire whenever
// inputFifoPath was empty, so the live session (file/stdin input, no FIFO, Reengage
// set) silently dropped every verify-fail follow-up. Driving the resultMsg through
// Update must return a non-nil cmd (re-engagement initiated) and re-arm the model
// (thinking + APPEND separator), exactly like the FIFO path's auto-fire.
func TestVerifyFailureAutoFiresFollowupInProc(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.inputFifoPath = "" // live session: NO input FIFO, only in-process Reengage
	m.reflow()           // populate m.blocks so blockCommand("verify") resolves

	if !m.canReengageInProc() {
		t.Fatal("test setup: expected in-process re-engagement to be available")
	}
	originalMd := m.md

	m2, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m3 := m2.(model)

	if cmd == nil {
		t.Fatal("verify failure with Reengage wired (no FIFO) must auto-fire — got nil cmd")
	}
	if m3.blockStates["verify"].Status != "failed" {
		t.Errorf("verify block status = %q, want failed", m3.blockStates["verify"].Status)
	}
	if !m3.thinking {
		t.Error("in-process auto-fire must set thinking=true")
	}
	if !m3.streaming {
		t.Error("in-process auto-fire must set streaming=true")
	}
	if !strings.Contains(m3.md, originalMd) {
		t.Error("in-process auto-fire must keep prior md content (APPEND)")
	}
	if !strings.Contains(m3.md, "---") {
		t.Error("in-process auto-fire must append the --- separator")
	}
}

// With NEITHER an input FIFO nor in-process re-engagement, a verify failure must
// NOT auto-fire (nothing could deliver the follow-up) — the pre-4c-ii standalone
// behavior is preserved.
func TestVerifyFailureNoReengageNoFifoDoesNotFire(t *testing.T) {
	m := newModel("T", "```bash {id=verify}\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.inputFifoPath = "" // no FIFO
	// m.orch is nil → no in-process re-engagement either.
	m.reflow()

	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd != nil {
		t.Errorf("verify failure with no FIFO and no Reengage must not auto-fire, got %T", cmd)
	}
}

// hasSpinTick reports whether running cmd (flattening batches) yields a spinTickMsg
// — i.e. a spinner tick loop is (re)started.
func hasSpinTick(cmd tea.Cmd) bool {
	for _, msg := range collectMsgs(cmd) {
		if _, ok := msg.(spinTickMsg); ok {
			return true
		}
	}
	return false
}

// Issue #1: when the verify-fail auto-fire begins the follow-up thinking state,
// a spinner tick MUST be (re)issued so the follow-up "Working…" animates exactly
// like the first authoring — even when a (stale) tick loop flag was still set from
// the just-finished verify run. restartTick guarantees this regardless of the flag.
func TestFollowupReissuesSpinnerTick(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.inputFifoPath = ""
	m.reflow()
	// Stale-true tick flag (the prior verify-run loop's flag had not been cleared):
	// this is exactly the condition under which startTick would no-op and the
	// follow-up spinner would freeze. restartTick must still issue a fresh tick.
	m.tickRunning = true

	_, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	if cmd == nil {
		t.Fatal("verify failure must auto-fire (non-nil cmd)")
	}
	if !hasSpinTick(cmd) {
		t.Error("follow-up auto-fire must (re)issue a spinner tick so the spinner animates")
	}
}

// Issue #2: an activityMsg while thinking updates the visible thinking-region line
// to the agent's latest tool-call summary (rendered with the "⟳" glyph), and a
// later real-content stream clears it.
func TestActivityMsgUpdatesThinkingLine(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.thinking = true
	m.streaming = true
	ch := make(chan string, 4)
	m.activity = ch

	m2, _ := m.Update(activityMsg{summary: "run: gg build", ok: true})
	m = m2.(model)
	if m.activityLine != "run: gg build" {
		t.Fatalf("activityLine = %q, want %q", m.activityLine, "run: gg build")
	}
	view := strip(m.viewString())
	if !strings.Contains(view, "run: gg build") {
		t.Errorf("thinking view must show the activity summary; got:\n%s", view)
	}
	if !strings.Contains(view, activityGlyph) {
		t.Errorf("activity line must render the %q glyph", activityGlyph)
	}

	// Real playbook content arrives → the activity line is cleared.
	m3, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{text: "# Diagnosis\n"}}})
	m = m3.(model)
	if m.activityLine != "" {
		t.Errorf("activityLine must clear when real content arrives, got %q", m.activityLine)
	}
}

// Issue #2: a closed activity channel (!ok) stops the model re-subscribing — the
// activityMsg handler must not re-issue the wait cmd.
func TestActivityChannelClosedStopsSubscription(t *testing.T) {
	m := newModel("agent", "")
	ch := make(chan string)
	m.activity = ch
	m2, cmd := m.Update(activityMsg{ok: false})
	m = m2.(model)
	if m.activity != nil {
		t.Error("a closed activity channel must clear m.activity")
	}
	if cmd != nil {
		t.Errorf("a closed activity channel must not re-subscribe, got %T", cmd)
	}
}

// Issue #3 (in-process path): two successive verify failures both auto-fire the
// in-process follow-up; a third (at the cap) does not, and the manual button shows.
func TestVerifyFailureRepeatsUntilCapInProc(t *testing.T) {
	m, _ := newReengageModel(t, "# Revised fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.inputFifoPath = ""
	m.maxFollowups = 2
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("test setup: expected in-process re-engagement to be available")
	}

	m2, cmd1 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m2.(model)
	if cmd1 == nil {
		t.Fatal("first verify failure must auto-fire in-process")
	}
	if m.followups != 1 {
		t.Fatalf("followups after first = %d, want 1", m.followups)
	}

	m3, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m3.(model)
	if cmd2 == nil {
		t.Fatal("second verify failure must ALSO auto-fire in-process (repeat-until-success)")
	}
	if m.followups != 2 {
		t.Fatalf("followups after second = %d, want 2", m.followups)
	}

	m4, cmd3 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: "/tmp/x.log"})
	m = m4.(model)
	if cmd3 != nil {
		t.Errorf("at the cap, in-process verify failure must NOT auto-fire, got %T", cmd3)
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

// Follow-up in-process re-arms in APPEND mode with the failed output threaded in.
func TestInProcessFollowupReArmsAppend(t *testing.T) {
	m, fa := newReengageModel(t, "# Revised fix\n")

	cmd := m.beginFollowupStream("verify", "make build")
	if cmd == nil {
		t.Fatal("beginFollowupStream returned nil cmd with Reengage wired")
	}
	m = pumpStream(t, m, cmd)

	if fa.calls != 1 {
		t.Fatalf("agent calls = %d, want 1", fa.calls)
	}
	if !strings.Contains(m.md, "old playbook content") {
		t.Errorf("followup APPEND must keep the original playbook → %q", m.md)
	}
	if !strings.Contains(m.md, "Revised fix") {
		t.Errorf("followup did not append the revised fix → %q", m.md)
	}
}
