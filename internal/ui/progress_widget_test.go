package ui

import (
	"strings"
	"testing"
)

func TestProgressWidget_Render(t *testing.T) {
	var w ProgressWidget
	// 0s, no activity → spinner + first phrase + "0s", single line.
	got := w.Render(80)
	if !strings.Contains(got, "Working…") || !strings.Contains(got, "0s") {
		t.Fatalf("render at 0s = %q, want first phrase + 0s", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("no-activity render must be a single line: %q", got)
	}
	// With activity → a second line carrying the (collapsed) summary.
	w.SetActivity("running   gg build")
	got = w.Render(80)
	if !strings.Contains(got, "\n") || !strings.Contains(got, "running gg build") {
		t.Errorf("activity render must add a collapsed activity line: %q", got)
	}
}

func TestProgressWidget_TickAndElapsed(t *testing.T) {
	var w ProgressWidget
	for i := 0; i < 155; i++ { // 155 ticks = 15.5s
		w.Tick()
	}
	if w.Elapsed() != 15 {
		t.Fatalf("Elapsed() = %d, want 15 (155 ticks / 10)", w.Elapsed())
	}
	// At 15s the phrase has escalated past the first entry.
	if got := w.Render(80); !strings.Contains(got, "15s") {
		t.Errorf("render at 15s must show elapsed 15s, got %q", got)
	}
}

func TestProgressWidget_Reset(t *testing.T) {
	var w ProgressWidget
	w.Tick()
	w.SetActivity("x")
	w.Reset()
	if w.Elapsed() != 0 || strings.Contains(w.Render(80), "\n") {
		t.Errorf("Reset must clear elapsed + activity, got render %q", w.Render(80))
	}
}
