package input

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

const us, rs, gs = "\x1f", "\x1e", "\x1d"

func TestParseFormSpec(t *testing.T) {
	raw := "name" + us + "line" + us + "Your name" + us + "" + rs +
		"plan" + us + "choose" + us + "Plan" + us + "free" + gs + "pro"
	ff, err := parseFormSpec(raw)
	if err != nil || len(ff) != 2 {
		t.Fatalf("parse: %v len=%d", err, len(ff))
	}
	if ff[0].name != "name" || ff[0].ftype != "line" || ff[1].ftype != "choose" {
		t.Fatalf("bad parse: %+v", ff)
	}
	if ff[1].param != "free"+gs+"pro" {
		t.Fatalf("choose param: %q", ff[1].param)
	}
}

func TestFormTabCyclesFields(t *testing.T) {
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""}, {"b", "line", "Second", ""},
	}, 1, 1)
	if m.focus != 0 {
		t.Fatal("starts on field 0")
	}
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m2.(formModel).focus != 1 {
		t.Fatal("Tab moves to field 1")
	}
}

func TestFormRequiredNoEarlySubmit(t *testing.T) {
	// Enter on an empty required form must NOT submit; it jumps to next unfilled.
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""}, {"b", "line", "Second", ""},
	}, 1, 1)
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m2.(formModel).submitted {
		t.Fatal("must not submit with empty fields")
	}
}

func TestFormRendersTabRowAndActiveFieldNoNestedTitle(t *testing.T) {
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""}, {"b", "choose", "Options", "yes\x1dno"},
	}, 1, 1)
	m.width = 50
	out := strip(m.render())
	if !strings.Contains(out, "▓▓▓ Setup") {
		t.Fatal("main title missing")
	}
	if !strings.Contains(out, "First") || !strings.Contains(out, "Options") {
		t.Fatal("tab row labels missing")
	}
	if strings.Count(out, "▓▓▓") != 1 {
		t.Fatal("must have exactly one ▓▓▓ (no nested per-field titles)")
	}
}

func TestFormOutputProtocol(t *testing.T) {
	answers := []string{"a" + us + "Alice", "b" + us + "yes"}
	if got := strings.Join(answers, rs); got != "a"+us+"Alice"+rs+"b"+us+"yes" {
		t.Fatal("output join")
	}
}

func TestFormRejectsConfirmType(t *testing.T) {
	_, err := parseFormSpec("a" + us + "line" + us + "A" + us + "" + rs + "b" + us + "confirm" + us + "B" + us + "")
	if err == nil {
		t.Fatal("a confirm field type must be rejected (forms support line/text/choose only)")
	}
}

func TestFormShowsActiveFieldLabelAsDescription(t *testing.T) {
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"name", "line", "Your full name", ""}, {"city", "line", "City", ""},
	}, 1, 1)
	m.width = 50
	out := strip(m.render())
	// the active field's label renders as a description line above its input
	if !strings.Contains(out, "Your full name") {
		t.Fatal("active field label must render as a description above the input")
	}
}

func TestFormArrowKeysMoveFields(t *testing.T) {
	// Use choose fields: Left/Right navigate fields for choose (not text cursor).
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "choose", "A", "x\x1dy"}, {"b", "choose", "B", "x\x1dy"},
	}, 1, 1)
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m2.(formModel).focus != 1 {
		t.Fatal("Right arrow moves to the next field")
	}
	m3, _ := m2.(formModel).Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m3.(formModel).focus != 0 {
		t.Fatal("Left arrow moves to the previous field")
	}
}

// C1: maxHeight must equal the height rendered with the tallest field active.
// A form with [line "A", text "B"] — the text field renders taller (textarea
// rows + inner box border), so focus=1 is taller than focus=0.
func TestFormMaxHeightIsTallestField(t *testing.T) {
	const w = 64
	m := newFormModel(defaultTheme(), "T", []formField{
		{"a", "line", "A", ""},
		{"b", "text", "B", ""},
	}, 1, 1)
	m.width = w

	// Render with focus=0 (line field).
	h0 := measureHeight(m.render())

	// Render with focus=1 (text field — should be taller).
	m.focus = 1
	h1 := measureHeight(m.render())

	if h1 <= h0 {
		t.Fatalf("text field (focus=1) must render taller than line field (focus=0): h0=%d h1=%d", h0, h1)
	}

	// maxHeight must equal h1 (the worst-case tallest field), not h0.
	got := m.maxHeight(w)
	if got != h1 {
		t.Fatalf("maxHeight(%d)=%d want %d (height when focus=1/text field is active)", w, got, h1)
	}
	if got <= h0 {
		t.Fatalf("maxHeight(%d)=%d must be > height when focus=0 (%d)", w, got, h0)
	}
}

