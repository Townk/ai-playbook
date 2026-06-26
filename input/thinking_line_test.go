package input

import (
	"strings"
	"testing"
)

// mutedANSI is the truecolor foreground fragment lipgloss emits for the default
// theme's Muted (#6c7086 → 108,112,134) — the dark grey the thinking line uses.
const mutedANSI = "38;2;108;112;134"

// newThinkingModel builds the input model forced into the thinking state at a fixed
// width, with the given below-box thinking line.
func newThinkingLineModel(line string) model {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?",
		"list the last 3 commits", "", 3, 1, 1, false, "")
	m.width = 64
	m.resize()
	m.thinking = true
	m.thinkingLine = line
	return m
}

// TestRenderThinking_LineBelowBox asserts the dark-grey thinking line renders BELOW
// the box, separated by exactly one blank line from the box's bottom border.
func TestRenderThinking_LineBelowBox(t *testing.T) {
	const line = "deciding: command, quick answer, or a deeper question"
	out := newThinkingLineModel(line).renderThinking()

	if !strings.Contains(out, mutedANSI) {
		t.Errorf("thinking line must be dark grey (%s):\n%s", mutedANSI, out)
	}

	lines := strings.Split(out, "\n")
	last := strip(lines[len(lines)-1])
	if !strings.Contains(last, line) {
		t.Errorf("last line must be the thinking text, got %q", last)
	}
	if gap := strip(lines[len(lines)-2]); strings.TrimSpace(gap) != "" {
		t.Errorf("line above the thinking text must be a blank gap, got %q", gap)
	}
	// The row above the blank gap is the box's bottom border (rounded corner).
	if border := strip(lines[len(lines)-3]); !strings.Contains(border, "╰") {
		t.Errorf("row above the gap must be the box bottom border, got %q", border)
	}
}

// TestRenderThinking_EmptyLineAddsNothing asserts an empty thinkingLine adds no
// trailing blank or text — the render ends at the box's bottom border.
func TestRenderThinking_EmptyLineAddsNothing(t *testing.T) {
	out := newThinkingLineModel("").renderThinking()
	lines := strings.Split(out, "\n")
	last := strip(lines[len(lines)-1])
	if !strings.Contains(last, "╰") {
		t.Errorf("with no thinking line the render must end at the box border, got last=%q", last)
	}
	// And the with-line render must have strictly more rows (the blank + line).
	withLine := strings.Split(newThinkingLineModel("hello").renderThinking(), "\n")
	if len(withLine) != len(lines)+2 {
		t.Errorf("a set thinking line must add exactly 2 rows (blank + line): empty=%d set=%d", len(lines), len(withLine))
	}
}

// TestRenderThinking_LongLineTruncated asserts a thinking line wider than the box
// inner width is truncated (with an ellipsis) and never wraps onto a new row.
func TestRenderThinking_LongLineTruncated(t *testing.T) {
	long := strings.Repeat("verylongword ", 30) // far wider than the 64-col box
	m := newThinkingLineModel(long)
	out := m.renderThinking()
	lines := strings.Split(out, "\n")
	textRow := strip(lines[len(lines)-1])
	if len([]rune(textRow)) > m.innerW() {
		t.Errorf("thinking line not truncated to inner width %d: %q (%d runes)", m.innerW(), textRow, len([]rune(textRow)))
	}
	if !strings.Contains(textRow, "…") {
		t.Errorf("truncated thinking line must carry an ellipsis, got %q", textRow)
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
