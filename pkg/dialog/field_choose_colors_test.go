package dialog

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/pkg/dialog/theme"
)

// mantleBG is theme.Mantle's truecolor background SGR params (#181825 =
// rgb(24,24,37)) — the frame background every framed span must carry.
const mantleBG = "48;2;24;24;37"

// The typed text sits on the textarea's cursor line, which is drawn with the
// CursorLine style — it must carry the text foreground, not a default colour.
// Distinct colours isolate the text fg from the icon/border fg.
func TestTextFieldCursorLineUsesTextFg(t *testing.T) {
	f := newTextField(defaultTheme(), "hello", "", 2, false)
	out := f.viewWith(30, taStyle{icon: "#00ff00", border: "#00ff00", text: "#ff0000", bg: "#000088", placeholder: false})
	const redFg = "38;2;255;0;0"
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(strip(ln), "hello") {
			if !strings.Contains(ln, redFg) {
				t.Fatalf("typed text on the cursor line must use the text fg %q: %q", redFg, ln)
			}
			return
		}
	}
	t.Fatal("typed text 'hello' not found in render")
}

// Truecolor ANSI fragments for the default theme.
const (
	fgMuted       = "38;2;108;112;134" // theme.Muted (unselected-tab fg)
	fgText        = "38;2;205;214;244" // theme.Text (the old, too-white option fg)
	fgWhite       = "38;2;255;255;255" // ButtonSelFg (bright white)
	bgSel         = "48;2;101;106;131" // ButtonSelBg (selected background)
	fgFieldBorder = "38;2;88;91;112"   // theme.FieldBorder (default box border)
)

func topBorderLine(t *testing.T, out string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "╭") {
			return ln
		}
	}
	t.Fatalf("no box top border (╭) found in: %q", out)
	return ""
}

// Problem 2: non-highlighted rows must use the muted (unselected-tab) fg, not
// the near-white text fg that made them indistinguishable from the selection.
func TestChooseNonHighlightedRowUsesMutedFg(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"alpha", "beta"}, false, "")
	out := f.view(40, true, theme.Mantle) // highlight is on row 0; "beta" is non-highlighted
	if !strings.Contains(out, fgMuted) {
		t.Fatalf("non-highlighted rows must use the muted fg %q: %q", fgMuted, out)
	}
	if strings.Contains(out, fgText) {
		t.Fatalf("option rows must NOT use the near-white text fg %q: %q", fgText, out)
	}
}

// TestChooseField_NonHighlightedRowPaintsFrameBG is the discriminating frame-bg
// test for the choose widget (the model is TestTextField_FrameBG_MantleVsEmpty):
// the non-highlighted "beta" row — a muted, foreground-only span that used to
// bleed the terminal default inside a Mantle frame — must carry frameBG when
// framed and NOT when inline. A two-sided check so an implementation that ignores
// frameBG (the pre-contract per-site state) fails one side. The assertion targets
// the specific non-highlighted row line, not the whole view (the highlighted row
// carries its own selected bg), and view()'s unwrapped output (renderFrame's own
// Background wrap would confound a whole-frame scan — see the text-box test).
func TestChooseField_NonHighlightedRowPaintsFrameBG(t *testing.T) {
	nonHLLine := func(out string) string {
		t.Helper()
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(strip(ln), "beta") {
				return ln
			}
		}
		t.Fatalf("no non-highlighted 'beta' row in: %q", out)
		return ""
	}
	f := newChooseField(defaultTheme(), "default", []string{"alpha", "beta"}, false, "")

	framed := nonHLLine(f.view(40, true, theme.Mantle))
	if !strings.Contains(framed, mantleBG) {
		t.Fatalf("framed non-highlighted row must carry the Mantle background %q: %q", mantleBG, framed)
	}
	inline := nonHLLine(f.view(40, true, ""))
	if strings.Contains(inline, mantleBG) {
		t.Fatalf("inline non-highlighted row must NOT carry the Mantle background: %q", inline)
	}
}

// Problem 3 (unfocused): the other entry shows its label above the box, and the
// whole widget (label + border) renders in the muted colour on the FRAME
// background — not the selected background, and (post frame-bg contract) not the
// terminal default either. The old behavior painted no background at all on the
// unfocused row, bleeding the terminal default inside the Mantle frame; the
// frame-bg contract now paints frameBG, so the border line carries the Mantle bg.
func TestChooseOtherUnfocusedIsMuted(t *testing.T) {
	f := newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…")
	out := f.view(40, true, theme.Mantle) // highlight row 0 → other row (idx 2) unfocused + empty
	if !strings.Contains(strip(out), "Other…:") {
		t.Fatalf("other label must render as a heading above the box: %q", strip(out))
	}
	bl := topBorderLine(t, out)
	if strings.Contains(bl, fgFieldBorder) {
		t.Fatalf("unfocused other border must NOT use the default field-border colour: %q", bl)
	}
	if !strings.Contains(bl, fgMuted) {
		t.Fatalf("unfocused other border must use the muted fg %q: %q", fgMuted, bl)
	}
	if strings.Contains(bl, bgSel) {
		t.Fatalf("unfocused other must NOT have the selected background: %q", bl)
	}
	// Frame-bg contract (flipped from the old "no background" expectation): the
	// unfocused border must now carry the Mantle frame background, not bleed.
	if !strings.Contains(bl, mantleBG) {
		t.Fatalf("unfocused other border must carry the Mantle frame background %q: %q", mantleBG, bl)
	}
}

// Problem 3 (focused): the other entry uses the selected background and bright
// white for everything (label + border + icon), and the background fills EVERY
// line of the box — including the icon row and the empty rows (no gaps).
func TestChooseOtherFocusedSelBgBrightWhite(t *testing.T) {
	g, _, _ := field(newChooseField(defaultTheme(), "default", []string{"a", "b"}, false, "Other…")).
		handle(tea.KeyPressMsg{Code: tea.KeyDown})
	g, _, _ = g.handle(tea.KeyPressMsg{Code: tea.KeyDown}) // onto the other row (idx 2)
	out := g.view(40, true, theme.Mantle)

	// The label heading renders bright white on the selected background.
	if !strings.Contains(strip(out), "Other…:") {
		t.Fatalf("focused other should show its label heading: %q", strip(out))
	}
	bl := topBorderLine(t, out)
	if !strings.Contains(bl, fgWhite) {
		t.Fatalf("focused other border must be bright white %q: %q", fgWhite, bl)
	}

	// Every line from the label through the box bottom must carry the selected
	// background — no cell left with the default background (Problem 2/3).
	lines := strings.Split(out, "\n")
	start := -1
	for i, ln := range lines {
		if strings.Contains(ln, "Other…") {
			start = i
			break
		}
	}
	if start < 0 || start+4 >= len(lines) {
		t.Fatalf("expected a 5-line focused other item: %q", out)
	}
	for off := 0; off <= 4; off++ { // label, top, icon row, empty row, bottom
		if !strings.Contains(lines[start+off], bgSel) {
			t.Fatalf("focused other line %d must be backed by the selected bg %q: %q",
				off, bgSel, lines[start+off])
		}
		// Each focused line spans the full inner width (40), so the highlight
		// includes a trailing space past the box's right border.
		if w := lipgloss.Width(strip(lines[start+off])); w != 40 {
			t.Fatalf("focused other line %d width = %d, want 40 (trailing space): %q",
				off, w, strip(lines[start+off]))
		}
	}
}
