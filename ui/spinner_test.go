package ui

import (
	"strings"
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

// collapseLine must flatten multi-line model reasoning into a single trimmed row
// and cap it with an ellipsis (the activity feed now carries Reasoning, which can
// be long/multiline — part 2a).
func TestCollapseLine(t *testing.T) {
	got := collapseLine("  let me check the\n  build output\tnow  ")
	if got != "let me check the build output now" {
		t.Fatalf("collapseLine = %q, want a single trimmed line", got)
	}
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("collapseLine must not contain newlines/tabs: %q", got)
	}

	long := strings.Repeat("a", activityLineMax+20)
	capped := collapseLine(long)
	if !strings.HasSuffix(capped, "…") {
		t.Errorf("over-long reasoning must end with an ellipsis: %q", capped)
	}
	if len([]rune(capped)) != activityLineMax+1 { // activityLineMax runes + the ellipsis
		t.Errorf("collapseLine length = %d runes, want %d", len([]rune(capped)), activityLineMax+1)
	}
}
