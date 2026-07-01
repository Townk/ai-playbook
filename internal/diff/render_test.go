package diff

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func id(code, lang string) string { return code } // identity highlight for tests

// hlReset mimics chroma's terminal16m output: it wraps content in a per-token
// reset so tests can assert the cell background survives (is re-asserted after)
// the reset.
func hlReset(code, lang string) string { return "\x1b[38;2;1;2;3m" + code + "\x1b[0m" }

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

func TestRender_SideBySide_CellBackgrounds(t *testing.T) {
	// Each cell must carry an explicit background: the dialog bg for context/blank
	// cells and the divider, the add/del tints for changed cells. The bg must also
	// be re-asserted after the highlighter's per-token reset (F12/F13).
	files := []FileDiff{{OldPath: "a/x.go", NewPath: "b/x.go", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "keep"}, {OpDel, "old"}, {OpAdd, "new"},
	}}}}}
	out := Render(files, 120, hlReset)

	if !strings.Contains(out, dialogBgANSI) {
		t.Errorf("context/blank cells missing dialog bg %q:\n%q", dialogBgANSI, out)
	}
	if !strings.Contains(out, addBgANSI) {
		t.Errorf("add cell missing add tint %q:\n%q", addBgANSI, out)
	}
	if !strings.Contains(out, delBgANSI) {
		t.Errorf("del cell missing del tint %q:\n%q", delBgANSI, out)
	}
	// bg re-asserted after every highlighter reset: no "\x1b[0m" is left without a
	// following bg re-injection inside a cell.
	if !strings.Contains(out, "\x1b[0m"+dialogBgANSI) &&
		!strings.Contains(out, "\x1b[0m"+addBgANSI) &&
		!strings.Contains(out, "\x1b[0m"+delBgANSI) {
		t.Errorf("bg not re-asserted after highlighter reset:\n%q", out)
	}
}

func TestRender_SideBySide_HeaderSplitTwoColumns(t *testing.T) {
	// The file header must be laid out as two columns matching the panes, joined by
	// the same " │ " divider, so the divider is one unbroken vertical line (F14).
	files := []FileDiff{{OldPath: "a/x.go", NewPath: "b/x.go", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "keep"},
	}}}}}
	out := Render(files, 120, id)
	header := strings.SplitN(out, "\n", 2)[0]

	if !strings.Contains(header, "--- a/x.go") || !strings.Contains(header, "+++ b/x.go") {
		t.Errorf("header missing split old/new paths:\n%q", header)
	}
	if !strings.Contains(header, " │ ") {
		t.Errorf("header missing continuous pane divider:\n%q", header)
	}
	// The divider column must be identical on the header and the content rows so │
	// aligns. Compare the display column of │ on the header vs the next row.
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) < 2 {
		t.Fatalf("expected header + content rows, got:\n%q", out)
	}
	colOf := func(s string) int { return lipgloss.Width(s[:strings.Index(s, "│")]) }
	if colOf(rows[0]) != colOf(rows[1]) {
		t.Errorf("divider not aligned: header col %d vs content col %d", colOf(rows[0]), colOf(rows[1]))
	}
}

func TestRows_Numbering(t *testing.T) {
	// A context/del/add sequence starting at old=10/new=12: context carries both
	// numbers, the del/add pair carries left=old and right=new (incremented past
	// the context line).
	files := []FileDiff{{OldPath: "a/x", NewPath: "b/x", Hunks: []Hunk{{
		OldStart: 10, NewStart: 12,
		Lines: []Line{{OpContext, "ctx"}, {OpDel, "old"}, {OpAdd, "new"}},
	}}}}
	rows := Rows(files)
	if len(rows) != 3 { // header + context + one paired del/add row
		t.Fatalf("Rows len = %d, want 3:\n%#v", len(rows), rows)
	}
	if !rows[0].Header || rows[0].Left != "--- a/x" || rows[0].Right != "+++ b/x" {
		t.Fatalf("row 0 not the file header: %#v", rows[0])
	}
	ctx := rows[1]
	if ctx.LeftNo != 10 || ctx.RightNo != 12 || ctx.Left != "ctx" || ctx.Right != "ctx" {
		t.Fatalf("context row = %#v, want both numbers 10/12", ctx)
	}
	chg := rows[2]
	if chg.LeftNo != 11 || chg.LeftOp != OpDel || chg.Left != "old" {
		t.Fatalf("del side = %#v, want LeftNo 11 OpDel old", chg)
	}
	if chg.RightNo != 13 || chg.RightOp != OpAdd || chg.Right != "new" {
		t.Fatalf("add side = %#v, want RightNo 13 OpAdd new", chg)
	}
}

