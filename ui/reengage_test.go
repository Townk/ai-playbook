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
