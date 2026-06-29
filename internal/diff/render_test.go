package diff

import (
	"strings"
	"testing"
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
