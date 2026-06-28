package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The inline (no-mux) render shows ONLY the three spec elements — description
// line, (self-bordered) input box, hint — with NO title bar and NO outer frame.
func TestInlineRender_OmitsTitleAndOuterFrame(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?", "", "", 3, 1, 1, false, "")
	m.inline = true
	m.width = 80
	out := m.render()

	if strings.Contains(out, "▓▓▓") {
		t.Error("inline render must omit the title bar (▓▓▓ …)")
	}
	if !strings.Contains(out, "How can I help you today?") {
		t.Error("inline render must include the description line")
	}
	if !strings.Contains(out, "submit") {
		t.Error("inline render must include the hint line")
	}

	// The framed (float) render of the same model DOES carry the title bar — proves
	// the inline flag is what removes it, not a missing title.
	m.inline = false
	if framed := m.render(); !strings.Contains(framed, "▓▓▓") {
		t.Error("framed render should still show the title bar (sanity)")
	}
}

// The no-mux layout: a dark-grey separator rule on top, then description, box,
// hint. Separator + box are inlineBoxWidth wide at inlineBoxIndent (1 space);
// description + hint at inlineTextIndent (3 spaces); no blank lines.
func TestInlineRender_LayoutStructure(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?", "x", "", 3, 1, 1, false, "")
	m.inline = true
	m.width = 120 // a wide terminal — the box must still be the fixed width
	lines := strings.Split(m.render(), "\n")

	// No blank lines anywhere.
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			t.Errorf("line %d is blank; the inline layout must have no empty lines", i)
		}
	}
	// First line is the dark-grey separator rule: inlineBoxWidth columns of "─" at
	// the box indent.
	sep := lines[0]
	if !strings.HasPrefix(sep, inlineBoxIndent) || strings.HasPrefix(sep, inlineTextIndent) {
		t.Errorf("separator must carry the box indent, not the text indent: %q", sep)
	}
	if !strings.Contains(sep, strings.Repeat("─", inlineBoxWidth)) {
		t.Errorf("first line must be the %d-col separator rule", inlineBoxWidth)
	}
	// The separator + box lines are inlineBoxWidth + the box indent wide.
	wantW := inlineBoxWidth + len(inlineBoxIndent)
	maxW := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > maxW {
			maxW = w
		}
	}
	if maxW != wantW {
		t.Errorf("widest line = %d, want %d (box %d + indent %d)", maxW, wantW, inlineBoxWidth, len(inlineBoxIndent))
	}
	// Description (line 1) and hint (last) carry the 3-space text indent; a box
	// border line (line 2) carries only the 1-space box indent.
	if !strings.HasPrefix(lines[1], inlineTextIndent) {
		t.Errorf("description line must have the text indent (%d spaces)", len(inlineTextIndent))
	}
	if !strings.HasPrefix(lines[len(lines)-1], inlineTextIndent) {
		t.Errorf("hint line must have the text indent (%d spaces)", len(inlineTextIndent))
	}
	if !strings.HasPrefix(lines[2], inlineBoxIndent) || strings.HasPrefix(lines[2], inlineTextIndent) {
		t.Errorf("box line must have the box indent (1 space), not the text indent: %q", lines[2])
	}
}

// LOGIC-level proof that a plain Enter submits on the inline path: it must
// transition to the in-box thinking state AND invoke inlineSubmit with the typed
// value. If this passes but Enter "does nothing" in a live ZLE-hosted run, the
// fault is key DELIVERY (terminal/protocol), not the submit logic.
func TestInlineModel_PlainEnterSubmitsToThinking(t *testing.T) {
	called := make(chan string, 1)
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "How can I help you today?", "fix the build", "", 3, 1, 1, false, "")
	m.inline = true
	m.inlineSubmit = func(v string) <-chan ThinkUpdate {
		called <- v
		return make(chan ThinkUpdate) // never closes; the test only checks the transition
	}

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	res := nm.(model)
	if !res.thinking {
		t.Fatal("plain Enter must transition the inline model to the thinking state")
	}
	select {
	case v := <-called:
		if v != "fix the build" {
			t.Fatalf("inlineSubmit got %q, want %q", v, "fix the build")
		}
	default:
		t.Fatal("inlineSubmit was not invoked on Enter")
	}
}

// Shift+Enter on the inline path inserts a newline, it does NOT submit.
func TestInlineModel_ShiftEnterDoesNotSubmit(t *testing.T) {
	m := newInputModel(defaultTheme(), "default", "ai-playbook", "p", "x", "", 3, 1, 1, false, "")
	m.inline = true
	m.inlineSubmit = func(string) <-chan ThinkUpdate { t.Fatal("Shift+Enter must not submit"); return nil }
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if nm.(model).thinking {
		t.Fatal("Shift+Enter must not enter the thinking state")
	}
}
