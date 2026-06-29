package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestViewDiff_NoMuxOpensOverlay verifies that clicking the diff button when
// there is no mux asker (no-mux path) opens the in-viewer diff overlay rather
// than emitting an orchestrator action.
func TestViewDiff_NoMuxOpensOverlay(t *testing.T) {
	// A diff block in markdown → registers a "diff" button after reflow.
	md := "```diff {id=fix}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n"
	m := newModel("T", md)
	m.width, m.height = 100, 30
	m.asker = nil // no-mux: no request-input float
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "diff")
	if b == nil {
		t.Fatal("diff button must be registered after reflow")
	}

	// Simulate a mouse click on the diff button.
	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	m3 := m2.(model)

	if !m3.diffMode {
		t.Fatal("view-diff on no-mux must open the in-viewer diff overlay")
	}
	if len(m3.diffLines) == 0 {
		t.Fatal("diffLines must be non-empty after opening overlay")
	}
	if !strings.Contains(strings.Join(m3.diffLines, "\n"), "b") {
		t.Fatal("diff overlay content not rendered")
	}
}

// TestViewDiff_EscClosesDiffMode verifies that pressing Escape while the diff
// overlay is open closes it.
func TestViewDiff_EscClosesDiffMode(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffLines = []string{"--- a", "+++ b", "+b"}

	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Text: "esc"})
	m3 := m2.(model)
	if m3.diffMode {
		t.Fatal("esc must close the diff overlay")
	}
}

// TestViewDiff_QClosesDiffMode verifies that pressing q while the diff overlay
// is open closes it.
func TestViewDiff_QClosesDiffMode(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffLines = []string{"--- a", "+++ b", "+b"}

	m2, _ := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m3 := m2.(model)
	if m3.diffMode {
		t.Fatal("q must close the diff overlay")
	}
}

// TestViewDiff_OverlayAppearsInView verifies that viewString includes some
// content from the diff when diffMode is active.
func TestViewDiff_OverlayAppearsInView(t *testing.T) {
	m := newModel("T", "# Playbook\n\nbody text\n")
	m.width, m.height = 100, 30
	m.reflow()
	m.diffMode = true
	m.diffLines = []string{"--- a/x  +++ b/x", "-old", "+new"}

	v := m.viewString()
	// The overlay composites the diff modal over the document; the modal renders
	// the diff lines in a bordered box — the raw ANSI-stripped text should appear.
	if !strings.Contains(strip(v), "old") && !strings.Contains(strip(v), "new") {
		t.Fatal("diff overlay must appear in viewString when diffMode is active")
	}
}

// TestViewDiff_ScrollDown verifies that pressing down increments diffYOff.
func TestViewDiff_ScrollDown(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffLines = make([]string, 50) // 50 lines so scrolling is possible
	for i := range m.diffLines {
		m.diffLines[i] = "line"
	}
	m.diffYOff = 0

	m2, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m3 := m2.(model)
	if m3.diffYOff <= 0 {
		t.Fatal("pressing j in diffMode must increment diffYOff")
	}
}
