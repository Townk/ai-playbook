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
	gotKind      orchestrator.ReengageKind
	gotBase      string
	gotChange    string
	calls        int
}

func (f *fakeEventsProducer) fn(kind orchestrator.ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error) {
	f.calls++
	f.gotKind = kind
	f.gotBase = base
	f.gotChange = change
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

// Stage 2 (spec §A): a verify exit-0 result sets the NATIVE confirm state ONCE
// (rendering an inline row, no auto agent-ask wrap-up); a second verify-0 must not
// re-prompt. No re-engagement cmd is fired on the result itself — the confirm is
// answered by y/n/click.
func TestVerifySuccessSetsConfirmOnce(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# resolved?\n", "## Solution\ndone\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	if !m.canReengageInProc() {
		t.Fatal("setup: expected in-process re-engagement")
	}

	// First verify exit 0 → confirm state set; NO agent re-engagement fired yet.
	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if cmd != nil {
		t.Errorf("verify exit 0 must NOT auto-fire an agent re-engagement (native confirm), got %T", cmd)
	}
	if !m.confirmResolved {
		t.Fatal("verify exit 0 must set the native confirmResolved state")
	}
	if !m.wrappedUp {
		t.Fatal("verify exit 0 must set wrappedUp (once-guard)")
	}
	if m.thinking {
		t.Error("setting the confirm must NOT set thinking=true (no generation yet)")
	}
	if fe.calls != 0 {
		t.Errorf("no agent call must happen on the confirm prompt itself, got %d", fe.calls)
	}

	// Second verify exit 0 → must NOT re-set the confirm (once per resolution). Clear
	// the first confirm to detect a spurious re-set.
	m.confirmResolved = false
	nm2, cmd2 := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm2.(model)
	if cmd2 != nil {
		t.Errorf("a second verify exit 0 must NOT re-trigger, got %T", cmd2)
	}
	if m.confirmResolved {
		t.Error("a second verify exit 0 must NOT re-set the confirm state")
	}
}

// Stage 2 (spec §A): the confirm row + its [ Yes ] [ No ] buttons render in the
// pager pane on a verify-success.
func TestVerifySuccessRendersConfirmRow(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# resolved?\n", "# Playbook\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("verify exit 0 must set confirmResolved")
	}
	view := strip(m.viewString())
	if !strings.Contains(view, "Generate a playbook for this solution?") {
		t.Errorf("confirm prompt prose missing from view:\n%s", view)
	}
	if !strings.Contains(view, confirmYesLabel) || !strings.Contains(view, confirmNoLabel) {
		t.Errorf("confirm Yes/No buttons missing from view:\n%s", view)
	}
	// The two Screen-fixed confirm buttons must be registered for click hit-testing.
	var yes, no bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-yes" {
			yes = true
		}
		if b.Kind == "confirm-no" {
			no = true
		}
	}
	if !yes || !no {
		t.Errorf("confirm buttons not registered: yes=%v no=%v", yes, no)
	}
}

// fix(ui): the confirm renders on TWO rows — the QUESTION prose on its own row and the
// [ Yes ] [ No ] buttons on a SEPARATE row directly below it. The buttons must NOT
// share the prompt line (the old single-row layout pushed them off the pane edge).
func TestConfirmRendersButtonsOnSeparateRow(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	// confirmRowString is now the QUESTION only — no button labels on it.
	q := strip(m.confirmRowString())
	if !strings.Contains(q, "Generate a playbook for this solution?") {
		t.Errorf("question row must carry the prompt prose, got %q", q)
	}
	if strings.Contains(q, confirmYesLabel) || strings.Contains(q, confirmNoLabel) {
		t.Errorf("buttons must NOT be on the question row, got %q", q)
	}
	// The buttons live on their own row (both labels there).
	btns := strip(m.confirmButtonsRowString())
	if !strings.Contains(btns, confirmYesLabel) || !strings.Contains(btns, confirmNoLabel) {
		t.Errorf("buttons row must carry both labels, got %q", btns)
	}

	// The confirm block is FIVE rows above the status bar: blank, prompt, blank,
	// buttons, blank. Locate the prompt and buttons rows in the full rendered view and
	// assert the blank spacers around them.
	lines := strings.Split(strip(m.viewString()), "\n")
	promptLine, buttonLine := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "Generate a playbook for this solution?") {
			promptLine = i
		}
		if strings.Contains(ln, confirmYesLabel) && strings.Contains(ln, confirmNoLabel) {
			buttonLine = i
		}
	}
	if promptLine < 0 || buttonLine < 0 {
		t.Fatalf("could not find both rows: prompt=%d buttons=%d", promptLine, buttonLine)
	}
	if promptLine == buttonLine {
		t.Error("the prompt prose and the buttons must render on SEPARATE rows")
	}
	// Layout: blank(prompt-1), prompt, blank(prompt+1), buttons(prompt+2), blank(prompt+3), status.
	if buttonLine != promptLine+2 {
		t.Errorf("buttons row must sit two rows below the question (a blank between): prompt=%d buttons=%d", promptLine, buttonLine)
	}
	blank := func(i string) bool { return strings.TrimSpace(i) == "" }
	if promptLine-1 < 0 || !blank(lines[promptLine-1]) {
		t.Errorf("a blank row must sit ABOVE the prompt (row %d)", promptLine-1)
	}
	if !blank(lines[promptLine+1]) {
		t.Errorf("a blank row must sit BETWEEN the prompt and the buttons (row %d)", promptLine+1)
	}
	if buttonLine+1 >= len(lines) || !blank(lines[buttonLine+1]) {
		t.Errorf("a blank row must sit BELOW the buttons (row %d)", buttonLine+1)
	}
	if strings.Contains(lines[buttonLine], "Generate a playbook for this solution?") {
		t.Error("buttons row must not contain the prompt prose")
	}
}

