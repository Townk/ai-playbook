package launcher

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/reengage"
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

// TestRunViewer_WiresDriftRegenReengage verifies the `run --file` viewer path stashes the
// drift-regen re-engagement context (so a standalone playbook can regenerate a drifted diff).
func TestRunViewer_WiresDriftRegenReengage(t *testing.T) {
	origUI, origRE := uiMainFn, setReengageFn
	t.Cleanup(func() { uiMainFn, setReengageFn = origUI, origRE })
	var got *reengage.Reengage
	setReengageFn = func(re *reengage.Reengage) { got = re }
	uiMainFn = func() int { return 0 }
	withArgs(t, []string{"ai-playbook", "run", "--file", "/x.md"})

	runViewer("/x.md", "")

	if got == nil || !got.DriftRegenOnly {
		t.Fatalf("runViewer must wire a DriftRegenOnly reengage; got %+v", got)
	}
}
