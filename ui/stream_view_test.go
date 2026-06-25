package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestViewShowsSpinnerWhenThinking(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.thinking = true
	m.thinkLabel = "Working…"
	m.spinTicks = 30 // 3s
	m.reflow()
	out := m.viewString()
	if !strings.Contains(strip(out), "Working… 3s") {
		t.Fatalf("thinking view must show the spinner label+seconds:\n%s", strip(out))
	}
	if lipgloss.Height(out) != m.height {
		t.Fatalf("view height %d != %d", lipgloss.Height(out), m.height)
	}
}

func TestViewNoSpinnerWhenNotThinking(t *testing.T) {
	m := newModel("T", "hello world")
	m.width, m.height = 80, 24
	m.thinking = false
	m.reflow()
	if strings.Contains(strip(m.viewString()), "Working…") {
		t.Fatal("non-thinking view must not show a spinner")
	}
}

// TestThinkingSpinnerAnimatesInView is the Bug B regression guard: with
// thinking=true and NO running block, processing successive spinTickMsg must
// change the rendered spinner frame in the View output. The thinking spinner is
// composed LIVE in normalLines (reading m.spinFrame each View call), not from the
// cached reflow buffer (m.lines) — so the authoring/re-engagement wait, during
// which only spinTickMsg + activityMsg arrive (no block reflow), still animates.
func TestThinkingSpinnerAnimatesInView(t *testing.T) {
	m := newModel("T", "")
	m.width, m.height = 80, 24
	m.thinking = true
	m.thinkLabel = "Working…"
	m.tickRunning = true // a live tick loop drives the spinner

	var mdl tea.Model = m
	seen := map[rune]bool{}
	for i := 0; i < len(spinnerFrames); i++ {
		var cmd tea.Cmd
		mdl, cmd = mdl.Update(spinTickMsg{gen: 0})
		if cmd == nil {
			t.Fatalf("tick %d: spinTickMsg must keep the loop alive while thinking (got nil cmd)", i)
		}
		frame := spinnerGlyphInView(t, strip(mdl.(model).viewString()))
		seen[frame] = true
	}
	if len(seen) < 3 {
		t.Fatalf("spinner frozen: only %d distinct frames across %d ticks (want the animation to advance)", len(seen), len(spinnerFrames))
	}
}

// spinnerGlyphInView extracts the braille spinner glyph from the rendered view.
func spinnerGlyphInView(t *testing.T, view string) rune {
	t.Helper()
	for _, r := range view {
		for _, f := range spinnerFrames {
			if r == f {
				return r
			}
		}
	}
	t.Fatalf("no spinner glyph found in view:\n%s", view)
	return 0
}
