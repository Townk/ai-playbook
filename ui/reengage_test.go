package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ai-playbook/agentstream"
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

// newReengageEventsModel wires an in-process model to an orchestrator whose
// Reengage uses an injected EVENT producer (the part-2b path) instead of the text
// Agent, so regenerate/followup/wrapup stream a normalized event channel that the
// orchestrator fans into a playbook reader + a live activity feed.
func newReengageEventsModel(t *testing.T, delta, final string) (model, *fakeEventsProducer) {
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
	fe := &fakeEventsProducer{delta: delta, final: final}
	m := newModel("agent", "old playbook content")
	m.width, m.height = 80, 24
	m.orch = orchestrator.New(d, &cliMux{}).WithReengage(&orchestrator.Reengage{
		Req: capture.Request{
			Command:     "make build",
			Exit:        "2",
			UserRequest: "fix my build",
			ProjectRoot: t.TempDir(),
		},
		Events:   fe.fn,
		DataRoot: t.TempDir(),
	})
	return m, fe
}

// fakeEventsProducer is the ui-side injected orchestrator.EventsFunc: it emits a
// canned normalized event stream (delta → playbook; reasoning + tool → activity;
// Final → body) so a re-engagement exercises the live activity feed deterministically.
type fakeEventsProducer struct {
	delta, final string
}

func (f *fakeEventsProducer) fn(kind orchestrator.ReengageKind, failedOutput string) (<-chan agentstream.Event, func() error, error) {
	ch := make(chan agentstream.Event)
	go func() {
		ch <- agentstream.Event{Kind: agentstream.TextDelta, Text: f.delta}
		ch <- agentstream.Event{Kind: agentstream.Reasoning, Text: "thinking it through"}
		ch <- agentstream.Event{Kind: agentstream.ToolActivity, Text: "run: make build"}
		ch <- agentstream.Event{Kind: agentstream.Final, Text: f.final}
		close(ch)
	}()
	return ch, func() error { return nil }, nil
}