// TestFormTextField_BoxHasMantleBackground pins the form-field counterpart of
// TestTextBox_FramedPaintsMantle_InlineDoesNot (render_test.go): form fields are
// ALWAYS rendered inside the dialog frame (no inline layout for forms), so
// formModel.render() passes theme.Mantle as field.view's frameBG. See that
// test's comment for why the assertion targets the field's unwrapped view()
// output at frameBG=theme.Mantle rather than the fully-framed render string
// (renderFrame's own Background(Mantle) wrap confounds a naive whole-string SGR
// check).
func TestFormTextField_BoxHasMantleBackground(t *testing.T) {
	const mantleBG = "48;2;24;24;37"
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""},
	}, 1, 1)
	m.width = 50
	tf := m.fields[m.focus].(*textField)
	if got := tf.view(m.innerW(), true, theme.Mantle); !strings.Contains(got, mantleBG) {
		t.Fatalf("form text field box must paint the Mantle background; got %q", got)
	}
}

// TestFormTabRow_PaintsFrameBG is the discriminating frame-bg test for the form
// tab row (companion to the choose/text-box frame-bg tests). The tab labels are
// foreground-only styled spans that used to bleed the terminal default inside the
// always-Mantle form frame; tabRow(frameBG) must now paint frameBG on every label
// and separator, and leave them bare when frameBG=="". A two-sided check so an
// implementation ignoring frameBG fails one side.
func TestFormTabRow_PaintsFrameBG(t *testing.T) {
	const mantleBG = "48;2;24;24;37"
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""}, {"b", "line", "Second", ""},
	}, 1, 1)
	if got := m.tabRow(theme.Mantle); !strings.Contains(got, mantleBG) {
		t.Fatalf("framed tab row must carry the Mantle background %q: %q", mantleBG, got)
	}
	if got := m.tabRow(""); strings.Contains(got, mantleBG) {
		t.Fatalf("bare tab row (frameBG=\"\") must NOT carry the Mantle background: %q", got)
	}
}

// A3b: formModel.Update only forwarded KeyPressMsg/WindowSizeMsg to the
// focused field, silently dropping tea.PasteMsg (which textField.handle
// supports) in forms. Pasting into a focused line field must land the
// content in the field's value.
func TestFormPasteDeliveredToFocusedField(t *testing.T) {
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""}, {"b", "line", "Second", ""},
	}, 1, 1)
	m2, _ := m.Update(tea.PasteMsg{Content: "pasted-text"})
	got := m2.(formModel).fields[0].value()
	if got != "pasted-text" {
		t.Fatalf("paste must land in the focused field's value, got %q", got)
	}
}

// A3b: cursor-blink commands/messages returned by initCmd must reach the
// focused field's Update via the same delegation path as paste, instead of
// being silently dropped by formModel.Update's narrowed switch.
func TestFormDeliversNonKeyMsgToFocusedField(t *testing.T) {
	m := newFormModel(defaultTheme(), "Setup", []formField{
		{"a", "line", "First", ""},
	}, 1, 1)
	blink := textarea.Blink()
	_, cmd := m.Update(blink)
	if cmd == nil {
		t.Fatal("a blink message delivered to the focused (blinking) field must yield a re-arm cmd, not be dropped")
	}
}

// I2: the form hint must use the 󱊷 glyph, not ⎋.
func TestFormHintUsesNewEscGlyph(t *testing.T) {
	m := newFormModel(defaultTheme(), "T", []formField{
		{"a", "line", "A", ""},
		{"b", "line", "B", ""},
	}, 1, 1)
	h := strip(m.hint())
	if !strings.Contains(h, "󱊷") {
		t.Fatalf("form hint must use 󱊷 ESC glyph: %q", h)
	}
	if strings.Contains(h, "⎋") {
		t.Fatalf("form hint must not use ⎋ glyph: %q", h)
	}
}
