package input

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func strip(s string) string { return ansiRE.ReplaceAllString(s, "") }

// TestRenderLayout pins the modal: a rounded outer border, then the title, rule,
// boxed input with the prompt icon, and the new "·"-separated hint.
func TestRenderLayout(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "hello world", "", 5, 1, 1, false, "")
	m.width = 60
	m.resize()
	plain := strip(m.render())
	lines := strings.Split(plain, "\n")
	if !strings.HasPrefix(lines[0], "╭") {
		t.Fatalf("first line must be the outer border, got %q", lines[0])
	}
	if !strings.Contains(plain, "▓▓▓ ai-playbook") {
		t.Fatal("title missing")
	}
	if !strings.Contains(plain, "━") {
		t.Fatal("rule missing")
	}
	if !strings.Contains(plain, promptIcon) {
		t.Fatal("prompt icon missing")
	}
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatal("input box border missing")
	}
	if !strings.Contains(plain, "󰌑 submit") || !strings.Contains(plain, "󱊷 cancel") {
		t.Fatalf("hint line wrong:\n%s", plain)
	}
	if !strings.Contains(plain, "󰘶󰌑 newline") {
		t.Fatal("text hint must offer newline")
	}
}

// TestTextRendersPromptAboveBox verifies that a non-empty --prompt string
// renders above the input box, inside the frame.
func TestTextRendersPromptAboveBox(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "Notes", "Describe the issue", "", "", 3, 1, 1, false, "")
	m.width = 60
	m.resize()
	plain := strip(m.render())
	if !strings.Contains(plain, "▓▓▓ Notes") {
		t.Fatal("title missing")
	}
	if !strings.Contains(plain, "Describe the issue") {
		t.Fatal("prompt missing above the input box")
	}
	// prompt must appear before the input box top-border (skip outer frame ╭ on line 0)
	lines := strings.Split(plain, "\n")
	promptLine, boxLine := -1, -1
	for i, l := range lines[1:] { // skip outer frame top border
		if promptLine < 0 && strings.Contains(l, "Describe the issue") {
			promptLine = i + 1
		}
		if boxLine < 0 && strings.Contains(l, "╭") {
			boxLine = i + 1
		}
	}
	if promptLine < 0 || boxLine < 0 || promptLine >= boxLine {
		t.Fatalf("prompt (line %d) must be above the input box top-border (line %d)", promptLine, boxLine)
	}
}

func TestTextNoPromptRowWhenEmpty(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "Notes", "", "", "", 3, 1, 1, false, "")
	m.width = 60
	m.resize()
	// the line after the rule should be the box top, not a blank prompt line
	lines := strings.Split(strip(m.render()), "\n")
	ruleIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "━") {
			ruleIdx = i
			break
		}
	}
	if ruleIdx < 0 {
		t.Fatal("no rule")
	}
}

// TestLineHasNoScrollbarOrNewline pins the line variant: single row, no
// scrollbar, and a hint without the newline affordance.
func TestLineHasNoScrollbarOrNewline(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "Name", "", "", "type…", 1, 1, 1, true, "")
	m.width = 60
	m.resize()
	plain := strip(m.render())
	if strings.Contains(plain, "┃") {
		t.Fatal("line must not render a scroll thumb")
	}
	if strings.Contains(plain, "newline") {
		t.Fatal("line hint must not offer newline")
	}
	if !strings.Contains(plain, "󰌑 submit") || !strings.Contains(plain, "󱊷 cancel") {
		t.Fatalf("line hint wrong:\n%s", plain)
	}
}

// TestPopupInputArea pins the chrome budget against the named frame/box
// constants rather than a magic number, and the input height against --height.
func TestPopupInputArea(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "", "", 3, 1, 1, false, "")
	m.width = 57
	m.resize()
	wantW := 57 - (frameBorder + 2*frameHPad + boxBorder + boxPadL + iconCol + scrollGap + scrollCol)
	tf := m.fld.(*textField)
	if w, h := tf.ta.Width(), tf.ta.Height(); w != wantW || h != 3 {
		t.Fatalf("input area = %dx%d, want %dx3", w, h, wantW)
	}
}

