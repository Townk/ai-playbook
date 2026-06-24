package ui

import (
	"testing"
)

func TestSpinnerLine(t *testing.T) {
	got := spinnerLine(0, "Working…", 3)
	plain := strip(got)
	if plain != "⠋ Working… 3s" {
		t.Fatalf("strip = %q, want %q", plain, "⠋ Working… 3s")
	}
}

func TestSpinnerFrameWraps(t *testing.T) {
	// Frame index wraps over the frame set and tolerates large values.
	a := strip(spinnerLine(0, "x", 0))
	b := strip(spinnerLine(len(spinnerFrames), "x", 0)) // wraps to frame 0
	if a != b {
		t.Fatalf("frame %d should equal frame 0: %q vs %q", len(spinnerFrames), b, a)
	}
}
