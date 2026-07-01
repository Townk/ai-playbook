package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// TestDriftRegenMsg_SuccessSplicesAndRechecks verifies that a successful
// driftRegenMsg splices the fresh patch into m.md and clears the "regenerating"
// status on the block state, and that the re-check cmd is returned.
func TestDriftRegenMsg_SuccessSplicesAndRechecks(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix") // m.md has ```diff {id=fix} ... ```
	// Inject a non-nil orchestrator so driftCheckCmds() returns the re-check cmd.
	m.orch = orchestrator.New(nil, &cliMux{})
	m.blockStates["fix"] = blockRunState{Status: "regenerating", Drifted: true}
	fresh := "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+FRESH\n"
	m2i, cmd := m.Update(driftRegenMsg{ID: "fix", NewPatch: fresh})
	if cmd == nil {
		t.Fatal("success must return the drift re-check cmd")
	}
	m2 := m2i.(model)
	if m2.blockStates["fix"].Status == "regenerating" {
		t.Fatal("regenerating status must clear")
	}
	if !strings.Contains(m2.md, "+FRESH") {
		t.Fatal("m.md must carry the spliced patch")
	}
}

// TestDriftRegenMsg_FailureStaysDrifted verifies that an errored driftRegenMsg
// keeps Drifted=true, sets RegenFailed=true, and clears the "regenerating" status.
func TestDriftRegenMsg_FailureStaysDrifted(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.blockStates["fix"] = blockRunState{Status: "regenerating", Drifted: true}
	m2i, _ := m.Update(driftRegenMsg{ID: "fix", Err: errors.New("boom")})
	m2 := m2i.(model)
	st := m2.blockStates["fix"]
	if !st.Drifted || !st.RegenFailed || st.Status == "regenerating" {
		t.Fatalf("failure must stay drifted + set RegenFailed + clear status, got %+v", st)
	}
}

// TestDriftRegenMsg_NoBackendSetsNote verifies F24: a failure whose error looks like a
// missing AI backend surfaces the specific "no AI backend available" note (not a silent
// no-op), while a generic failure leaves RegenNote empty (the generic alternate shows).
func TestDriftRegenMsg_NoBackendSetsNote(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.blockStates["fix"] = blockRunState{Status: "regenerating", Drifted: true}
	noBackend := errors.New(`exec: "claude": executable file not found in $PATH`)
	m2 := mustModel(m.Update(driftRegenMsg{ID: "fix", Err: noBackend}))
	if note := m2.blockStates["fix"].RegenNote; !strings.Contains(note, "no AI backend found") {
		t.Fatalf("no-backend failure must set the specific note, got %q", note)
	}

	m3 := newTestModelWithDiffBlock(t, "fix")
	m3.blockStates["fix"] = blockRunState{Status: "regenerating", Drifted: true}
	m3b := mustModel(m3.Update(driftRegenMsg{ID: "fix", Err: errors.New("boom")}))
	if note := m3b.blockStates["fix"].RegenNote; note != "" {
		t.Fatalf("generic failure must leave RegenNote empty (generic alternate shows), got %q", note)
	}
}

// TestLooksLikeNoBackend spot-checks the F24 error classifier.
func TestLooksLikeNoBackend(t *testing.T) {
	yes := []error{
		errors.New(`exec: "claude": executable file not found in $PATH`),
		errors.New(`harness "pi" not yet supported`),
		errors.New("regenerate unavailable"),
	}
	for _, e := range yes {
		if !looksLikeNoBackend(e) {
			t.Errorf("looksLikeNoBackend(%q) = false, want true", e)
		}
	}
	if looksLikeNoBackend(errors.New("some transient model error")) {
		t.Error("a generic error must NOT be classified as no-backend")
	}
	if looksLikeNoBackend(nil) {
		t.Error("nil error must not be no-backend")
	}
}

// TestDriftedViewDiff_ClickOpensOverlay verifies F30: with a block DRIFTED, clicking
// the view-diff (diff) button still opens the read-only side-by-side pager (it is no
// longer swallowed alongside apply-diff).
func TestDriftedViewDiff_ClickOpensOverlay(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.asker = nil
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "diff")
	if b == nil {
		t.Fatal("drifted diff block must still register a view-diff button")
	}
	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2 := mustModel(m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}))
	if !m2.diffMode {
		t.Fatal("F30: clicking view-diff on a DRIFTED block must open the read-only pager")
	}
}