// Part 2b: a followup over the EVENT path re-arms with the orchestrator's fan-out,
// carrying a live activity channel into the model. The reArmStreamMsg swaps
// m.activity to that feed, and an activityMsg off it updates m.activityLine while
// thinking — mirroring how the initial authoring shows live reasoning.
func TestInProcessFollowupEventPathWiresActivity(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")

	cmd := m.beginFollowupInProc("ld: symbol not found")
	if cmd == nil {
		t.Fatal("beginFollowupInProc returned nil with an Events-backed Reengage")
	}

	// Find the reArmStreamMsg the trigger produced (off the event loop) and apply it.
	var rearm reArmStreamMsg
	var found bool
	for _, msg := range collectMsgs(cmd) {
		if rs, ok := msg.(reArmStreamMsg); ok {
			rearm = rs
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no reArmStreamMsg produced by the followup trigger")
	}
	if rearm.err != nil {
		t.Fatalf("re-arm error: %v", rearm.err)
	}
	if rearm.activity == nil {
		t.Fatal("event-path followup must carry a non-nil activity channel into the model")
	}

	// Apply the re-arm: the model must swap m.activity to the re-engagement feed.
	nm, _ := m.Update(rearm)
	m = nm.(model)
	if m.activity != rearm.activity {
		t.Fatal("reArmStreamMsg must swap m.activity to the re-engagement feed")
	}

	// A summary off the NEW feed updates the activity line while thinking.
	m.thinking = true
	m2, _ := m.Update(activityMsg{summary: "run: make build", ok: true, ch: m.activity})
	m = m2.(model)
	if m.activityLine != "run: make build" {
		t.Errorf("activityLine = %q, want the re-engagement tool summary", m.activityLine)
	}
}

// A stale activityMsg (from the initial-authoring feed that has since been swapped
// out) must NOT clobber the freshly-wired re-engagement feed nor paint its summary.
func TestStaleActivityFeedIgnoredAfterReArm(t *testing.T) {
	m := newModel("agent", "")
	m.width, m.height = 80, 24
	m.thinking = true
	stale := make(chan string)
	fresh := make(chan string)
	m.activity = fresh // the current (re-engagement) feed

	// A close (!ok) from the STALE feed must not clear the current m.activity.
	m2, cmd := m.Update(activityMsg{ok: false, ch: stale})
	m = m2.(model)
	if m.activity != fresh {
		t.Error("a stale feed's close must not clobber the current activity feed")
	}
	if cmd != nil {
		t.Errorf("a stale feed's close must not re-subscribe, got %T", cmd)
	}

	// A summary from the STALE feed must not paint the activity line.
	m3, _ := m.Update(activityMsg{summary: "stale: do not show", ok: true, ch: stale})
	m = m3.(model)
	if m.activityLine == "stale: do not show" {
		t.Error("a stale feed's summary must not paint the activity line")
	}
}

// Issue #1: a follow-up re-arm followed by streamed content must NOT auto-scroll
// the viewport to the bottom — the user is reading the failed attempt. m.yOff must
// stay where it was; only an explicit follow (wrap-up) jumps to the bottom.
func TestFollowupReArmDoesNotAutoScroll(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")
	// A long playbook the user has scrolled UP into (yOff well above the bottom).
	var sb strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&sb, "line %d of the original playbook\n", i)
	}
	m.md = sb.String()
	m.reflow()
	m.yOff = 5 // user is reading near the top
	startYOff := m.yOff

	// Begin a follow-up: APPEND mode. follow MUST be false so the viewport is pinned.
	cmd := m.beginFollowupInProc("boom")
	if cmd == nil {
		t.Fatal("beginFollowupInProc returned nil")
	}
	if m.follow {
		t.Fatal("follow-up must keep follow=false so the viewport does not scroll")
	}
	if m.yOff != startYOff {
		t.Fatalf("begin follow-up moved yOff %d -> %d before any content", startYOff, m.yOff)
	}

	// Apply the re-arm, then stream new content + flush — yOff must NOT jump to the bottom.
	var rearm reArmStreamMsg
	for _, msg := range collectMsgs(cmd) {
		if rs, ok := msg.(reArmStreamMsg); ok {
			rearm = rs
			break
		}
	}
	nm, _ := m.Update(rearm)
	m = nm.(model)
	if m.follow {
		t.Error("re-arm must not re-enable follow for a follow-up")
	}
	m2, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{text: "## Revised diagnosis\nmore content\n"}}})
	m = m2.(model)
	m.flushRender() // force the coalesced reflow now

	if m.yOff != startYOff {
		t.Errorf("follow-up streamed content auto-scrolled the viewport: yOff %d -> %d (want unchanged)", startYOff, m.yOff)
	}
}

// Issue #2: two successive follow-up re-arms must EACH deliver a non-nil, FRESH
// activity channel that the model swaps in and that updates m.activityLine — the
// 2nd/3rd round must show live activity exactly like the first (no dead feed). This
// drives the real resultMsg verify-fail auto-fire path twice.
func TestTwoSuccessiveFollowupsLiveActivity(t *testing.T) {
	m, _ := newReengageEventsModel(t, "", "# fix\n") // empty delta → activity gets reasoning+tool
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.maxFollowups = 5
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("setup: expected in-process re-engagement")
	}

	var seen []<-chan string
	round := func(label string) {
		nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: ""})
		m = nm.(model)
		if cmd == nil {
			t.Fatalf("%s: verify-fail did not auto-fire", label)
		}
		var rearm reArmStreamMsg
		var found bool
		for _, msg := range collectMsgs(cmd) {
			if rs, ok := msg.(reArmStreamMsg); ok {
				rearm = rs
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s: no reArmStreamMsg", label)
		}
		if rearm.activity == nil {
			t.Fatalf("%s: re-arm carried a NIL activity channel (dead feed)", label)
		}
		for _, prev := range seen {
			if prev == rearm.activity {
				t.Fatalf("%s: re-arm reused a PRIOR activity channel — must be fresh", label)
			}
		}
		seen = append(seen, rearm.activity)

		nm2, c := m.Update(rearm)
		m = nm2.(model)
		if m.activity != rearm.activity {
			t.Fatalf("%s: model did not swap m.activity to the fresh feed", label)
		}
		// Pump the fresh subscription to drain its summaries: the activityMsg handler
		// re-issues activityWaitCmd, so follow that chain until a non-empty summary
		// updates m.activityLine (the empty TextDelta is dropped by collapseLine).
		m.thinking = true
		m.activityLine = ""
		next := firstActivityWait(c)
		for i := 0; i < 20 && next != nil && m.activityLine == ""; i++ {
			msg := next()
			am, ok := msg.(activityMsg)
			if !ok {
				break
			}
			nm3, c3 := m.Update(am)
			m = nm3.(model)
			next = c3
		}
		if m.activityLine == "" {
			t.Errorf("%s: activityLine never updated off the fresh feed (dead feed)", label)
		}
		// End this round (closes the round's stream) so the next verify can re-fire.
		nm4, _ := m.Update(streamEventsMsg{eof: true})
		m = nm4.(model)
	}

	round("first followup")
	round("second followup")
}

