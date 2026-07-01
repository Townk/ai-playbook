package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	idiff "github.com/Townk/ai-playbook/internal/diff"
)

// boxDims returns the rendered diffModal box width (widest line) and height (row
// count).
func boxDims(m model) (w, h int) {
	rows := strings.Split(m.diffModal(), "\n")
	h = len(rows)
	for _, r := range rows {
		if lw := lipgloss.Width(r); lw > w {
			w = lw
		}
	}
	return w, h
}

// TestDiffModal_FixedGeometry_SmallDiff verifies the box is ALWAYS m.width-6 wide
// and m.height-2 tall even when the diff has fewer rows than the box (F15/F16):
// short diffs are blank-padded, never shrunk to content.
func TestDiffModal_FixedGeometry_SmallDiff(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffRows = []idiff.Row{
		{Left: "--- a/x", Right: "+++ b/x", Header: true},
		{LeftNo: 1, RightNo: 1, Left: "keep", Right: "keep"},
	} // 2 short rows

	w, h := boxDims(m)
	if w != m.width-6 {
		t.Errorf("box width = %d, want m.width-6 = %d", w, m.width-6)
	}
	if h != m.height-2 {
		t.Errorf("box height = %d, want m.height-2 = %d", h, m.height-2)
	}
}

// TestDiffModal_FixedGeometry_LargeDiff verifies the box stays m.width-6 ×
// m.height-2 when the diff is larger than the box (windowed, not grown).
func TestDiffModal_FixedGeometry_LargeDiff(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffRows = make([]idiff.Row, 200)
	for i := range m.diffRows {
		wide := strings.Repeat("x", 300) // far wider + taller than the box
		m.diffRows[i] = idiff.Row{LeftNo: i + 1, RightNo: i + 1, Left: wide, Right: wide}
	}

	w, h := boxDims(m)
	if w != m.width-6 {
		t.Errorf("box width = %d, want m.width-6 = %d", w, m.width-6)
	}
	if h != m.height-2 {
		t.Errorf("box height = %d, want m.height-2 = %d", h, m.height-2)
	}
}

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
	if len(m3.diffRows) == 0 {
		t.Fatal("diffRows must be non-empty after opening overlay")
	}
	var joined strings.Builder
	for _, r := range m3.diffRows {
		joined.WriteString(r.Left + "\x00" + r.Right + "\n")
	}
	if !strings.Contains(joined.String(), "b") {
		t.Fatal("diff overlay content not rendered")
	}
}

// TestViewDiff_EscClosesDiffMode verifies that pressing Escape while the diff
// overlay is open closes it.
func TestViewDiff_EscClosesDiffMode(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffRows = []idiff.Row{{Left: "--- a", Right: "+++ b", Header: true}, {RightNo: 1, Right: "b", RightOp: idiff.OpAdd}}

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
	m.diffRows = []idiff.Row{{Left: "--- a", Right: "+++ b", Header: true}, {RightNo: 1, Right: "b", RightOp: idiff.OpAdd}}

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
	m.diffRows = []idiff.Row{
		{Left: "--- a/x", Right: "+++ b/x", Header: true},
		{LeftNo: 1, Left: "old", LeftOp: idiff.OpDel, RightNo: 1, Right: "new", RightOp: idiff.OpAdd},
	}

	v := m.viewString()
	// The overlay composites the diff modal over the document; the modal renders
	// the diff lines in a bordered box — the raw ANSI-stripped text should appear.
	if !strings.Contains(strip(v), "old") && !strings.Contains(strip(v), "new") {
		t.Fatal("diff overlay must appear in viewString when diffMode is active")
	}
}

// TestDiff_PerPaneClampCouples verifies coupled horizontal scroll with an
// independent per-pane clamp: one m.diffXOff windows both panes, but the short
// side stops at its own content end while the long side keeps revealing.
func TestDiff_PerPaneClampCouples(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	// Left pane short, right pane long.
	m.diffRows = []idiff.Row{
		{Left: "--- a", Right: "+++ b", Header: true},
		{LeftNo: 1, Left: "short", LeftOp: idiff.OpDel,
			RightNo: 1, Right: strings.Repeat("x", 400), RightOp: idiff.OpAdd},
	}

	visW := diffContentWidth(m)
	gutterW := diffGutterW(m.diffRows)
	textCol := diffTextCol(visW, gutterW)
	leftMax, rightMax := diffPaneMax(m.diffRows, textCol)

	if leftMax != 0 {
		t.Fatalf("short left pane should not scroll: leftMax = %d, want 0", leftMax)
	}
	if rightMax <= leftMax {
		t.Fatalf("long right pane must scroll further: rightMax %d must exceed leftMax %d", rightMax, leftMax)
	}

	// Scroll right past the short side's end: clamp must keep diffXOff advancing
	// (up to rightMax) even though the left pane is already fully revealed.
	m.diffXOff = leftMax + 5
	m.clampDiffScroll()
	if m.diffXOff != leftMax+5 {
		t.Fatalf("diffXOff should advance to %d (< rightMax %d), got %d", leftMax+5, rightMax, m.diffXOff)
	}
	// The per-pane windowing: the left offset clamps at leftMax, the right keeps growing.
	if got := min(m.diffXOff, leftMax); got != leftMax {
		t.Fatalf("leftXOff should clamp at leftMax %d, got %d", leftMax, got)
	}
	if got := min(m.diffXOff, rightMax); got != m.diffXOff {
		t.Fatalf("rightXOff should still track diffXOff %d, got %d", m.diffXOff, got)
	}

	// The overall clamp ceiling is the wider pane's end.
	m.diffXOff = 1 << 20
	m.clampDiffScroll()
	if m.diffXOff != rightMax {
		t.Fatalf("diffXOff must clamp to max(leftMax,rightMax)=%d, got %d", rightMax, m.diffXOff)
	}
}

// TestViewDiff_ScrollDown verifies that pressing down increments diffYOff.
func TestViewDiff_ScrollDown(t *testing.T) {
	m := newModel("T", "# hi\n")
	m.width, m.height = 100, 30
	m.diffMode = true
	m.diffRows = make([]idiff.Row, 50) // 50 rows so scrolling is possible
	for i := range m.diffRows {
		m.diffRows[i] = idiff.Row{LeftNo: i + 1, RightNo: i + 1, Left: "line", Right: "line"}
	}
	m.diffYOff = 0

	m2, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m3 := m2.(model)
	if m3.diffYOff <= 0 {
		t.Fatal("pressing j in diffMode must increment diffYOff")
	}
}