// fix(ui): the confirm buttons register on the BUTTONS row (m.height-2), left-aligned
// at the content edge — Yes at the left, No after it by the shared gap. The drawn
// label positions (content col 0 → screen col 2 under the 2-col margin) must match
// the registered click cells.
func TestAppendConfirmButtonsLeftAligned(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
	m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	var yes, no Button
	var fy, fn bool
	for _, b := range m.buttons {
		switch b.Kind {
		case "confirm-yes":
			yes, fy = b, true
		case "confirm-no":
			no, fn = b, true
		}
	}
	if !fy || !fn {
		t.Fatal("confirm buttons not registered")
	}
	wantRow := m.height - 3
	if yes.Line != wantRow || no.Line != wantRow {
		t.Errorf("buttons must be on the buttons row %d: yes.Line=%d no.Line=%d", wantRow, yes.Line, no.Line)
	}
	if yes.Col != confirmButtonIndent {
		t.Errorf("Yes must be left-aligned at content edge %d, got %d", confirmButtonIndent, yes.Col)
	}
	// Each button cell is the label width plus the Padding(0, confirmButtonPad) on both
	// sides; No follows the Yes cell plus the shared gap.
	wantYesW := len(confirmYesLabel) + 2*confirmButtonPad
	if yes.Width != wantYesW {
		t.Errorf("Yes cell width must include the padding: got %d want %d", yes.Width, wantYesW)
	}
	if no.Width != len(confirmNoLabel)+2*confirmButtonPad {
		t.Errorf("No cell width must include the padding: got %d want %d", no.Width, len(confirmNoLabel)+2*confirmButtonPad)
	}
	wantNoCol := confirmButtonIndent + wantYesW + confirmButtonGap
	if no.Col != wantNoCol {
		t.Errorf("No col must follow the Yes cell by the shared gap: got %d want %d", no.Col, wantNoCol)
	}
	// A click at each button's drawn cell (content col + 2-col margin) round-trips
	// through buttonAt back to the same button.
	if got, ok := buttonAt(m.buttons, yes.Col+2, yes.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "confirm-yes" {
		t.Fatalf("buttonAt at the Yes cell must hit Yes: ok=%v kind=%q", ok, got.Kind)
	}
	if got, ok := buttonAt(m.buttons, no.Col+2, no.Line, m.yOff, m.bodyTop()); !ok || got.Kind != "confirm-no" {
		t.Fatalf("buttonAt at the No cell must hit No: ok=%v kind=%q", ok, got.Kind)
	}
}

// fix(ui): the button columns are INDEPENDENT of the prompt width. The old layout put
// Yes at width(prompt)+2 (and No further right), so a long prompt pushed the buttons
// toward / past the pane edge — unreachable. The new left-aligned layout keeps both
// buttons at the content edge, well to the LEFT of the old width(prompt)+2 position,
// regardless of the prompt mode (fresh / amend).
func TestConfirmButtonColsIndependentOfPromptWidth(t *testing.T) {
	colsFor := func(servedBase string) (yesCol, noCol, promptW int) {
		m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
		m.md = "# Playbook\n\n```bash {id=verify}\nmake build\n```\n"
		m.inputFifoPath = ""
		m.servedBase = servedBase
		m.reflow()
		nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
		m = nm.(model)
		promptW = len(strip(m.confirmRowString()))
		for _, b := range m.buttons {
			if b.Kind == "confirm-yes" {
				yesCol = b.Col
			}
			if b.Kind == "confirm-no" {
				noCol = b.Col
			}
		}
		return
	}
	fy, fn, fw := colsFor("")                    // fresh prompt
	ay, an, aw := colsFor("# served playbook\n") // amend prompt
	wantNoCol := confirmButtonIndent + len(confirmYesLabel) + 2*confirmButtonPad + confirmButtonGap
	for _, c := range []struct {
		name             string
		yes, no, promptW int
	}{{"fresh", fy, fn, fw}, {"amend", ay, an, aw}} {
		if c.yes != confirmButtonIndent {
			t.Errorf("%s: Yes must stay at the left edge %d, got %d", c.name, confirmButtonIndent, c.yes)
		}
		if c.no != wantNoCol {
			t.Errorf("%s: No col must be the shared-constant value %d, got %d", c.name, wantNoCol, c.no)
		}
		// The OLD layout would have put Yes at promptW+2 / No further right; the new
		// columns sit well to the left of that, so the prompt width can't push them off.
		if c.no >= c.promptW {
			t.Errorf("%s: No col %d must be left of the old width(prompt)+2 position (promptW=%d)", c.name, c.no, c.promptW)
		}
	}
	// Independence: the columns are identical across the two prompt modes.
	if fy != ay || fn != an {
		t.Errorf("button cols must not change with the prompt: fresh=(%d,%d) amend=(%d,%d)", fy, fn, ay, an)
	}
}

// fix(ui): a mouse click at the drawn No button cell DISMISSES the confirm (the command
// already succeeded — nothing to re-fix) and does NOT start a generation. Yes-by-click is
// covered by TestConfirmResolvesByClick; this covers No and the drawn-cell round-trip.
func TestConfirmClickNoResolves(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	var no Button
	var found bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-no" {
			no, found = b, true
		}
	}
	if !found {
		t.Fatal("confirm-no button not registered")
	}
	nm2, _ := m.Update(tea.MouseClickMsg{X: no.Col + 2, Y: no.Line, Button: tea.MouseLeft})
	m = nm2.(model)
	if m.confirmResolved {
		t.Error("clicking No must dismiss (clear) the confirm state")
	}
	if m.finalDraft {
		t.Error("clicking No must NOT start a playbook generation")
	}
}

