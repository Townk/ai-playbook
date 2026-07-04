package launcher

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/internal/ui"
)

// TestDriftRegenReengage_DriftOnly verifies the run-viewer re-engagement is wired for
// drift regenerate only: DriftRegenOnly set, Events present, and any non-drift kind is
// refused (so the followup/authoring affordances can't accidentally use it).
func TestDriftRegenReengage_DriftOnly(t *testing.T) {
	re := driftRegenReengage()
	if re == nil || !re.DriftRegenOnly || re.Events == nil {
		t.Fatalf("driftRegenReengage must be DriftRegenOnly with Events wired; got %+v", re)
	}
	if _, _, err := re.Events(reengage.KindReengageFollowup, "", "", nil); err == nil {
		t.Error("drift-only Events must refuse a non-drift-regen kind")
	}
}

// TestRunViewer_WiresDriftRegenReengage verifies the `run --file` viewer path sets the
// drift-regen re-engagement context on ui.Options (so a standalone playbook can
// regenerate a drifted diff).
func TestRunViewer_WiresDriftRegenReengage(t *testing.T) {
	origUI := uiRunFn
	t.Cleanup(func() { uiRunFn = origUI })
	var got *reengage.Reengage
	uiRunFn = func(o ui.Options) int { got = o.Reengage; return 0 }
	withArgs(t, []string{"ai-playbook", "run", "--file", "/x.md"})

	runViewer("/x.md", "", ui.Options{})

	if got == nil || !got.DriftRegenOnly {
		t.Fatalf("runViewer must wire a DriftRegenOnly reengage; got %+v", got)
	}
}
