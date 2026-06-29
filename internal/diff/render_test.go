package diff

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func id(code, lang string) string { return code } // identity highlight for tests

func TestRender_SideBySide(t *testing.T) {
	files := []FileDiff{{NewPath: "b/x.txt", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "keep"}, {OpDel, "old"}, {OpAdd, "new"},
	}}}}}
	out := Render(files, 120, id)
	// both old and new content present; laid out in two columns (a vertical separator)
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") || !strings.Contains(out, "keep") {
		t.Fatalf("side-by-side missing content:\n%s", out)
	}
	if !strings.Contains(out, "│") { // a column separator
		t.Fatalf("no two-column separator in side-by-side:\n%s", out)
	}
}

func TestRender_NarrowFallsBackToUnified(t *testing.T) {
	files := []FileDiff{{NewPath: "b/x", Hunks: []Hunk{{Lines: []Line{{OpDel, "old"}, {OpAdd, "new"}}}}}}
	out := Render(files, 30, id)
	// unified: -old / +new prefixed lines, single column (no two-pane separator run)
	if !strings.Contains(out, "-old") || !strings.Contains(out, "+new") {
		t.Fatalf("narrow render not unified:\n%s", out)
	}
}

func TestRender_SideBySide_CellTruncationPreventsWrap(t *testing.T) {
	// A line far wider than the column must not cause any output line to exceed
	// the requested terminal width (lipgloss .Width would word-wrap without truncation).
	longLine := strings.Repeat("a", 200)
	files := []FileDiff{{NewPath: "b/x.go", Hunks: []Hunk{{Lines: []Line{
		{OpContext, longLine},
		{OpDel, longLine + "X"},
		{OpAdd, longLine + "Y"},
	}}}}}
	const width = 120
	out := Render(files, width, id)
	rowCount := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		rowCount++
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("output line width %d exceeds terminal width %d: %q", w, width, line)
		}
	}
	// 3 content rows + 1 header row: no ballooning from wrap
	if rowCount > 10 {
		t.Fatalf("row count %d looks like wrap ballooning (expected ~4)", rowCount)
	}
}