// Stage 2 (spec §A): answering "Yes" (the `y` key) generates the FINAL-PLAYBOOK in
// REPLACE mode as a DRAFT — the producer is called with KindReengageFinalPlaybook,
// the rendered content is reset, thinking starts, and finalDraft is set / committed
// stays false. The current troubleshoot content is threaded as the change.
func TestConfirmYesGeneratesFinalPlaybookReplaceDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — fix\nclean playbook\n", "# Playbook — fix\nclean playbook\n")
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.md = troubleshoot
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes (y) must trigger the final-playbook generation")
	}
	if m.confirmResolved {
		t.Error("answering Yes must clear the confirm state")
	}
	// REPLACE: the troubleshoot content was reset on the trigger.
	if m.md != "" {
		t.Errorf("Yes must REPLACE (reset m.md), got %q", m.md)
	}
	if !m.thinking {
		t.Error("Yes must set thinking=true (generation in flight)")
	}
	if !m.finalDraft {
		t.Error("Yes must mark the result a finalDraft")
	}
	if m.committed {
		t.Error("stage 2 must NOT commit the draft (committed stays false)")
	}

	// Drain the generation so the producer is invoked and we can assert the kind/change.
	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", fe.calls)
	}
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != "" {
		t.Errorf("stage 2 is fresh-only: base must be empty, got %q", fe.gotBase)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("Yes must thread the troubleshoot content as the change, got %q", fe.gotChange)
	}
	if !strings.Contains(m.md, "clean playbook") {
		t.Errorf("the clean playbook must stream into m.md, got %q", m.md)
	}
}

// Answering "No" (the `n` key) DISMISSES the confirm and does nothing else: the command
// already succeeded, so there is no follow-up and no generation. The user can still
// press `c` to generate later (covered by TestCKeyGeneratesAfterSolution).
func TestConfirmNoDismisses(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Revised\n", "# Revised fix\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	originalMd := m.md

	nm2, cmd := m.Update(key("n"))
	m = nm2.(model)
	if cmd != nil {
		t.Fatal("confirm No (n) must NOT trigger a follow-up or generation (dismiss only)")
	}
	if m.confirmResolved {
		t.Error("answering No must clear the confirm state")
	}
	if m.finalDraft {
		t.Error("No must NOT mark a finalDraft")
	}
	if m.thinking || m.streaming {
		t.Error("No must not start any stream")
	}
	// Content is untouched (no APPEND, no REPLACE).
	if m.md != originalMd {
		t.Errorf("No must leave the content untouched, got %q want %q", m.md, originalMd)
	}
	if fe.calls != 0 {
		t.Errorf("No must not invoke the producer, got %d calls", fe.calls)
	}
}

// Stage 4 (spec §C amend-on-rerun): when the session is SERVING an existing playbook
// (m.servedBase set, a cache HIT), a verify-success confirm → Yes AMENDS the served
// playbook — the producer is called with base==servedBase and change==the troubleshoot
// content (which carries the resolved fix). The served playbook is the base; the
// output is base+fix, re-cached under the same keys on `w` (never lost).
func TestConfirmYesAmendsServedPlaybook(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — amended\nbase + fix\n", "# Playbook — amended\nbase + fix\n")
	served := "# Playbook — My Setup\n\nstep 1\nstep 2\n"
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.servedBase = served // serving an existing playbook for this context
	m.md = troubleshoot
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes must trigger the amend generation")
	}
	if !m.finalDraft || m.committed {
		t.Errorf("amend must mark a draft (finalDraft=%v committed=%v)", m.finalDraft, m.committed)
	}

	m = pumpReArm(t, m, cmd)
	if fe.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", fe.calls)
	}
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != served {
		t.Errorf("AMEND must thread the served playbook as base, got %q want %q", fe.gotBase, served)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("AMEND must thread the troubleshoot content as change, got %q", fe.gotChange)
	}
}

// Stage 4 (spec §C): without a served base (a FRESH troubleshoot / cache MISS), the
// verify-success confirm → Yes generates a FRESH playbook (base==""), unchanged from
// stage 2. This is the amend-vs-fresh branch scoped by whether a playbook was served.
func TestConfirmYesFreshWhenNoServedBase(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — fresh\nnew\n", "# Playbook — fresh\nnew\n")
	troubleshoot := "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.servedBase = "" // FRESH: no playbook served
	m.md = troubleshoot
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}

	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes must trigger the fresh generation")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("producer kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
	if fe.gotBase != "" {
		t.Errorf("FRESH must thread an empty base, got %q", fe.gotBase)
	}
	if fe.gotChange != troubleshoot {
		t.Errorf("FRESH must thread the troubleshoot content as change, got %q", fe.gotChange)
	}
}

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
	m.inputFifoPath = ""
	m.reflow()

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