// Issue #3: a verify exit-0 result auto-triggers the wrap-up re-engagement ONCE
// (and not again on a second verify-0); a verify-fail still triggers the follow-up.
func TestVerifySuccessTriggersWrapupOnce(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# resolved?\n", "## Solution\ndone\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("setup: expected in-process re-engagement")
	}

	// First verify exit 0 → wrap-up fires once.
	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("verify exit 0 must auto-trigger the wrap-up re-engagement")
	}
	if !m.wrappedUp {
		t.Fatal("verify exit 0 must set wrappedUp")
	}
	if !m.thinking {
		t.Error("wrap-up auto-trigger must set thinking=true")
	}
	// Drain the wrap-up round so the model settles before the next verify.
	m = pumpReArm(t, m, cmd)

	// Second verify exit 0 → must NOT re-trigger (once per resolution).
	nm2, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm2.(model)
	if cmd2 != nil {
		t.Errorf("a second verify exit 0 must NOT re-trigger the wrap-up, got %T", cmd2)
	}
}

// Issue #3 (regression guard): a verify FAILURE still triggers the follow-up,
// unchanged by the verify-success wrap-up addition.
func TestVerifyFailStillTriggersFollowupNotWrapup(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
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

// firstActivityWait flattens cmd's batch and returns the single tea.Cmd that yields
// an activityMsg (the activityWaitCmd), so a test can drive the activity feed by
// following its re-subscription chain.
func firstActivityWait(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, c := range bm {
			if w := firstActivityWait(c); w != nil {
				return w
			}
		}
		return nil
	}
	if _, ok := msg.(activityMsg); ok {
		// Re-wrap the already-produced msg as a cmd so the caller can apply it.
		return func() tea.Msg { return msg }
	}
	return nil
}

// pumpReArm applies the reArmStreamMsg in a trigger's batch and drains the resulting
// stream to EOF (settling thinking/streaming), mirroring pumpStream but tolerant of
// the event-path activity batch.
func pumpReArm(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	var rearm reArmStreamMsg
	var found bool
	for _, msg := range collectMsgs(cmd) {
		if rs, ok := msg.(reArmStreamMsg); ok {
			rearm = rs
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no reArmStreamMsg in trigger batch")
	}
	nm, c := m.Update(rearm)
	m = nm.(model)
	next := c
	for i := 0; i < 1000 && next != nil; i++ {
		msg := next()
		ev, ok := msg.(streamEventsMsg)
		if !ok {
			// Skip non-stream msgs (e.g. activityMsg/spinTick) and stop pumping.
			break
		}
		nm2, c2 := m.Update(ev)
		m = nm2.(model)
		if ev.eof {
			break
		}
		next = c2
	}
	// Ensure settled for the test's next step.
	m2, _ := m.Update(streamEventsMsg{eof: true})
	return m2.(model)
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

// Issue #1: an AUTO follow-up (the verify-fail auto-fire) must insert an
// agent-voice narration line ABOVE the new attempt, and the phrasing must vary by
// attempt number across successive rounds.
func TestAutoFollowupAnnouncesInAgentVoice(t *testing.T) {
	m, _ := newReengageEventsModel(t, "", "# fix\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.width, m.height = 80, 24
	m.inputFifoPath = ""
	m.maxFollowups = 5
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("setup: expected in-process re-engagement")
	}

	// Round 1: the first announcement appears in the rendered doc.
	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: ""})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("round 1: verify-fail did not auto-fire")
	}
	want1 := followupAnnouncement(1)
	if !strings.Contains(m.md, want1) {
		t.Errorf("round 1 announcement %q not inserted into md:\n%s", want1, m.md)
	}
	// Rendered as a dim/italic narration paragraph (underscore-wrapped markdown).
	if !strings.Contains(m.md, "_"+want1+"_") {
		t.Errorf("round 1 announcement must be italic-wrapped narration, got:\n%s", m.md)
	}

	// Drive the round to EOF so a fresh thinking session can begin for round 2.
	nmEOF, _ := m.Update(streamEventsMsg{eof: true})
	m = nmEOF.(model)
	m.reflow()

	// Round 2: a DIFFERENT announcement (varies by attempt number).
	nm2, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: ""})
	m = nm2.(model)
	if cmd2 == nil {
		t.Fatal("round 2: verify-fail did not auto-fire")
	}
	want2 := followupAnnouncement(2)
	if want1 == want2 {
		t.Fatal("attempt-1 and attempt-2 announcements must differ")
	}
	if !strings.Contains(m.md, want2) {
		t.Errorf("round 2 announcement %q not inserted into md:\n%s", want2, m.md)
	}
}