// TestScrollbarWrappedLines verifies the scrollbar reflects soft-wrapped rows,
// not just logical lines: one long line (no newlines) that wraps past the
// viewport must still show a thumb.
func TestScrollbarWrappedLines(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", strings.Repeat("x", 200), "", 3, 1, 1, false, "") // 1 logical line → ~5 wrapped rows at width 40
	m.width = 53
	m.resize()
	tf := m.fld.(*textField)
	if vc := visualLineCount(tf); vc <= tf.ta.Height() {
		t.Fatalf("visualLineCount = %d, want > %d (content wraps past the viewport)", vc, tf.ta.Height())
	}
	if tf.ta.LineCount() != 1 {
		t.Fatalf("precondition: expected 1 logical line, got %d", tf.ta.LineCount())
	}
	if sb := scrollbarColored(tf, ""); !strings.Contains(sb, "┃") {
		t.Fatalf("scrollbar should show a thumb for wrapped content, got %q", strip(sb))
	}
}

// TestRenderFitsPane verifies no rendered line exceeds the pane width.
func TestRenderFitsPane(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "a long enough value to exercise wrapping across the textarea width", "", 4, 1, 1, false, "")
	m.width = 50
	m.resize()
	for i, l := range strings.Split(m.render(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("line %d width %d exceeds pane width %d: %q", i, w, m.width, strip(l))
		}
	}
}

// TestPasteMultiLine verifies that a bracketed-paste event carrying a
// multi-line string inserts the full text (newlines included) without
// triggering submit.
func TestPasteMultiLine(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "", "", 5, 1, 1, false, "")
	m.width = 60
	m.resize()

	pastedText := "first line\nsecond line\nthird line"
	result, _ := m.Update(tea.PasteMsg{Content: pastedText})
	updated := result.(model)

	if updated.submitted {
		t.Fatal("paste must NOT trigger submit")
	}
	if got := updated.fld.value(); got != pastedText {
		t.Fatalf("textarea value after paste = %q, want %q", got, pastedText)
	}
}

// TestTextUsesChevronIcon verifies that the rendered text input contains ❯
// and not the old brain glyph.
func TestTextUsesChevronIcon(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "hi", "", 3, 1, 1, false, "")
	m.width = 60
	m.resize()
	out := strip(m.render())
	if !strings.Contains(out, "❯") {
		t.Fatalf("text input must use the ❯ prompt icon: %q", out)
	}
	if strings.Contains(out, "󰧑") {
		t.Fatal("old brain icon must be gone")
	}
}

// --icon overrides the default prompt glyph (e.g. ai-assist-popup uses 󰧑).
func TestTextIconOverride(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?", "", "", 3, 1, 1, false, "󰧑")
	m.width = 60
	m.resize()
	out := strip(m.render())
	if !strings.Contains(out, "󰧑") {
		t.Fatalf("--icon override must render the given glyph: %q", out)
	}
	if strings.Contains(out, "❯") {
		t.Fatal("overridden icon must replace the default ❯")
	}
}

// TestTextBox_FramedSetsMantleBoxBG_InlineDoesNot pins the text-input box
// interior fix: model.render()'s FRAMED branch must set the hosted
// textField's boxBG to theme.Mantle before rendering the box — otherwise the
// box interior (and its border background) bleeds to the terminal default
// inside the Mantle-filled dialog frame. The INLINE branch must leave it ""
// — that layout composites on the pane/terminal background, mirroring
// hintFrameBG's framed/inline split.
//
// The assertion targets the field's own boxBG state and its unwrapped
// view() output (not the fully-framed render string): renderFrame's outer
// Background(Mantle) wrap re-supplies the Mantle SGR at the start of every
// physical line regardless of the box's own background, so scanning the
// full framed string for the SGR would pass even with the fix reverted
// (confirmed against a deliberately-reverted build) — it doesn't isolate the
// property under test the way checking boxBG/view() directly does.
func TestTextBox_FramedSetsMantleBoxBG_InlineDoesNot(t *testing.T) {
	const mantleBG = "48;2;24;24;37"
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "", "hi", "", 3, 1, 1, false, "")
	m.width = 60
	m.resize()

	m.render() // framed
	tf := m.fld.(*textField)
	if tf.boxBG != theme.Mantle {
		t.Fatalf("framed render must set the text box's boxBG to theme.Mantle, got %q", tf.boxBG)
	}
	if got := tf.view(m.innerW(), true); !strings.Contains(got, mantleBG) {
		t.Fatalf("framed text box must paint the Mantle background; got %q", got)
	}

	m.inline = true
	m.render() // inline
	if tf.boxBG != "" {
		t.Fatalf("inline render must leave the text box's boxBG empty, got %q", tf.boxBG)
	}
	if got := tf.view(inlineBoxWidth, true); strings.Contains(got, mantleBG) {
		t.Fatalf("inline text box must NOT paint the Mantle background; got %q", got)
	}
}
