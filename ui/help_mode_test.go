package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func key(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

func TestStatusBarPerMode(t *testing.T) {
	m := newModel("T", "hi")
	if !strings.Contains(m.statusBar(), "?: keys") || !strings.Contains(m.statusBar(), "\U000F1050") {
		t.Fatalf("normal status bar wrong: %q", strip(m.statusBar()))
	}
	cancel := "\U000F12B7"
	hm := m
	hm.hintMode = true
	if !strings.Contains(strip(hm.statusBar()), "cancel") || !strings.Contains(hm.statusBar(), cancel) {
		t.Fatalf("hint status bar wrong: %q", strip(hm.statusBar()))
	}
	pm := m
	pm.helpMode = true
	if pm.statusBar() != hm.statusBar() {
		t.Fatal("help status bar must equal the hint cancel bar")
	}
}

// TestHelpTextDims tests the visible-text dims with content-capped values.
func TestHelpTextDims(t *testing.T) {
	// Generous pane: content fits, no scrollbars.
	m := newModel("T", "hi")
	m.width, m.height = 120, 60
	w, h, needV, needH := m.helpTextDims()
	if w < 1 || h < 1 {
		t.Fatalf("generous pane gave non-positive dims: %d x %d", w, h)
	}
	if needV || needH {
		t.Fatalf("generous pane should need no scrollbars: needV=%v needH=%v", needV, needH)
	}
	// Content fits → dims equal the content size (not the cap).
	if want := MaxWideWidth(m.helpLines); w != want {
		t.Fatalf("generous pane: textW %d != contentW %d (should be content-sized)", w, want)
	}
	if want := len(m.helpLines); h != want {
		t.Fatalf("generous pane: textH %d != content height %d (should be content-sized)", h, want)
	}

	// Tiny pane: both dims floored at 1.
	mt := newModel("T", "hi")
	mt.width, mt.height = 8, 5
	tw, th, _, _ := mt.helpTextDims()
	if tw < 1 || th < 1 {
		t.Fatalf("tiny pane must still give ≥1x1: %d x %d", tw, th)
	}

	// Medium pane where caps kick in: text width within width-14 (minus the vbar
	// column when shown), text height within m.height-8.
	mc := newModel("T", "hi")
	mc.width, mc.height = 40, 20
	tw2, th2, nV, _ := mc.helpTextDims()
	if capW := mc.width - 14 - boolToInt(nV); capW > 0 && tw2 > capW {
		t.Fatalf("medium pane: textW %d exceeds cap %d", tw2, capW)
	}
	if capH := mc.height - 8; capH > 0 && th2 > capH {
		t.Fatalf("medium pane: textH %d exceeds cap %d", th2, capH)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestHelpClampScroll(t *testing.T) {
	m := newModel("T", "hi")
	m.width, m.height = 80, 24
	m.helpYOff, m.helpXOff = 9999, 9999
	m.clampHelpScroll()
	if m.helpYOff < 0 || m.helpXOff < 0 {
		t.Fatal("offsets must not go negative")
	}
	_, textH, _, _ := m.helpTextDims()
	if max := len(m.helpLines) - textH; max >= 0 && m.helpYOff > max {
		t.Fatalf("helpYOff %d exceeds max %d", m.helpYOff, max)
	}
}

func TestHelpModalFitsAllPanes(t *testing.T) {
	for _, d := range [][2]int{{80, 24}, {50, 18}, {30, 12}, {120, 40}, {28, 11}, {24, 9}, {20, 8}} {
		m := newModel("T", "hi")
		m.width, m.height = d[0], d[1]
		m.helpMode = true
		lines := strings.Split(m.helpModal(), "\n")
		// When the pane can hold the modal's minimum box (5 rows: 2 border + 2 pad +
		// 1 text), it must fit within the 4-col side margins and the 2-line
		// top/bottom margins. (At smaller panes the overlay clips it.)
		if area := m.height - 4; area >= 5 && len(lines) > area {
			t.Fatalf("%dx%d: box height %d exceeds area %d (H-4)", d[0], d[1], len(lines), area)
		}
		w := lipgloss.Width(lines[0])
		if cap := m.width - 8; cap >= 8 && w > cap {
			t.Fatalf("%dx%d: box width %d exceeds cap %d (W-8)", d[0], d[1], w, cap)
		}
		// All rows are the same width (rectangular box).
		for i, line := range lines {
			if lw := lipgloss.Width(line); lw != w {
				t.Fatalf("%dx%d: line %d width %d != box width %d", d[0], d[1], i, lw, w)
			}
		}
	}
}

// TestHelpOverlayShowsDocument verifies the modal is an overlay: the document
// markdown still renders behind it, and the modal title shows on top.
func TestHelpOverlayShowsDocument(t *testing.T) {
	m := newModel("T", "# Doc Heading\n\nbody paragraph alpha bravo charlie delta echo foxtrot")
	// A tall pane so the modal (content-sized) doesn't cover the whole body — the
	// document then shows above/below the centered overlay.
	m.width, m.height = 80, 60
	m.reflow()
	m.helpMode = true
	out := strip(m.viewString())
	if !strings.Contains(out, "Doc Heading") {
		t.Fatal("document content must still render behind the help modal")
	}
	if !strings.Contains(out, "Pager guide") {
		t.Fatal("modal title must render on top of the document")
	}
}

func TestViewHeightAllModes(t *testing.T) {
	for _, d := range [][2]int{{80, 24}, {50, 18}, {120, 40}} {
		base := newModel("T", "hello\nworld")
		base.width, base.height = d[0], d[1]
		base.reflow()
		for _, mode := range []string{"normal", "hint", "help"} {
			m := base
			switch mode {
			case "hint":
				m.hintMode = true
			case "help":
				m.helpMode = true
			}
			got := m.viewString()
			if h := lipgloss.Height(got); h != m.height {
				t.Fatalf("%dx%d %s: View height %d != %d", d[0], d[1], mode, h, m.height)
			}
		}
	}
}

// TestViewThinkingMode verifies the three spinner-view invariants:
//
//	(a) total view height equals m.height (frame discipline)
//	(b) every rendered body line has equal display width (I-2: scrollbar alignment)
//	(c) with empty content the spinner appears within the first body row, not
//	    pinned to the bottom (I-1: position under last real content line)
func TestViewThinkingMode(t *testing.T) {
	dims := [][2]int{{80, 24}, {50, 18}, {120, 40}}

	t.Run("height_invariant", func(t *testing.T) {
		for _, d := range dims {
			for _, tc := range []struct {
				name string
				md   string
			}{
				{"empty", ""},
				{"short", "hello\nworld"},
				{"overflow", strings.Repeat("line\n", 100)},
			} {
				m := newModel("T", tc.md)
				m.width, m.height = d[0], d[1]
				m.thinking = true
				m.thinkLabel = "Working…"
				m.spinTicks = 15 // 1s
				m.reflow()
				if tc.md == "overflow" {
					// Simulate follow: scroll to bottom.
					m.yOff = len(m.lines)
					m.clampScroll()
				}
				got := m.viewString()
				if h := lipgloss.Height(got); h != m.height {
					t.Errorf("%dx%d thinking/%s: View height %d != %d", d[0], d[1], tc.name, h, m.height)
				}
			}
		}
	})

	t.Run("uniform_body_width", func(t *testing.T) {
		// Use content that triggers a vertical scrollbar (overflow), which is the
		// scenario where I-2 manifests: content rows get vscrollCell but the
		// spinner row was missing it, making it 2 columns narrower.
		for _, d := range dims {
			m := newModel("T", strings.Repeat("line\n", 100))
			m.width, m.height = d[0], d[1]
			m.thinking = true
			m.thinkLabel = "Working…"
			m.reflow()
			m.yOff = len(m.lines)
			m.clampScroll()
			got := m.viewString()
			lines := strings.Split(got, "\n")
			// Skip the leading blank, header, top-pad, bottom-pad, and status bar
			// (the last two non-body lines). Body rows are lines[3 : 3+m.body()].
			bodyStart := 3
			bodyEnd := bodyStart + m.body()
			if bodyEnd > len(lines) {
				t.Fatalf("%dx%d: not enough lines in view (%d), want at least %d", d[0], d[1], len(lines), bodyEnd)
			}
			bodyLines := lines[bodyStart:bodyEnd]
			if len(bodyLines) == 0 {
				t.Fatalf("%dx%d: no body lines", d[0], d[1])
			}
			wantW := lipgloss.Width(bodyLines[0])
			for i, l := range bodyLines {
				if w := lipgloss.Width(l); w != wantW {
					t.Errorf("%dx%d: body line %d width %d != %d (line 0 width); spinner may be missing vscrollCell", d[0], d[1], i, w, wantW)
				}
			}
		}
	})

	t.Run("spinner_position_empty_content", func(t *testing.T) {
		// With no content the spinner should appear at body row 0 (just under the
		// title), NOT at the bottom row (m.body()-1). I-1 caused it to always pin
		// to the bottom because Window() right-pads and len(rows)==m.body() always.
		m := newModel("T", "")
		m.width, m.height = 80, 24
		m.thinking = true
		m.thinkLabel = "Working…"
		m.spinTicks = 0
		m.reflow()
		got := m.viewString()
		lines := strings.Split(got, "\n")
		bodyStart := 3
		bodyEnd := bodyStart + m.body()
		if bodyEnd > len(lines) {
			t.Fatalf("not enough lines in view (%d)", len(lines))
		}
		bodyLines := lines[bodyStart:bodyEnd]
		// The spinner text "⠋" (first frame) and "0s" should appear in one of
		// the first 2 body rows (row 0 = right under header), NOT only at the
		// last body row.
		spinnerFound := -1
		for i, l := range bodyLines {
			if strings.Contains(strip(l), "0s") {
				spinnerFound = i
				break
			}
		}
		if spinnerFound < 0 {
			t.Fatal("spinner text not found in body")
		}
		lastRow := m.body() - 1
		if spinnerFound == lastRow && lastRow > 1 {
			t.Errorf("spinner pinned to bottom row %d; want it at row 0 (just under title) for empty content", lastRow)
		}
		if spinnerFound != 0 {
			t.Errorf("empty content: spinner at body row %d, want 0", spinnerFound)
		}
	})

	t.Run("spinner_position_short_content", func(t *testing.T) {
		// With 2 lines of content (short, no overflow), spinner should appear at
		// body row 2 (just below the last content line), not at the bottom.
		m := newModel("T", "line1\nline2")
		m.width, m.height = 80, 24
		m.thinking = true
		m.thinkLabel = "Working…"
		m.reflow()
		got := m.viewString()
		lines := strings.Split(got, "\n")
		bodyStart := 3
		bodyEnd := bodyStart + m.body()
		if bodyEnd > len(lines) {
			t.Fatalf("not enough lines in view (%d)", len(lines))
		}
		bodyLines := lines[bodyStart:bodyEnd]
		spinnerFound := -1
		for i, l := range bodyLines {
			if strings.Contains(strip(l), "Working…") {
				spinnerFound = i
				break
			}
		}
		if spinnerFound < 0 {
			t.Fatal("spinner text not found in body")
		}
		// With 2 lines of content, spinner should be at body row 2 (0-indexed).
		wantRow := len(m.lines)
		lastRow := m.body() - 1
		if spinnerFound == lastRow && lastRow > wantRow {
			t.Errorf("spinner pinned to bottom row %d; want row %d (just below last content line)", lastRow, wantRow)
		}
		if spinnerFound != wantRow {
			t.Errorf("short content: spinner at body row %d, want %d", spinnerFound, wantRow)
		}
	})
}

func TestHelpTransitions(t *testing.T) {
	m := newModel("T", "hi")
	m.width, m.height = 80, 24
	// ? opens help, zeroes offsets
	m.helpYOff, m.helpXOff = 3, 3
	m2, _ := m.Update(key("?"))
	hm := m2.(model)
	if !hm.helpMode || hm.helpYOff != 0 || hm.helpXOff != 0 {
		t.Fatalf("? should open help and zero offsets: %+v", hm.helpMode)
	}
	// j scrolls help, not the document
	hm.helpLines = append(hm.helpLines, make([]Line, 200)...) // ensure scrollable
	docY := hm.yOff
	m3, _ := hm.Update(key("j"))
	hm3 := m3.(model)
	if hm3.yOff != docY {
		t.Fatal("help scroll must not move the document")
	}
	// esc / q / ? close help
	for _, k := range []string{"esc", "q", "?"} {
		cm, _ := hm3.Update(tea.KeyPressMsg{Text: k})
		if cm.(model).helpMode {
			t.Fatalf("%q must close help", k)
		}
	}
}

// TestHelpSlashAlignment verifies that " / " appears at the same column in
// every binding that contains it (ANSI stripped).
func TestHelpSlashAlignment(t *testing.T) {
	lines := buildHelpLines()
	sepCol := -1
	for _, l := range lines {
		plain := strip(l.Text)
		// Binding lines are the only ones containing " / "; its first occurrence
		// is the key separator (the key column is left of the description). No
		// indent gate — bindings now align at the content's left edge.
		idx := strings.Index(plain, " / ")
		if idx < 0 {
			continue
		}
		// Use rune count so multi-byte chars measure correctly.
		col := utf8.RuneCountInString(plain[:idx])
		if sepCol == -1 {
			sepCol = col
		} else if col != sepCol {
			t.Fatalf("'/ ' not aligned: col %d vs %d in %q", col, sepCol, plain)
		}
	}
	if sepCol == -1 {
		t.Fatal("no ' / ' found in help lines")
	}
}

// TestBandReinjectsBg guards the mechanism the modal uses for a uniform
// background: band must prefix the bg and re-apply it after every color reset,
// so styled segments don't leave gaps on the terminal's default background.
func TestBandReinjectsBg(t *testing.T) {
	bg := "\x1b[48;2;1;2;3m"
	got := band("\x1b[38;2;9;9;9mfg\x1b[0m plain", bg, 0)
	if !strings.HasPrefix(got, bg) {
		t.Fatal("band must prefix the bg")
	}
	if !strings.Contains(got, "\x1b[0m"+bg) {
		t.Fatal("band must re-inject the bg after each reset")
	}
}

// TestHelpContentSizeWithinMargins verifies that on a generous pane the box is
// content-sized and that the text dims are within the margin caps.
func TestHelpContentSizeWithinMargins(t *testing.T) {
	m := newModel("T", "hi")
	m.width, m.height = 120, 60
	textW, textH, _, _ := m.helpTextDims()

	// Content-sized: equal to the actual content dimensions.
	if want := MaxWideWidth(m.helpLines); textW != want {
		t.Fatalf("textW %d != content width %d", textW, want)
	}
	if want := len(m.helpLines); textH != want {
		t.Fatalf("textH %d != content height %d", textH, want)
	}

	// Within margin caps (width-14 wide; H-8 tall = modal area H-4 minus chrome 4).
	if textW > m.width-14 {
		t.Fatalf("textW %d exceeds cap width(%d)-14 = %d", textW, m.width, m.width-14)
	}
	if textH > m.height-8 {
		t.Fatalf("textH %d exceeds cap H(%d)-8 = %d", textH, m.height, m.height-8)
	}
}

// TestHelpModalScrollThreshold pins the rule: the modal fits without scrolling
// exactly when m.height-8 >= len(helpLines) — the modal area (H-4) minus the box
// chrome (border 2 + padding 2) holds the whole cheatsheet (title included).
func TestHelpModalScrollThreshold(t *testing.T) {
	m := newModel("T", "hi")
	m.width = 80
	// Smallest height where the full cheatsheet fits without scrolling.
	m.height = 24
	for ; m.height-8 < len(m.helpLines); m.height++ {
	}
	if _, textH, needV, _ := m.helpTextDims(); needV || textH != len(m.helpLines) {
		t.Fatalf("at H=%d modal should not scroll, textH=%d needV=%v want textH=%d", m.height, textH, needV, len(m.helpLines))
	}
	m.height--
	if _, _, needV, _ := m.helpTextDims(); !needV {
		t.Fatalf("at H=%d modal should scroll (needV)", m.height)
	}
}

// TestHelpModalScrollbarUsesMantle verifies that when the help modal renders a
// horizontal scrollbar (needH=true), the scrollbar row uses colMantle
// (#181825 → 48;2;24;24;37) as its background and NOT colCodeBg
// (#282C41 → 48;2;40;44;65).
func TestHelpModalScrollbarUsesMantle(t *testing.T) {
	// width=30 gives cw=26, innerW=12, contentW=42 → needH=true.
	m := newModel("T", "hi")
	m.width, m.height = 30, 24
	m.helpMode = true

	if _, _, _, needH := m.helpTextDims(); !needH {
		t.Skip("needH not triggered at this size; adjust test dimensions")
	}

	out := m.helpModal()

	const mantleBgParams = "48;2;24;24;37"  // colMantle #181825
	const codeBgParams   = "48;2;40;44;65"  // colCodeBg #282C41
	if !strings.Contains(out, mantleBgParams) {
		t.Fatalf("help modal scrollbar row must use colMantle bg (%s), but sequence not found in output", mantleBgParams)
	}
	if strings.Contains(out, codeBgParams) {
		t.Fatalf("help modal scrollbar row must NOT use colCodeBg bg (%s), but sequence was found in output", codeBgParams)
	}
}

// TestMouseWheelScroll verifies the wheel scrolls the document in normal mode
// and the modal in help mode (and not the document).
func TestMouseWheelScroll(t *testing.T) {
	wheel := func(m model, b tea.MouseButton) model {
		mm, _ := m.Update(tea.MouseWheelMsg{Button: b})
		return mm.(model)
	}
	m := newModel("T", "")
	m.width, m.height = 80, 10
	m.md = strings.Repeat("line\n", 100)
	m.reflow()
	m.follow = false // don't let auto-follow fight the scroll
	m.yOff = 0

	if m = wheel(m, tea.MouseWheelDown); m.yOff <= 0 {
		t.Fatalf("wheel down should scroll the document down, yOff=%d", m.yOff)
	}
	dy := m.yOff
	if m = wheel(m, tea.MouseWheelUp); m.yOff >= dy {
		t.Fatalf("wheel up should scroll back up, yOff=%d (was %d)", m.yOff, dy)
	}

	// In help mode the wheel scrolls the modal, leaving the document put.
	m.helpMode = true
	m.helpLines = append(m.helpLines, make([]Line, 200)...) // ensure scrollable
	docY := m.yOff
	if m = wheel(m, tea.MouseWheelDown); m.yOff != docY {
		t.Fatal("wheel in help mode must not move the document")
	}
	if m.helpYOff <= 0 {
		t.Fatalf("wheel down in help mode should scroll the modal, helpYOff=%d", m.helpYOff)
	}
}