// TestDriftedApplyDiff_ClickSwallowed verifies apply-diff stays inert when drifted:
// the click must NOT open the pager nor start a run.
func TestDriftedApplyDiff_ClickSwallowed(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.asker = nil
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "apply-diff")
	if b == nil {
		t.Fatal("drifted diff block must still register an apply-diff button (dimmed)")
	}
	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2 := mustModel(m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}))
	if m2.diffMode {
		t.Fatal("apply-diff on a drifted block must stay inert (no pager)")
	}
	if m2.blockStates["fix"].Status == "running" {
		t.Fatal("apply-diff on a drifted block must not start a run")
	}
}

// TestDriftResolve_MouseClickReachesHandler verifies F22: a mouse click at the
// drift-resolve button's rendered (Line,Col) reaches the handler, and F21: it opens
// the target file in an editor (a non-nil ExecProcess cmd) rather than the read-only
// pager (diffMode stays false — the OLD behavior was to open the pager).
func TestDriftResolve_MouseClickReachesHandler(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.asker = nil // no-mux → ExecProcess editor path
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "drift-resolve")
	if b == nil {
		t.Fatal("drifted block must register a drift-resolve button")
	}
	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2i, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	m2 := m2i.(model)
	if m2.diffMode {
		t.Fatal("F21: resolve manually must NOT open the read-only pager (that's view-diff's job)")
	}
	if cmd == nil {
		t.Fatal("F22/F21: the click must reach the handler and return the editor ExecProcess cmd")
	}
	if strings.Contains(m2.status, "couldn't determine") {
		t.Fatalf("target path must resolve for a well-formed patch, got status %q", m2.status)
	}
}

// TestDriftRegen_ClickShowsSpinner verifies F23(a): clicking regenerate immediately
// sets the "regenerating" status (the spinner) and clears any prior RegenFailed/Note.
func TestDriftRegen_ClickShowsSpinner(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.asker = nil
	m.orch = orchestrator.New(nil, &cliMux{}) // non-nil so the drift-regen branch runs
	m.blockStates["fix"] = blockRunState{Drifted: true, RegenFailed: true, RegenNote: "stale"}
	m.reflow()

	b := buttonForBlock(m.buttons, "fix", "drift-regen")
	if b == nil {
		t.Fatal("drifted block must register a drift-regen button")
	}
	x := b.Col + 2
	y := m.bodyTop() + (b.Line - m.yOff)
	m2 := mustModel(m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}))
	st := m2.blockStates["fix"]
	if st.Status != "regenerating" {
		t.Fatalf("regenerate click must set status=regenerating immediately, got %q", st.Status)
	}
	if st.RegenFailed || st.RegenNote != "" {
		t.Fatalf("a fresh regenerate must clear prior failure state, got %+v", st)
	}
}

// TestDriftedApplyDiff_NoHintLabel verifies F19: in hint mode a drifted block's inert
// apply-diff button gets NO hint label, while the live drift-resolve / drift-regen
// buttons do.
func TestDriftedApplyDiff_NoHintLabel(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	m2 := mustModel(m.Update(tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m2.hintMode {
		t.Fatal("space must enter hint mode")
	}
	var haveResolve, haveRegen bool
	for _, b := range m2.hintLabels {
		if b.Kind == "apply-diff" && b.BlockID == "fix" {
			t.Fatal("F19: a drifted block's inert apply-diff must NOT get a hint label")
		}
		if b.Kind == "drift-resolve" {
			haveResolve = true
		}
		if b.Kind == "drift-regen" {
			haveRegen = true
		}
	}
	if !haveResolve || !haveRegen {
		t.Fatalf("drift buttons must be hint-labelled: resolve=%v regen=%v", haveResolve, haveRegen)
	}
}

// TestDriftHint_DoesNotClobberBanner verifies F20: in hint mode the resolve/regenerate
// hint letters do NOT overwrite the "⚠ … no longer applies" banner on the line above
// the buttons — the banner text (and its ⚠ glyph) survive intact.
func TestDriftHint_DoesNotClobberBanner(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.width, m.height = 100, 40 // tall enough that the drift region is in the body window
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	m2 := mustModel(m.Update(tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m2.hintMode {
		t.Fatal("space must enter hint mode")
	}
	got := strip(m2.viewString())
	if !strings.Contains(got, "no longer applies") {
		t.Fatalf("F20: the drift banner text must survive hint mode intact:\n%s", got)
	}
	if !strings.Contains(got, "⚠") {
		t.Fatal("F20: the ⚠ banner glyph must not be overwritten by a hint label")
	}
}

// mustModel unwraps a (tea.Model, tea.Cmd) Update result to the concrete model.
func mustModel(mi tea.Model, _ tea.Cmd) model { return mi.(model) }
