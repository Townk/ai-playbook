package ui

import "testing"

// TestStreamCoalescesReflow verifies that streamed text chunks are appended
// cheaply and the (expensive) reflow is deferred to a single render tick rather
// than run per chunk.
func TestStreamCoalescesReflow(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24

	// Chunk 1: text appended, marked dirty, render tick scheduled — but NOT
	// reflowed yet.
	m2, cmd := m.Update(streamEventsMsg{events: []streamEvent{textEvent{"# A\n\n"}}})
	m = m2.(model)
	if !m.dirty {
		t.Fatal("a text chunk should mark the model dirty")
	}
	if len(m.lines) != 0 {
		t.Fatalf("reflow must be coalesced, not per-chunk; got %d lines", len(m.lines))
	}
	if !m.renderScheduled {
		t.Fatal("first dirty chunk should schedule a render tick")
	}
	if cmd == nil {
		t.Fatal("expected reader + render-tick commands")
	}

	// Chunk 2: still no reflow; the render tick stays scheduled exactly once.
	m2, _ = m.Update(streamEventsMsg{events: []streamEvent{textEvent{"body\n"}}})
	m = m2.(model)
	if len(m.lines) != 0 {
		t.Fatal("second chunk must also defer reflow until the render tick")
	}
	if !m.renderScheduled {
		t.Fatal("render tick should remain scheduled (not re-scheduled per chunk)")
	}

	// The render tick flushes all accumulated text in one reflow.
	m2, _ = m.Update(renderTickMsg{})
	m = m2.(model)
	if m.dirty || m.renderScheduled {
		t.Fatal("render tick should clear dirty + renderScheduled")
	}
	if len(m.lines) == 0 {
		t.Fatal("render tick should reflow the accumulated buffer")
	}
}

// TestEOFFlushesImmediately verifies that EOF renders the final content right
// away (no waiting on a render tick) and stops the stream.
func TestEOFFlushesImmediately(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m2, cmd := m.Update(streamEventsMsg{events: []streamEvent{textEvent{"# Final\n"}}, eof: true})
	m = m2.(model)
	if m.dirty {
		t.Fatal("EOF must flush pending text (clear dirty)")
	}
	if len(m.lines) == 0 {
		t.Fatal("EOF must reflow the final content immediately")
	}
	if m.streaming || m.thinking {
		t.Fatal("EOF clears streaming + thinking")
	}
	if cmd != nil {
		t.Fatal("EOF must not re-issue the reader")
	}
}
