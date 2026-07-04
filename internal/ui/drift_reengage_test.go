package ui

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/reengage"
)

// TestCanReengageInProc_DriftRegenOnlyExcluded verifies a DriftRegenOnly re-engagement
// context (the run --file harness wiring for drift regenerate) does NOT enable the
// followup/authoring affordances — only a full Reengage does.
func TestCanReengageInProc_DriftRegenOnlyExcluded(t *testing.T) {
	m := model{reeng: reengage.New(&reengage.Reengage{}, nil)}
	if !m.canReengageInProc() {
		t.Error("a full Reengage should enable in-proc re-engagement (followup)")
	}
	m.reeng = reengage.New(&reengage.Reengage{DriftRegenOnly: true}, nil)
	if m.canReengageInProc() {
		t.Error("a DriftRegenOnly Reengage must NOT enable followup re-engagement")
	}
}
