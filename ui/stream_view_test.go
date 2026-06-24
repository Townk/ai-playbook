package ui

import (
	"strings"
	"testing"

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
