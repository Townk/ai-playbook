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

// The no-mux layout: a fixed inlineBoxWidth-column box, a 1-space indent on the
// description + hint lines (not the box), and NO blank lines between the three.
func TestInlineRender_BoxWidthIndentNoBlanks(t *testing.T) {
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
	// The box (the widest lines) is exactly inlineBoxWidth columns.
	maxW := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > maxW {
			maxW = w
		}
	}
	if maxW != inlineBoxWidth {
		t.Errorf("box width = %d, want %d", maxW, inlineBoxWidth)
	}
	// Description (first) and hint (last) lines carry a 1-space indent; the box
	// border lines do not.
	if !strings.HasPrefix(lines[0], " ") {
		t.Error("description line must have a 1-space leading indent")
	}
	if !strings.HasPrefix(lines[len(lines)-1], " ") {
		t.Error("hint line must have a 1-space leading indent")
	}
	if strings.HasPrefix(lines[1], " ") {
		t.Error("the box must NOT be indented")
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
