package ui

import (
	"strings"
	"testing"
)

func TestWaitingLine_TieredPhraseNoActivity(t *testing.T) {
	got := WaitingLine(0, 0, "", 80)
	if !strings.Contains(got, workingPhrases[0]) {
		t.Errorf("WaitingLine = %q, want first tiered phrase %q", got, workingPhrases[0])
	}
	if strings.Contains(got, "\n") {
		t.Error("no activity → single line expected")
	}
}

func TestWaitingLine_AdvancesPhraseWithElapsed(t *testing.T) {
	got := WaitingLine(0, 10*workingStepSec, "", 80)
	if !strings.Contains(got, workingLabel(10*workingStepSec)) {
		t.Errorf("WaitingLine = %q, want later tiered phrase", got)
	}
}

func TestWaitingLine_AppendsActivity(t *testing.T) {
	got := WaitingLine(0, 0, "reading config.go", 80)
	if !strings.Contains(got, "reading config.go") {
		t.Errorf("WaitingLine = %q, want the activity text", got)
	}
	if !strings.Contains(got, activityGlyph) {
		t.Errorf("WaitingLine = %q, want the activity glyph %q", got, activityGlyph)
	}
}