// Stage 4 (spec §C): the confirm wording differs by mode — amend prose when serving
// an existing playbook (servedBase set), fresh prose otherwise.
func TestConfirmWordingByMode(t *testing.T) {
	// Fresh: no served base → "Generate a playbook for this solution?".
	mf, _ := newReengageEventsModel(t, "# x\n", "# x\n")
	mf.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	mf.servedBase = ""
	mf.inputFifoPath = ""
	mf.reflow()
	nmf, _ := mf.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	mf = nmf.(model)
	freshView := strip(mf.viewString())
	if !strings.Contains(freshView, "Generate a playbook for this solution?") {
		t.Errorf("fresh confirm must read the fresh prose:\n%s", freshView)
	}
	if strings.Contains(freshView, "Update the playbook with this solution?") {
		t.Errorf("fresh confirm must NOT show the amend prose:\n%s", freshView)
	}

	// Amend: served base → "Update the playbook with this solution?".
	ma, _ := newReengageEventsModel(t, "# x\n", "# x\n")
	ma.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	ma.servedBase = "# Playbook — served\n\nstep\n"
	ma.inputFifoPath = ""
	ma.reflow()
	nma, _ := ma.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	ma = nma.(model)
	amendView := strip(ma.viewString())
	if !strings.Contains(amendView, "Update the playbook with this solution?") {
		t.Errorf("amend confirm must read the amend prose:\n%s", amendView)
	}
	if strings.Contains(amendView, "Generate a playbook for this solution?") {
		t.Errorf("amend confirm must NOT show the fresh prose:\n%s", amendView)
	}
	// The amend confirm's Yes/No buttons must still render + register for clicks.
	if !strings.Contains(amendView, confirmYesLabel) || !strings.Contains(amendView, confirmNoLabel) {
		t.Errorf("amend confirm Yes/No buttons missing:\n%s", amendView)
	}
}

