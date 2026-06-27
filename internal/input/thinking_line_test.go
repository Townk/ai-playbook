package input

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// mutedANSI is the truecolor foreground fragment lipgloss emits for the default
// theme's Muted (#6c7086 → 108,112,134) — the dark grey the thinking line uses.
const mutedANSI = "38;2;108;112;134"

// newThinkingLineModel builds the input model forced into the thinking state at a
// fixed width, with the given model-activity line (rendered in the modal hint slot).
func newThinkingLineModel(line string) model {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?",
		"list the last 3 commits", "", 3, 1, 1, false, "")
	m.width = 64
	m.resize()
	m.thinking = true
	m.thinkingLine = line
	return m
}

// innerBoxBottom returns the index of the field box's rounded bottom-border line
// (the FIRST ╰ from the top; the LAST ╰ in the render is the outer modal border).
func innerBoxBottom(t *testing.T, lines []string) int {
	t.Helper()
	for i, l := range lines {
		if strings.Contains(strip(l), "╰") {
			return i
		}
	}
	t.Fatal("no inner box bottom border (╰) found")
	return -1
}

// outerModalBottom returns the index of the outer modal's rounded bottom-border
// line (the LAST ╰ in the render).
func outerModalBottom(t *testing.T, lines []string) int {
	t.Helper()
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strip(lines[i]), "╰") {
			return i
		}
	}
	t.Fatal("no outer modal bottom border (╰) found")
	return -1
}

// waveRowCount returns the number of input-box interior rows (between the inner
// box ╭ top and ╰ bottom) that carry Braille wave glyphs.
func waveRowCount(lines []string, innerTop, innerBottom int) int {
	n := 0
	for i := innerTop + 1; i < innerBottom; i++ {
		if hasBraille(strip(lines[i])) {
			n++
		}
	}
	return n
}

// TestRenderThinking_LineInModal asserts the dark-grey model-activity line renders
// in the MODAL hint slot — AFTER the input box's ╰ bottom border, roughly two lines
// above the OUTER modal's ╰ bottom border — while the input box's wave rows stay
// full (the activity line is NOT inside the input box).
func TestRenderThinking_LineInModal(t *testing.T) {
	const line = "deciding: command, quick answer, or a deeper question"
	out := newThinkingLineModel(line).renderThinking()

	if !strings.Contains(out, mutedANSI) {
		t.Errorf("thinking line must be dark grey (%s):\n%s", mutedANSI, out)
	}

	lines := strings.Split(out, "\n")
	innerBottom := innerBoxBottom(t, lines)
	outerBottom := outerModalBottom(t, lines)

	// Locate the activity row by its text.
	activityIdx := -1
	for i, l := range lines {
		if strings.Contains(strip(l), line) {
			activityIdx = i
			break
		}
	}
	if activityIdx < 0 {
		t.Fatalf("activity text %q not found in render:\n%s", line, out)
	}

	// It is BELOW the input box (after the inner box's ╰ bottom border) — not inside
	// the box.
	if activityIdx <= innerBottom {
		t.Errorf("activity line must be below the input box's ╰ (idx %d), got idx %d", innerBottom, activityIdx)
	}
	// It sits roughly two lines above the OUTER modal's ╰ bottom border (an inset
	// blank above it, the padding blank below it).
	if want := outerBottom - 2; activityIdx != want {
		t.Errorf("activity line must be two lines above the modal's ╰ (idx %d), got idx %d", want, activityIdx)
	}

	// The input box's wave rows stay full: every interior row of the box is a wave
	// row (taHeight rows, no activity line stealing one).
	tf := newThinkingLineModel(line).fld.(*textField)
	// The inner box top is the first ╭ above the inner bottom.
	innerTop := -1
	for i := innerBottom - 1; i >= 0; i-- {
		if strings.Contains(strip(lines[i]), "╭") {
			innerTop = i
			break
		}
	}
	if got := waveRowCount(lines, innerTop, innerBottom); got != tf.taHeight {
		t.Errorf("input box wave rows = %d, want full taHeight %d (activity line is in the modal, not the box)", got, tf.taHeight)
	}
}

// TestRenderThinking_EmptyLineSameHeight asserts the hint slot is always present:
// renderFrame always appends the hint row, so the modal height is identical whether
// thinkingLine is set or empty. A set line fills the slot; an empty one leaves it blank.
func TestRenderThinking_EmptyLineSameHeight(t *testing.T) {
	empty := strings.Split(newThinkingLineModel("").renderThinking(), "\n")
	set := strings.Split(newThinkingLineModel("hello").renderThinking(), "\n")

	// Identical height: the slot exists in both; a set line fills it, it adds no row.
	if len(empty) != len(set) {
		t.Errorf("hint slot must be reserved (set must not add rows): empty=%d set=%d", len(empty), len(set))
	}

	// The set line lands in the modal hint slot — two lines above the outer ╰.
	ob := outerModalBottom(t, set)
	if got := strip(set[ob-2]); !strings.Contains(got, "hello") {
		t.Errorf("set thinking line must fill the modal hint slot (two lines above the modal ╰), got %q", got)
	}
	// With an empty line the same slot is blank.
	eb := outerModalBottom(t, empty)
	if got := strings.TrimSpace(strings.ReplaceAll(strip(empty[eb-2]), "│", "")); got != "" {
		t.Errorf("empty thinking line must leave the hint slot blank, got %q", strip(empty[eb-2]))
	}
}

// TestRenderThinking_LongLineTruncated asserts an activity line wider than the modal
// interior is truncated (with an ellipsis) onto a single hint row — it never wraps —
// and the modal border stays straight.
func TestRenderThinking_LongLineTruncated(t *testing.T) {
	long := strings.Repeat("verylongword ", 30) // far wider than the modal interior
	lines := strings.Split(newThinkingLineModel(long).renderThinking(), "\n")
	ob := outerModalBottom(t, lines)
	activityRow := strip(lines[ob-2])

	if !strings.Contains(activityRow, "…") {
		t.Errorf("truncated activity line must carry an ellipsis, got %q", activityRow)
	}
	// No wrap: the modal height matches the empty-line render (the long line did not
	// push extra rows into the modal).
	base := strings.Split(newThinkingLineModel("").renderThinking(), "\n")
	if len(lines) != len(base) {
		t.Errorf("long line must not wrap/add rows: long=%d base=%d", len(lines), len(base))
	}
	// The border stays straight: every rendered line has the same display width.
	w0 := lipgloss.Width(lines[0])
	for i, l := range lines {
		if w := lipgloss.Width(l); w != w0 {
			t.Errorf("line %d width = %d, want %d (border must stay straight)\n%q", i, w, w0, l)
		}
	}
}

// TestWaveDemo_CarriesSampleLine asserts the --wave-demo model ships a non-empty
// sample thinking line so the preview shows the look.
func TestWaveDemo_CarriesSampleLine(t *testing.T) {
	d := newWaveDemoModel(defaultTheme())
	if strings.TrimSpace(d.m.thinkingLine) == "" {
		t.Fatal("wave-demo model must carry a non-empty sample thinking line")
	}
	if !strings.Contains(d.m.render(), mutedANSI) {
		t.Errorf("wave-demo render must show the dark-grey thinking line (%s)", mutedANSI)
	}
}
