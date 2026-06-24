package ui

import "testing"

func TestButtonAt(t *testing.T) {
	btns := []Button{
		{Line: 5, Col: 30, Width: 2, Kind: "play"},
		{Line: 5, Col: 32, Width: 2, Kind: "copy"},
	}
	// View: bodyTop=3, yOff=2 → line 5 shows on screen row 3+(5-2)=6.
	// content col = x-2; play covers content cols [30,32) → screen x [32,34).
	// copy covers content cols [32,34) → screen x [34,36).

	// hit on play: x=32 → content col=30, line=2+(6-3)=5 → play [30,32) ✓
	if b, ok := buttonAt(btns, 32, 6, 2, 3); !ok || b.Kind != "play" {
		t.Fatalf("expected play, got %v ok=%v", b, ok)
	}
	// hit on copy: x=34 → content col=32, line=5 → copy [32,34) ✓
	if b, ok := buttonAt(btns, 34, 6, 2, 3); !ok || b.Kind != "copy" {
		t.Fatalf("expected copy at x=34, got %v ok=%v", b, ok)
	}
	// miss just left of play: x=31 → content col=29 < 30, should miss
	if _, ok := buttonAt(btns, 31, 6, 2, 3); ok {
		t.Fatalf("x=31 (content col 29) is left of play, should miss")
	}
	// miss past all buttons: x=50 → content col=48, no button covers it
	if _, ok := buttonAt(btns, 50, 6, 2, 3); ok {
		t.Fatalf("x=50 is past the buttons, should miss")
	}
	// miss on wrong line: y=99 → line=2+(99-3)=98, no button on that line
	if _, ok := buttonAt(btns, 32, 99, 2, 3); ok {
		t.Fatalf("y on a different line should miss")
	}
}

func TestAssignHintLabels(t *testing.T) {
	btns := []Button{{Kind: "play"}, {Kind: "copy"}, {Kind: "copy"}}
	labels := assignHintLabels(btns)
	if len(labels) != 3 {
		t.Fatalf("want 3 labels, got %d", len(labels))
	}
	seen := map[string]bool{}
	for l := range labels {
		if len(l) != 1 || seen[l] {
			t.Fatalf("labels must be distinct single chars: %q", l)
		}
		seen[l] = true
	}
}