func TestRows_UnpairedDelBlankRight(t *testing.T) {
	// Two dels, one add: the second del row has a blank right side (number 0).
	files := []FileDiff{{Hunks: []Hunk{{
		OldStart: 1, NewStart: 1,
		Lines: []Line{{OpDel, "d1"}, {OpDel, "d2"}, {OpAdd, "a1"}},
	}}}}
	rows := Rows(files)
	// rows[0] header, rows[1] d1/a1, rows[2] d2/blank
	last := rows[len(rows)-1]
	if last.Left != "d2" || last.LeftNo != 2 || last.LeftOp != OpDel {
		t.Fatalf("second del row = %#v, want d2/LeftNo2/OpDel", last)
	}
	if last.Right != "" || last.RightNo != 0 {
		t.Fatalf("unpaired del must have a blank right (No 0): %#v", last)
	}
}

func TestRenderRow_GutterFixedWidthRightAligned(t *testing.T) {
	// The gutter is gutterW wide, right-aligned, in the dim gutter foreground.
	row := Row{LeftNo: 5, Left: "x", LeftOp: OpContext, RightNo: 42, Right: "y", RightOp: OpContext}
	out := RenderRow(row, 0, 0, 10, 3, "", id)
	if !strings.Contains(out, gutterFgANSI+"  5") { // 5 right-aligned in width 3
		t.Fatalf("left gutter not dim/right-aligned to width 3:\n%q", out)
	}
	if !strings.Contains(out, gutterFgANSI+" 42") { // 42 right-aligned in width 3
		t.Fatalf("right gutter not dim/right-aligned to width 3:\n%q", out)
	}
	// A header row shows no numbers: gutterW blanks in the dim fg.
	hdr := RenderRow(Row{Left: "--- a", Right: "+++ b", Header: true}, 0, 0, 10, 3, "", id)
	if !strings.Contains(hdr, gutterFgANSI+"   ") {
		t.Fatalf("header gutter must be blank (3 spaces):\n%q", hdr)
	}
}

func TestRenderRow_WindowRevealsTail(t *testing.T) {
	// A del wider than textCol, windowed by leftXOff, reveals its tail.
	long := strings.Repeat("a", 10) + "TAIL"
	row := Row{LeftNo: 1, Left: long, LeftOp: OpDel}
	// leftXOff=10 drops the 10 leading 'a's, leaving "TAIL" (fits textCol=4).
	out := RenderRow(row, 10, 0, 4, 2, "", id)
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("windowed del did not reveal its tail:\n%q", out)
	}
	if strings.Contains(out, "aaaa") {
		t.Fatalf("windowed del still shows its head:\n%q", out)
	}
	// The del cell carries the del tint, the divider carries the dialog bg.
	if !strings.Contains(out, delBgANSI) || !strings.Contains(out, dialogBgANSI+" │ ") {
		t.Fatalf("del tint or divider missing:\n%q", out)
	}
}

func TestRenderRow_HeaderPinnedAndColored(t *testing.T) {
	h := Row{Left: "--- a/x", Right: "+++ b/x", Header: true}
	// Pinned: a header row must render identically regardless of the scroll offset.
	if RenderRow(h, 0, 0, 20, 2, "", id) != RenderRow(h, 9, 9, 20, 2, "", id) {
		t.Error("header row must be pinned — the horizontal offset must not scroll it")
	}
	// Colored: left path red, right path green.
	out := RenderRow(h, 0, 0, 20, 2, "", id)
	if !strings.Contains(out, headerDelFgANSI) {
		t.Error("header left ('--- old') path must be red")
	}
	if !strings.Contains(out, headerAddFgANSI) {
		t.Error("header right ('+++ new') path must be green")
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
