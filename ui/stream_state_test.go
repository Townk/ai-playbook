package ui

import (
	"os"
	"testing"
)

func TestThinkingLifecycle(t *testing.T) {
	m := newModel("T", "")
	m.thinking = true // streaming path starts implicitly thinking
	m.defaultLabel = "Working…"

	// Text clears the spinner and appends content.
	m2, _ := m.Update(streamEventsMsg{events: []streamEvent{textEvent{"hello"}}})
	m = m2.(model)
	if m.thinking {
		t.Fatal("text must clear thinking")
	}
	if m.md != "hello" {
		t.Fatalf("md = %q, want hello", m.md)
	}

	// A think record re-arms the spinner and resets the timer.
	m.spinTicks = 50
	m2, _ = m.Update(streamEventsMsg{events: []streamEvent{thinkEvent{"Searching…"}}})
	m = m2.(model)
	if !m.thinking || m.thinkLabel != "Searching…" {
		t.Fatalf("want thinking Searching…, got thinking=%v label=%q", m.thinking, m.thinkLabel)
	}
	if m.spinTicks != 0 {
		t.Fatalf("new thinking session must reset timer, got %d", m.spinTicks)
	}

	// A second think record (already thinking) replaces the label WITHOUT
	// resetting the timer.
	m.spinTicks = 33
	m2, _ = m.Update(streamEventsMsg{events: []streamEvent{thinkEvent{"Reading 12 files…"}}})
	m = m2.(model)
	if m.thinkLabel != "Reading 12 files…" {
		t.Fatalf("label = %q, want Reading 12 files…", m.thinkLabel)
	}
	if m.spinTicks != 33 {
		t.Fatalf("in-place label replace must keep the timer running, got %d", m.spinTicks)
	}

	// Empty-label record falls back to the default.
	m2, _ = m.Update(streamEventsMsg{events: []streamEvent{thinkEvent{""}}})
	m = m2.(model)
	if m.thinkLabel != "Working…" {
		t.Fatalf("empty label should fall back to default, got %q", m.thinkLabel)
	}

	// EOF clears thinking and ends streaming.
	m2, _ = m.Update(streamEventsMsg{eof: true})
	m = m2.(model)
	if m.thinking || m.streaming {
		t.Fatalf("EOF must clear thinking+streaming, got thinking=%v streaming=%v", m.thinking, m.streaming)
	}
}

func TestSpinTickAdvancesOnlyWhileThinking(t *testing.T) {
	m := newModel("T", "")
	m.thinking = true
	m2, cmd := m.Update(spinTickMsg{})
	m = m2.(model)
	if m.spinFrame != 1 || m.spinTicks != 1 {
		t.Fatalf("tick should advance frame+ticks, got frame=%d ticks=%d", m.spinFrame, m.spinTicks)
	}
	if cmd == nil {
		t.Fatal("tick while thinking must re-issue the tick command")
	}
	m.thinking = false
	m3, cmd2 := m.Update(spinTickMsg{})
	m = m3.(model)
	if cmd2 != nil {
		t.Fatal("tick while not thinking must stop (nil cmd)")
	}
}

func TestRunClickMarksRunningAndTicks(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.fifoPath = "" // emitAction no-ops without a fifo; we only assert state here
	m = m.markRunning("a")            // helper invoked by the action path
	if m.blockStates["a"].Status != "running" {
		t.Fatalf("run must mark running")
	}
	f0 := m.blockStates["a"].SpinFrame
	m2, _ := m.Update(spinTickMsg{})
	if m2.(model).blockStates["a"].SpinFrame == f0 {
		t.Fatalf("spinTick must advance running blocks")
	}
}

func TestSpinTickStopsWhenNothingRunning(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	_, cmd := m.Update(spinTickMsg{}) // not thinking, no running blocks
	if cmd != nil {
		t.Fatalf("tick must not perpetuate when idle")
	}
}

func TestToggleExpandsRegion(t *testing.T) {
	dir := t.TempDir()
	lp := dir + "/log"
	os.WriteFile(lp, []byte("xTAILx\n"), 0o644)
	m := newModel("T", "```bash {id=a}\nls\n```\n")
	m.width, m.height = 80, 24
	m.blockStates = map[string]blockRunState{"a": {Status: "ok", Logpath: lp, Expanded: false}}
	m.reflow()
	if linesContain(m.lines, "xTAILx") {
		t.Fatal("collapsed must hide the tail")
	}
	m = m.handleToggle("a")
	if !m.blockStates["a"].Expanded {
		t.Fatal("toggle must expand")
	}
	if !linesContain(m.lines, "xTAILx") {
		t.Fatal("expanded must show the tail")
	}
}