// followupAnnouncement must vary per attempt and clamp to the last phrase past
// the list (e.g. a higher $AI_ASSIST_MAX_FOLLOWUPS).
func TestFollowupAnnouncementVariesAndClamps(t *testing.T) {
	a1, a2, a3 := followupAnnouncement(1), followupAnnouncement(2), followupAnnouncement(3)
	if a1 == a2 || a2 == a3 || a1 == a3 {
		t.Errorf("announcements must differ across attempts: %q %q %q", a1, a2, a3)
	}
	last := followupAnnouncements[len(followupAnnouncements)-1]
	if got := followupAnnouncement(99); got != last {
		t.Errorf("attempt past the list = %q, want held tail %q", got, last)
	}
	if got := followupAnnouncement(0); got != followupAnnouncements[0] {
		t.Errorf("attempt 0 = %q, want first phrase", got)
	}
}

// Issue #2: the one-time auto-follow-up scroll sets m.yOff to the announcement's
// starting line so it becomes the TOP visible body row, and subsequent streamed
// content does NOT move it (follow stays false).
func TestAutoFollowupOneTimeScrollThenNoMovement(t *testing.T) {
	m, _ := newReengageEventsModel(t, "", "# fix\n")
	// A long playbook the user has scrolled into; the verify block is at the end.
	var sb strings.Builder
	sb.WriteString("# Playbook\n\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&sb, "line %d of the original playbook\n", i)
	}
	sb.WriteString("\n```bash {id=verify}\nmake build\n```\n")
	m.md = sb.String()
	m.width, m.height = 80, 24
	m.inputFifoPath = ""
	m.maxFollowups = 5
	m.reflow()
	m.yOff = 3 // user reading near the top

	startYOff := m.yOff

	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 1, Logpath: ""})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("verify-fail did not auto-fire")
	}
	if m.follow {
		t.Fatal("auto-follow-up must keep follow=false")
	}
	// The one-time scroll jumped the viewport DOWN to the announcement (away from the
	// user's top position) so the new attempt gets a clean "fresh start" frame.
	if m.yOff <= startYOff {
		t.Fatalf("one-time scroll must move yOff down to the announcement: %d -> %d", startYOff, m.yOff)
	}
	// The announcement is now visible within the body window (it is the top row, or
	// pulled up by clampScroll when it sits at the very end of the doc).
	want := followupAnnouncement(1)
	var annIdx int = -1
	for i := m.yOff; i < len(m.lines) && i < m.yOff+m.body(); i++ {
		if strings.Contains(m.lines[i].Text, want) {
			annIdx = i
			break
		}
	}
	if annIdx < 0 {
		t.Fatalf("announcement %q must be visible in the body window [%d,%d); lines=%d", want, m.yOff, m.yOff+m.body(), len(m.lines))
	}

	// Apply the re-arm + stream content; yOff must NOT move further.
	var rearm reArmStreamMsg
	for _, msg := range collectMsgs(cmd) {
		if rs, ok := msg.(reArmStreamMsg); ok {
			rearm = rs
			break
		}
	}
	pinned := m.yOff
	nm2, _ := m.Update(rearm)
	m = nm2.(model)
	m2, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{text: "## Revised\nmore content here\n"}}})
	m = m2.(model)
	m.flushRender()
	if m.yOff != pinned {
		t.Errorf("streamed content moved the viewport after the one-time scroll: yOff %d -> %d", pinned, m.yOff)
	}
}
