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

// workingLabel must escalate one phrase per workingStepSec seconds and HOLD the
// last phrase once the list is exhausted (authoring has no hard timeout).
func TestWorkingLabel(t *testing.T) {
	if got := workingLabel(0); got != workingPhrases[0] {
		t.Errorf("workingLabel(0) = %q, want %q", got, workingPhrases[0])
	}
	if got := workingLabel(workingStepSec - 1); got != workingPhrases[0] {
		t.Errorf("workingLabel(%d) = %q, want first phrase (still in bucket 0)", workingStepSec-1, got)
	}
	if got := workingLabel(workingStepSec); got != workingPhrases[1] {
		t.Errorf("workingLabel(%d) = %q, want second phrase", workingStepSec, got)
	}
	if got := workingLabel(3 * workingStepSec); got != workingPhrases[3] {
		t.Errorf("workingLabel(%d) = %q, want fourth phrase", 3*workingStepSec, got)
	}
	// Held tail: far beyond the list pins to the LAST phrase, never panics/wraps.
	last := workingPhrases[len(workingPhrases)-1]
	if got := workingLabel(len(workingPhrases) * workingStepSec); got != last {
		t.Errorf("workingLabel past the list = %q, want held tail %q", got, last)
	}
	if got := workingLabel(10000 * workingStepSec); got != last {
		t.Errorf("workingLabel very large = %q, want held tail %q", got, last)
	}
	// Negative clamps to the first phrase.
	if got := workingLabel(-5); got != workingPhrases[0] {
		t.Errorf("workingLabel(-5) = %q, want first phrase", got)
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
