package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// collectRawSeqs executes cmd and returns every raw escape sequence it emits,
// recursing into Batch'd commands. The pager re-asserts the hide-cursor via
// tea.Raw (see reassertHideCursor), so the helper lets each transition/tick test
// assert that the DECTCEM hide sequence (ESC[?25l) is actually produced.
func collectRawSeqs(t *testing.T, cmd tea.Cmd) []string {
	t.Helper()
	if cmd == nil {
		return nil
	}
	var out []string
	switch msg := cmd().(type) {
	case tea.RawMsg:
		out = append(out, fmt.Sprint(msg.Msg))
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, collectRawSeqs(t, c)...)
		}
	}
	return out
}

func hasHide(seqs []string) bool {
	for _, s := range seqs {
		if strings.Contains(s, hideCursorSeq) {
			return true
		}
	}
	return false
}

// TestReassertHideCursorEmitsDECTCEM pins the mechanism: the helper must produce
// a tea.RawMsg carrying ESC[?25l (so the renderer writes it verbatim).
func TestReassertHideCursorEmitsDECTCEM(t *testing.T) {
	if hideCursorSeq != "\x1b[?25l" {
		t.Fatalf("hideCursorSeq = %q, want DECTCEM hide \\x1b[?25l", hideCursorSeq)
	}
	msg := reassertHideCursor()()
	raw, ok := msg.(tea.RawMsg)
	if !ok {
		t.Fatalf("reassertHideCursor must yield tea.RawMsg, got %T", msg)
	}
	if got := fmt.Sprint(raw.Msg); got != hideCursorSeq {
		t.Fatalf("raw payload = %q, want %q", got, hideCursorSeq)
	}
}

// TestViewHidesCursorAndReportsFocus: the View keeps Cursor nil (the base hide)
// and enables focus reporting so tea.FocusMsg is delivered for the re-assert.
func TestViewHidesCursorAndReportsFocus(t *testing.T) {
	m := newModel("T", "body")
	m.width, m.height = 80, 24
	v := m.View()
	if v.Cursor != nil {
		t.Error("View.Cursor must stay nil to keep the cursor hidden")
	}
	if !v.ReportFocus {
		t.Error("View.ReportFocus must be true so tea.FocusMsg is delivered")
	}
}

// TestSpinTickReassertsHideCursor: the live spinner tick re-asserts the hide so
// zellij can't re-show the cursor on the spinner/wave diff it paints each tick.
func TestSpinTickReassertsHideCursor(t *testing.T) {
	m := newModel("T", "body")
	m.width, m.height = 80, 24
	m.thinking = true // a live tick (gen 0 == m.tickGen)
	_, cmd := m.Update(spinTickMsg{gen: m.tickGen})
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Error("live spinTick must re-assert the hide-cursor")
	}
}

// TestFocusMsgReassertsHideCursor: regaining focus (e.g. after the thinking float
// closes) re-asserts the hide.
func TestFocusMsgReassertsHideCursor(t *testing.T) {
	m := newModel("T", "body")
	m.width, m.height = 80, 24
	_, cmd := m.Update(tea.FocusMsg{})
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Error("tea.FocusMsg must re-assert the hide-cursor")
	}
}

// TestHintEnterReassertsHideCursor: entering hint mode (Space over a visible
// button) repaints the hint overlay and re-asserts the hide.
func TestHintEnterReassertsHideCursor(t *testing.T) {
	m := newModel("T", "```bash\nmake build\n```\n"+strings.Repeat("para\n\n", 60))
	m.width, m.height = 80, 24
	m.reflow()
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if !nm.(model).hintMode {
		t.Fatal("setup: space must enter hint mode")
	}
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Error("entering hint mode must re-assert the hide-cursor")
	}
}

// TestVerifySuccessConfirmReassertsHideCursor: the verify-success confirm row
// appearing is a one-shot repaint; it re-asserts the hide.
func TestVerifySuccessConfirmReassertsHideCursor(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	nm, cmd := m.Update(resultMsg{ID: "verify", Exit: 0})
	if !nm.(model).confirmResolved {
		t.Fatal("setup: verify exit 0 must show the confirm row")
	}
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Error("verify-success confirm must re-assert the hide-cursor")
	}
}

// TestReshowConfirmReassertsHideCursor: pressing `c` to re-show the confirm also
// re-asserts the hide.
func TestReshowConfirmReassertsHideCursor(t *testing.T) {
	m, _ := newReengageEventsModel(t, "# Playbook\n", "# Playbook\nclean\n")
	m.wrappedUp = true // `c` is gated on a prior solution
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !nm.(model).confirmResolved {
		t.Fatal("setup: `c` must re-show the confirm row")
	}
	if !hasHide(collectRawSeqs(t, cmd)) {
		t.Error("re-showing the confirm must re-assert the hide-cursor")
	}
}
