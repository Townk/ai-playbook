package ui

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/orchestrator"
)

// newTestModelWithDiffBlock builds a model whose m.blocks contains one diff block
// with the given id alongside a shell block, so driftCheckCmds can be verified to
// skip non-diff blocks. The model is reflowed so m.blocks is populated.
func newTestModelWithDiffBlock(t *testing.T, id string) model {
	t.Helper()
	md := "# Fix\n\n" +
		"```bash {id=prep}\necho hi\n```\n\n" +
		"```diff {id=" + id + "}\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+new\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()
	for _, blk := range m.blocks {
		if blk.ID == id && blk.Type == "diff" {
			return m
		}
	}
	t.Fatalf("diff block id=%q not found after reflow; blocks=%+v", id, m.blocks)
	return m
}

// TestDriftMsg_SetsDriftedAndReflows verifies the driftMsg handler:
// DriftDrifted sets Drifted on the block state; DriftClean clears it.
func TestDriftMsg_SetsDriftedAndReflows(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")

	m2, _ := m.Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftDrifted})
	if !m2.(model).blockStates["fix"].Drifted {
		t.Fatal("driftMsg DriftDrifted must set Drifted=true")
	}

	m3, _ := m2.(model).Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftClean})
	if m3.(model).blockStates["fix"].Drifted {
		t.Fatal("driftMsg DriftClean must clear Drifted (set Drifted=false)")
	}
}

// TestDriftMsg_DriftAppliedClearsDrifted verifies that DriftApplied (already
// applied) also clears Drifted — it is not a drift error, just detection.
func TestDriftMsg_DriftAppliedClearsDrifted(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")

	// First mark it drifted.
	m2, _ := m.Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftDrifted})
	if !m2.(model).blockStates["fix"].Drifted {
		t.Fatal("precondition: DriftDrifted must set Drifted")
	}

	// Then deliver DriftApplied — must clear Drifted.
	m3, _ := m2.(model).Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftApplied})
	if m3.(model).blockStates["fix"].Drifted {
		t.Fatal("driftMsg DriftApplied must clear Drifted (patch already applied is not drift)")
	}
}

// TestDriftCheckCmds_NilWhenNoOrch verifies that driftCheckCmds returns nil when
// m.orch is nil (even if diff blocks are present).
func TestDriftCheckCmds_NilWhenNoOrch(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	// m.orch is nil (newModel does not set it).
	if m.orch != nil {
		t.Fatal("precondition: m.orch must be nil for this test")
	}
	if cmd := m.driftCheckCmds(); cmd != nil {
		t.Fatal("driftCheckCmds must return nil when m.orch is nil")
	}
}

// TestDriftCheckCmds_NonNilWhenOrchAndDiffBlock verifies that driftCheckCmds
// returns a non-nil Cmd when an orchestrator is installed and at least one diff
// block is present. The Cmd is not executed (the driver is nil in the stub orch).
func TestDriftCheckCmds_NonNilWhenOrchAndDiffBlock(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	// Inject a non-nil orchestrator (nil driver is fine — we don't execute the cmd).
	m.orch = orchestrator.New(nil, &cliMux{})

	cmd := m.driftCheckCmds()
	if cmd == nil {
		t.Fatal("driftCheckCmds must return non-nil when orch is set and a diff block exists")
	}
}

// TestDriftCheckCmds_NilWhenNoDiffBlocks verifies that driftCheckCmds returns nil
// when the orch is present but no diff blocks exist (only shell blocks).
func TestDriftCheckCmds_NilWhenNoDiffBlocks(t *testing.T) {
	md := "```bash {id=prep}\necho hi\n```\n"
	m := newModel("T", md)
	m.width, m.height = 80, 24
	m.reflow()
	m.orch = orchestrator.New(nil, &cliMux{})

	if cmd := m.driftCheckCmds(); cmd != nil {
		t.Fatal("driftCheckCmds must return nil when no diff blocks exist")
	}
}