// Stage 2 (spec §A): a mouse click on the [ Yes ] / [ No ] buttons resolves the
// confirm exactly like the y/n keys.
func TestConfirmResolvesByClick(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	// Locate the registered Yes button and click its center cell (+2 for the left margin).
	var yes Button
	var found bool
	for _, b := range m.buttons {
		if b.Kind == "confirm-yes" {
			yes = b
			found = true
		}
	}
	if !found {
		t.Fatal("confirm-yes button not registered")
	}
	clickX := yes.Col + 2 // buttonAt strips the 2-col left margin
	clickY := yes.Line    // Screen button: absolute screen row
	nm2, cmd := m.Update(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft})
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("clicking Yes must trigger the final-playbook generation")
	}
	if m.confirmResolved {
		t.Error("clicking Yes must clear the confirm state")
	}
	if !m.finalDraft {
		t.Error("clicking Yes must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("click Yes kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// Stage 2 (spec §E): the `w` key manually finalizes — it generates the same
// final-playbook draft (REPLACE) as the confirm Yes, even without a pending confirm.
func TestManualWGeneratesFinalPlaybookDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot content\n"
	m.inputFifoPath = ""
	m.reflow()

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
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("w kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// Stage 4b (spec §D): `w` on a DIRTY final-playbook DRAFT (finalDraft && !committed)
// re-persists it — it calls orchestrator.CommitPlaybook (save + cache-replace), shows
// "finalizing…" while the metadata round-trip runs, and on success the
// playbookCommittedMsg result flips committed=true and shows "✓ saved playbook → <path>".
// It does NOT re-generate (the producer is not called and the draft is preserved).
func TestWCommitsExistingDraft(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Playbook — My Setup\n\nclean playbook\n"
	m.finalDraft = true
	m.committed = false
	m.inputFifoPath = ""
	m.reflow()

	nm, cmd := m.Update(key("w"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("w on a dirty draft must return the commit cmd")
	}
	// committed flips on the RESULT now (not optimistically at the trigger); meanwhile
	// the transient "finalizing…" status covers the metadata round-trip.
	if m.committed {
		t.Error("w must not flip committed at the trigger — it flips on the persist result")
	}
	if m.status != "finalizing…" {
		t.Errorf("w-commit must show the finalizing status, got %q", m.status)
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
	m.inputFifoPath = ""
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

// Stage 3 (spec §E): `w` on a raw troubleshoot TRANSCRIPT (no draft yet) still
// GENERATES the final-playbook draft — the stage-2 behavior, unchanged.
func TestWGeneratesOnTranscript(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot transcript\n"
	m.finalDraft = false
	m.committed = false
	m.inputFifoPath = ""
	m.reflow()

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
	if fe.calls != 1 || fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("w-generate must call the final-playbook producer once, got calls=%d kind=%v", fe.calls, fe.gotKind)
	}
}

// Stage 4b (spec §D): confirm-Yes → generate → stream-EOF AUTO-persists a baseline.
// The finalize generation is marked persistOnFinish, so at EOF the model fires the
// commit (CommitPlaybook) and shows "finalizing…"; the playbookCommittedMsg result
// flips committed=true. Quitting now leaves a complete saved playbook.
func TestConfirmYesAutoPersistsBaselineAtEOF(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook — fix\nclean playbook\n", "# Playbook — fix\nclean playbook\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("confirm Yes must trigger generation")
	}
	if !m.persistOnFinish {
		t.Error("a FINALIZE (confirm-yes) generation must arm persistOnFinish")
	}

	// Drain to EOF and capture the auto-persist cmd the finalDraft branch returns.
	m, eofCmd := pumpReArmEOFCmd(t, m, cmd)
	if eofCmd == nil {
		t.Fatal("stream-EOF on a persistOnFinish finalize must return the auto-persist cmd")
	}
	if m.persistOnFinish {
		t.Error("persistOnFinish must be reset after the auto-persist fires (no re-persist)")
	}
	if m.status != "finalizing…" {
		t.Errorf("auto-persist must show the finalizing status, got %q", m.status)
	}
	// committed flips on the RESULT, not at EOF.
	if m.committed {
		t.Error("committed must not flip before the persist result")
	}
	pc, ok := eofCmd().(playbookCommittedMsg)
	if !ok {
		t.Fatalf("auto-persist cmd must yield a playbookCommittedMsg (CommitPlaybook called), got %T", eofCmd())
	}
	if pc.err != nil {
		t.Fatalf("auto-persist CommitPlaybook must succeed, got %v", pc.err)
	}
	nm3, _ := m.Update(pc)
	m = nm3.(model)
	if !m.committed {
		t.Error("the auto-persist result must flip committed=true (the baseline)")
	}
	if !strings.Contains(m.status, "saved playbook") {
		t.Errorf("auto-persist result must show the saved path, got %q", m.status)
	}
}

// Stage 4b (spec §D): an `f` AMEND → generate → stream-EOF does NOT auto-persist. The
// amend path leaves persistOnFinish cleared, so EOF fires no commit and committed stays
// false (an unsaved tweak the `w`/quit-guard handle).
func TestFAmendDoesNotAutoPersistAtEOF(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook — tweaked\nrevised\n", "# Playbook — tweaked\nrevised\n")
	// Start from a committed baseline (the auto-finish artifact) so the amend is the
	// thing under test: it must make the doc dirty again.
	m.md = "# Playbook — fix\n\nbody\n"
	m.finalDraft = true
	m.committed = true
	m.inputFifoPath = ""
	m.reflow()

	// Drive the `f` amend directly via its message (the asker float is off in tests).
	nm, cmd := m.Update(fChangeMsg{base: m.md, value: "add a cleanup step", submitted: true})
	m = nm.(model)
	if cmd == nil {
		t.Fatal("a submitted f amend must trigger generation")
	}
	if m.persistOnFinish {
		t.Error("an f amend must NOT arm persistOnFinish")
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
	if fe.calls != 1 || fe.gotKind != orchestrator.KindReengageFinalPlaybook {
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
func TestQuitGuardClearedByCommit(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Playbook — draft\n\nbody\n"
	m.finalDraft = true
	m.committed = false
	m.inputFifoPath = ""
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
	// The reArmStreamMsg handler returns a BATCH (readStream + activityWaitCmd on the
	// event path); extract the readStream cmd from it before pumping.
	next := firstStreamCmd(c)
	for i := 0; i < 1000 && next != nil; i++ {
		msg := next()
		ev, ok := msg.(streamEventsMsg)
		if !ok {
			break
		}
		nm2, c2 := m.Update(ev)
		m = nm2.(model)
		if ev.eof {
			break
		}
		next = firstStreamCmd(c2)
	}
	// Ensure settled for the test's next step.
	m2, _ := m.Update(streamEventsMsg{eof: true})
	return m2.(model)
}

// pumpReArmEOFCmd is like pumpReArm but captures the cmd the FIRST stream-EOF returns
// (Stage 4b's auto-persist: the finalDraft+persistOnFinish branch returns a
// commitPlaybookCmd). pumpReArm discards that cmd; this helper hands it back so a test
// can assert/drive the auto-persist. Returns the settled model and the EOF cmd (nil if
// the EOF branch did not fire a command — e.g. an `f` amend, which must NOT auto-persist).
func pumpReArmEOFCmd(t *testing.T, m model, cmd tea.Cmd) (model, tea.Cmd) {
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
	next := firstStreamCmd(c)
	var eofCmd tea.Cmd
	for i := 0; i < 1000 && next != nil; i++ {
		msg := next()
		ev, ok := msg.(streamEventsMsg)
		if !ok {
			break
		}
		nm2, c2 := m.Update(ev)
		m = nm2.(model)
		if ev.eof {
			eofCmd = c2
			break
		}
		next = firstStreamCmd(c2)
	}
	if eofCmd == nil {
		// The drained stream never surfaced a natural eof frame; settle with an explicit
		// one (mirrors pumpReArm's settle) and capture its cmd — this is the EOF the
		// finalDraft auto-persist branch keys on.
		m2, c := m.Update(streamEventsMsg{eof: true})
		m = m2.(model)
		eofCmd = c
	}
	return m, eofCmd
}

// firstStreamCmd flattens cmd's batch and returns the single tea.Cmd that yields a
// streamEventsMsg (the readStream cmd), so pumpReArm can drive the stream regardless
// of the event-path activity batch wrapping it.
func firstStreamCmd(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, c := range bm {
			if s := firstStreamCmd(c); s != nil {
				return s
			}
		}
		return nil
	}
	if _, ok := msg.(streamEventsMsg); ok {
		return func() tea.Msg { return msg }
	}
	return nil
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
	// The bug: the announcement sits past the normal scroll max, so clampScroll
	// pulled the viewport to the BOTTOM. The pin must relax the clamp (over-scroll:
	// yOff past maxY, blank below) and put the announcement at the TOP of the body —
	// not merely visible.
	if maxY := len(m.lines) - m.body(); m.yOff <= maxY {
		t.Errorf("announcement not pinned to top: yOff=%d <= maxY=%d (clamped to bottom)", m.yOff, maxY)
	}
	// Near the top — a few rows down is fine: the separator `---` (+ blank lines)
	// frames the attempt ABOVE the phrase, so the phrase sits a couple rows below yOff.
	if annIdx-m.yOff > 4 {
		t.Errorf("announcement must be near the TOP of the body (pinned), got %d rows down (yOff=%d)", annIdx-m.yOff, m.yOff)
	}
	// Issue #1: the pin is one line higher than before — the `---` SEPARATOR (the rule
	// framing the top of the new attempt), not the leading blank above it, is the FIRST
	// visible body row. The top row (m.lines[m.yOff]) must render the rule.
	if top := strings.TrimSpace(strip(m.lines[m.yOff].Text)); !strings.ContainsAny(top, "─-") || top == "" {
		t.Errorf("top visible body row must be the `---` separator, got %q", m.lines[m.yOff].Text)
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

// verifyBlockID falls back to the LAST runnable block when the agent drifts and
// leaves blocks untagged (the regression: a follow-up's blocks got auto-named ids
// like auto-5, so the id=="verify" triggers never matched). The explicit tag and
// the lone-fix cases must still behave.
func TestVerifyBlockIDFallback(t *testing.T) {
	// Two UNTAGGED runnable blocks → the last is the verify by convention.
	m := newModel("T", "```bash\nmake fix\n```\n\n```bash\nmake build\n```\n")
	m.width, m.height = 80, 24
	m.reflow()
	if len(m.blocks) < 2 {
		t.Fatalf("expected 2 parsed blocks, got %d", len(m.blocks))
	}
	if want, got := m.blocks[len(m.blocks)-1].ID, m.verifyBlockID(); got != want {
		t.Errorf("untagged: verifyBlockID()=%q, want last runnable %q", got, want)
	}
	// A single untagged block has NO implicit verify → keep the convention id
	// (a lone fix block's failure shows the manual button, not auto-fire).
	m1 := newModel("T", "```bash\nmake build\n```\n")
	m1.width, m1.height = 80, 24
	m1.reflow()
	if got := m1.verifyBlockID(); got != "verify" {
		t.Errorf("single block: verifyBlockID()=%q, want \"verify\"", got)
	}
	// Explicit {id=verify} always wins.
	m2 := newModel("T", "```bash {id=fix}\nx\n```\n\n```bash {id=verify}\ny\n```\n")
	m2.width, m2.height = 80, 24
	m2.reflow()
	if got := m2.verifyBlockID(); got != "verify" {
		t.Errorf("tagged: verifyBlockID()=%q, want \"verify\"", got)
	}
}

// fakeAsker is the ui.AskFunc test double: it records the prompt it was asked and
// returns a canned (value, submitted) pair — standing in for the request-input float
// without spawning a real zellij pane.
type fakeAsker struct {
	value     string
	submitted bool
	gotPrompt string
	calls     int
}

func (f *fakeAsker) fn(prompt string) (string, bool) {
	f.calls++
	f.gotPrompt = prompt
	return f.value, f.submitted
}

// Stage 5 (spec §D): pressing `f` with an asker wired issues a cmd that calls the
// asker; the resulting fChangeMsg carries the snapshotted pager content as the base
// and the typed value, and is submitted.
func TestFKeyIssuesAskCmd(t *testing.T) {
	m, _ := newReengageEventsModel(t, "", "")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.reflow()
	fk := &fakeAsker{value: "also configure the NDK", submitted: true}
	m.asker = fk.fn

	nm, cmd := m.Update(key("f"))
	m = nm.(model)
	if cmd == nil {
		t.Fatal("`f` with an asker must issue a cmd")
	}
	msg := cmd()
	fc, ok := msg.(fChangeMsg)
	if !ok {
		t.Fatalf("`f` cmd must yield an fChangeMsg, got %T", msg)
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
	m, fe := newReengageEventsModel(t, "# Playbook — amended\nbase + ndk\n", "# Playbook — amended\nbase + ndk\n")
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
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
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

// Stage 5 (spec §D): with no asker wired (off-zellij / tests), `f` is a no-op (a brief
// status, no cmd, no draft).
func TestFKeyNilAskerNoOp(t *testing.T) {
	m, fe := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = false
	m.asker = nil
	m.reflow()

	nm, cmd := m.Update(key("f"))
	m = nm.(model)
	if cmd != nil {
		t.Error("`f` with no asker must be a no-op (nil cmd)")
	}
	if m.finalDraft {
		t.Error("`f` with no asker must not mark a draft")
	}
	if fe.calls != 0 {
		t.Errorf("`f` with no asker must not call the producer, calls=%d", fe.calls)
	}
}

// Stage 5 (spec §D): `f` while streaming is a no-op — amends only apply to settled
// content (and must not be issued while a generation is in flight).
func TestFKeyWhileStreamingNoOp(t *testing.T) {
	m, _ := newReengageEventsModel(t, "x", "x")
	m.md = "# Playbook — current\n\nstep\n"
	m.streaming = true
	fk := &fakeAsker{value: "x", submitted: true}
	m.asker = fk.fn
	m.reflow()

	nm, cmd := m.Update(key("f"))
	_ = nm.(model)
	if cmd != nil {
		t.Error("`f` while streaming must be a no-op (nil cmd)")
	}
	if fk.calls != 0 {
		t.Errorf("`f` while streaming must not call the asker, calls=%d", fk.calls)
	}
}

// Stage 5 (spec §E): an `f` draft sets finalDraft && !committed, so the existing
// uncommitted-draft quit guard covers it — the first quit warns, a `w` commit clears
// it. This verifies `f` drafts ride the same guard as confirm/`w` drafts.
func TestFDraftCoveredByQuitGuard(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# amended\n", "# amended\n")
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

// Issue #4: the verify-success confirm row is keyboard-focusable. Default focus is
// Yes (confirmFocus==0); ←/→ (and h/l, Tab) move focus; Enter/Space select the
// focused button; y/n still resolve directly regardless of focus.
func TestConfirmFocusDefaultsToYes(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()

	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved {
		t.Fatal("setup: confirm not set")
	}
	if m.confirmFocus != 0 {
		t.Errorf("default confirm focus must be Yes (0), got %d", m.confirmFocus)
	}
}

func TestConfirmFocusArrowsMove(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// → moves focus to No.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("right arrow must focus No (1), got %d", m.confirmFocus)
	}
	// ← moves focus back to Yes.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = nm.(model)
	if m.confirmFocus != 0 {
		t.Errorf("left arrow must focus Yes (0), got %d", m.confirmFocus)
	}
	// l (vim) focuses No; h focuses Yes.
	nm, _ = m.Update(key("l"))
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("l must focus No (1), got %d", m.confirmFocus)
	}
	nm, _ = m.Update(key("h"))
	m = nm.(model)
	if m.confirmFocus != 0 {
		t.Errorf("h must focus Yes (0), got %d", m.confirmFocus)
	}
	// Tab toggles.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = nm.(model)
	if m.confirmFocus != 1 {
		t.Errorf("tab must toggle focus to No (1), got %d", m.confirmFocus)
	}
}

// Enter selects the FOCUSED button: with focus on No, Enter must DISMISS the confirm
// (No = dismiss now), not generate or follow up.
func TestConfirmEnterSelectsFocused(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# delta\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// Move focus to No, then Enter → dismiss (no follow-up, no generation).
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	nm2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm2.(model)
	if cmd != nil {
		t.Fatal("Enter on focused No must dismiss (no follow-up, no generation)")
	}
	if m.confirmResolved {
		t.Error("Enter must clear the confirm state")
	}
	if m.finalDraft {
		t.Error("Enter on No must NOT mark a finalDraft (that's the Yes path)")
	}
	if fe.calls != 0 {
		t.Errorf("Enter on No must not invoke the producer, got %d calls", fe.calls)
	}
}

// Enter on the DEFAULT focus (Yes) generates the final-playbook draft, and Space
// selects the focused button too.
func TestConfirmSpaceSelectsFocusedYes(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	nm2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("Space on focused Yes must trigger the final-playbook generation")
	}
	if !m.finalDraft {
		t.Error("Space on Yes must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("Space-on-Yes kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// The y/n direct shortcuts still resolve regardless of the focus position.
func TestConfirmYNStillWorkWithFocus(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	// Focus is on No, but `y` must still resolve Yes (final playbook).
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	nm2, cmd := m.Update(key("y"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("y must resolve Yes regardless of focus")
	}
	if !m.finalDraft {
		t.Error("y must mark a finalDraft (Yes path)")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("y kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// The focus-navigation keys must be captured ONLY while the confirm is active: with
// no confirm, ←/→/h/l still scroll and space enters hint mode (not swallowed).
func TestConfirmKeysInertWhenNoConfirm(t *testing.T) {
	// Tall content so vertical scroll has headroom; a runnable block so space → hint.
	// Blank-line-separated paragraphs render as distinct body lines (a markdown
	// paragraph collapses consecutive non-blank lines, which wouldn't overflow).
	m := newModel("T", "```bash\nmake build\n```\n"+strings.Repeat("body paragraph\n\n", 60))
	m.width, m.height = 80, 24
	m.reflow()
	if m.confirmResolved {
		t.Fatal("setup: confirm must not be active")
	}
	// space must enter hint mode over the visible run button — NOT be swallowed as a
	// confirm selection (which only happens while the confirm row is active). Tested at
	// the top so the run button is in the visible window.
	nmSpace, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	mSpace := nmSpace.(model)
	if !mSpace.hintMode {
		t.Error("space with no confirm must enter hint mode, not act as a confirm select")
	}
	// `down` must still scroll vertically — confirm-only keys never intercept nav, and
	// the confirm branch returns early only WHILE confirmResolved.
	nm, _ := m.Update(key("j"))
	m = nm.(model)
	if m.yOff == 0 {
		t.Error("j with no confirm must still scroll down (yOff should advance)")
	}
}

// Issue #4: the focused button is highlighted and the unfocused one dimmed; moving
// focus swaps which label carries the highlight in the rendered confirm row.
func TestConfirmFocusHighlightInRender(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	// Default focus Yes: the Yes label must carry a background highlight (the focus
	// ring), distinct from the No label. We compare the raw (un-stripped) ANSI of the
	// BUTTONS row (the buttons live on their own row now, not the prompt row).
	rowYes := m.confirmButtonsRowString()
	yesFocused := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if !strings.Contains(rowYes, yesFocused) {
		t.Errorf("with Yes focused, the buttons row must contain the highlighted Yes label")
	}
	// Move focus to No; now No carries the highlight and Yes is dimmed.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = nm.(model)
	rowNo := m.confirmButtonsRowString()
	noFocused := m.confirmButtonLabel(confirmNoLabel, "confirm-no", colPeach, true)
	if !strings.Contains(rowNo, noFocused) {
		t.Errorf("with No focused, the buttons row must contain the highlighted No label")
	}
	if rowYes == rowNo {
		t.Error("moving focus must change the rendered buttons row (highlight moved)")
	}
}

// The confirm buttons render as ask-style FILLED controls: the focused one carries a
// GREEN background, the unfocused one a muted (surface) background, and a click-flash
// (bright) wins over both. Each button is a padded cell (label width + 2*pad).
func TestConfirmButtonStyling(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)

	focused := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if !strings.Contains(focused, bgANSI(colGreen)) {
		t.Errorf("focused confirm button must carry the green background")
	}
	unfocused := m.confirmButtonLabel(confirmNoLabel, "confirm-no", colPeach, false)
	if !strings.Contains(unfocused, bgANSI(colSurface1)) {
		t.Errorf("unfocused confirm button must carry the muted surface background")
	}
	if strings.Contains(unfocused, bgANSI(colGreen)) {
		t.Errorf("unfocused confirm button must NOT be green")
	}
	// Both render as PADDED cells: label width + 2*confirmButtonPad.
	if w := len([]rune(strip(focused))); w != len(confirmYesLabel)+2*confirmButtonPad {
		t.Errorf("focused cell width = %d, want %d", w, len(confirmYesLabel)+2*confirmButtonPad)
	}
	// A click-flash wins over both the focused and unfocused styling (no fill bg).
	m.flashKey = "confirm:confirm-yes"
	flashed := m.confirmButtonLabel(confirmYesLabel, "confirm-yes", colGreen, true)
	if strings.Contains(flashed, bgANSI(colGreen)) || strings.Contains(flashed, bgANSI(colSurface1)) {
		t.Errorf("the click-flash must win over the fill backgrounds")
	}
}

// resolveConfirm: Yes generates the final-playbook draft; No DISMISSES and returns nil
// (the command already succeeded, so there is nothing to re-fix).
func TestResolveConfirmYesGeneratesNoDismisses(t *testing.T) {
	// No → dismiss, nil, no generation.
	mn, fn := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	mn.md = "# Troubleshoot\n"
	mn.confirmResolved = true
	if cmd := mn.resolveConfirm(false); cmd != nil {
		t.Errorf("resolveConfirm(false) must return nil (dismiss only)")
	}
	if mn.confirmResolved {
		t.Error("resolveConfirm(false) must clear the confirm state")
	}
	if mn.finalDraft || fn.calls != 0 {
		t.Error("resolveConfirm(false) must not generate")
	}
	// Yes → generate.
	my, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	my.md = "# Troubleshoot\n"
	my.confirmResolved = true
	cmd := my.resolveConfirm(true)
	if cmd == nil {
		t.Fatal("resolveConfirm(true) must generate")
	}
	if my.confirmResolved {
		t.Error("resolveConfirm(true) must clear the confirm state")
	}
	if !my.finalDraft {
		t.Error("resolveConfirm(true) must mark a finalDraft")
	}
}

// `c` generates the playbook for a reached solution — both while the confirm is still
// showing AND after it was dismissed with No.
func TestCKeyGeneratesAfterSolution(t *testing.T) {
	// While the confirm is still showing.
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	if !m.confirmResolved || !m.wrappedUp {
		t.Fatal("setup: confirm/wrappedUp not set")
	}
	nm2, cmd := m.Update(key("c"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("c while the confirm shows must trigger generation")
	}
	if m.confirmResolved {
		t.Error("c must clear the confirm")
	}
	if !m.finalDraft {
		t.Error("c must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("c kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

func TestCKeyGeneratesAfterNoDismiss(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	nm, _ := m.Update(resultMsg{ID: "verify", Exit: 0, Logpath: ""})
	m = nm.(model)
	// Dismiss with No.
	nm, _ = m.Update(key("n"))
	m = nm.(model)
	if m.confirmResolved {
		t.Fatal("setup: No must dismiss the confirm")
	}
	if !m.wrappedUp {
		t.Fatal("setup: wrappedUp must remain set after a No dismiss")
	}
	// `c` still generates.
	nm2, cmd := m.Update(key("c"))
	m = nm2.(model)
	if cmd == nil {
		t.Fatal("c after a No dismiss must still trigger generation")
	}
	if !m.finalDraft {
		t.Error("c must mark a finalDraft")
	}
	m = pumpReArm(t, m, cmd)
	if fe.gotKind != orchestrator.KindReengageFinalPlaybook {
		t.Errorf("c kind = %v, want KindReengageFinalPlaybook", fe.gotKind)
	}
}

// `c` is a no-op before a solution is reached and while a stream is in flight.
func TestCKeyNoOpBeforeSolutionOrWhileStreaming(t *testing.T) {
	m, fe := newReengageEventsModel(t, "# Playbook\n", "# Playbook\n")
	m.md = "# Troubleshoot\n"
	m.inputFifoPath = ""
	m.reflow()
	// Before a solution (wrappedUp false).
	nm, cmd := m.Update(key("c"))
	m = nm.(model)
	if cmd != nil {
		t.Error("c before a solution must be a no-op")
	}
	if m.finalDraft || fe.calls != 0 {
		t.Error("c before a solution must not generate")
	}
	// A solution reached but a stream is in flight.
	m.wrappedUp = true
	m.streaming = true
	nm2, cmd2 := m.Update(key("c"))
	m = nm2.(model)
	if cmd2 != nil {
		t.Error("c while streaming must be a no-op")
	}
	if fe.calls != 0 {
		t.Error("c while streaming must not generate")
	}
}

// Issue #3: a REPLACE final-playbook generation scrolls to the TOP and clears any
// follow-up pin, so the user reads the new document from the start.
func TestFinalPlaybookGenerateScrollsToTop(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\nclean\n", "# Playbook\nclean\n")
	m.md = "# Troubleshoot\n\n```bash {id=verify}\nmake build\n```\n"
	m.inputFifoPath = ""
	m.reflow()
	// Simulate the user having scrolled down with a stale follow-up pin set.
	m.yOff = 7
	m.pinTop = 5
	cmd := m.beginFinalPlaybookGenerate("", "the troubleshoot content")
	if cmd == nil {
		t.Fatal("beginFinalPlaybookGenerate returned nil (re-engagement not wired?)")
	}
	if m.yOff != 0 {
		t.Errorf("REPLACE generate must scroll to top: yOff=%d, want 0", m.yOff)
	}
	if m.pinTop != -1 {
		t.Errorf("REPLACE generate must clear the follow-up pin: pinTop=%d, want -1", m.pinTop)
	}
	if m.follow {
		t.Error("REPLACE generate must keep follow=false (stay at top while streaming)")
	}
}

// Issue #3: regenerate (REPLACE) likewise scrolls to the top and clears the pin.
func TestRegenerateScrollsToTop(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\nfresh\n", "# Playbook\nfresh\n")
	m.md = "# Old playbook\nlots of lines\n"
	m.reflow()
	m.yOff = 4
	m.pinTop = 3
	cmd := m.beginRegenerate()
	if cmd == nil {
		t.Fatal("beginRegenerate returned nil (re-engagement not wired?)")
	}
	if m.yOff != 0 {
		t.Errorf("regenerate must scroll to top: yOff=%d, want 0", m.yOff)
	}
	if m.pinTop != -1 {
		t.Errorf("regenerate must clear the follow-up pin: pinTop=%d, want -1", m.pinTop)
	}
}
