package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	idiff "github.com/Townk/ai-playbook/internal/diff"
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

	// Then deliver DriftApplied — must clear Drifted AND mark the block applied
	// (Status "ok") so it renders Undo + a greyed number, not an Apply button.
	m3, _ := m2.(model).Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftApplied})
	st := m3.(model).blockStates["fix"]
	if st.Drifted {
		t.Fatal("driftMsg DriftApplied must clear Drifted (patch already applied is not drift)")
	}
	if st.Status != "ok" {
		t.Fatalf("driftMsg DriftApplied must mark the block applied (Status ok); got %q", st.Status)
	}
}

// TestDriftMsg_PendingResolveBecomesResolved verifies the custom/mixed manual-resolve
// path: a resolve that CHANGED the file (pendingResolve) whose re-check is still
// DriftDrifted becomes the terminal "resolved manually" state — not unresolved drift.
// Without pendingResolve, the same verdict stays Drifted (a "kept current" resolve).
func TestDriftMsg_PendingResolveBecomesResolved(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.blockStates["fix"] = blockRunState{Drifted: true, pendingResolve: true}
	st := mustModel(m.Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftDrifted})).blockStates["fix"]
	if st.Drifted || !st.Resolved || st.pendingResolve {
		t.Fatalf("changed resolve + DriftDrifted → Resolved (not Drifted, flag cleared); got %+v", st)
	}

	m2 := newTestModelWithDiffBlock(t, "fix")
	m2.blockStates["fix"] = blockRunState{}
	st2 := mustModel(m2.Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftDrifted})).blockStates["fix"]
	if !st2.Drifted || st2.Resolved {
		t.Fatalf("plain DriftDrifted (no pendingResolve) must stay Drifted, not Resolved; got %+v", st2)
	}
}

// TestResolvedDiffBlock_Render verifies a manually-resolved diff block shows an
// undo-resolve button (not apply/undo-diff), keeps view-diff, and renders the note.
func TestResolvedDiffBlock_Render(t *testing.T) {
	md := "```diff {id=fix}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n"
	lines, buttons, _ := Render(md, 80, RenderOpts{States: map[string]blockRunState{"fix": {Resolved: true}}})
	if buttonForBlock(buttons, "fix", "apply-diff") != nil || buttonForBlock(buttons, "fix", "undo-diff") != nil {
		t.Error("a resolved diff block must show neither apply-diff nor undo-diff")
	}
	if buttonForBlock(buttons, "fix", "undo-resolve") == nil {
		t.Error("a resolved diff block must show an undo-resolve button")
	}
	if buttonForBlock(buttons, "fix", "diff") == nil {
		t.Error("view-diff must remain available on a resolved block")
	}
	if !strings.Contains(joinText(lines), "resolved manually") {
		t.Error("a resolved diff block must show the 'resolved manually' note")
	}
}

// TestUndoResolve_RestoresBackup verifies undo-resolve restores the pre-resolve file
// content, clears Resolved, and consumes the backup.
func TestUndoResolve_RestoresBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.conf")
	if err := os.WriteFile(target, []byte("resolved-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An absolute +++ path so driftTargetPath (no orch) resolves straight to it.
	patch := "--- " + target + "\n+++ " + target + "\n@@ -1 +1 @@\n-old\n+new\n"
	m := newModel("T", "```diff {id=fix}\n"+patch+"```\n")
	m.width, m.height = 80, 24
	m.reflow()
	m.blockStates["fix"] = blockRunState{Resolved: true}
	m.driftResolveBackup["fix"] = "original-drifted\n"

	m2, _ := m.undoResolve(Button{Kind: "undo-resolve", BlockID: "fix", Payload: patch})

	if got, _ := os.ReadFile(target); string(got) != "original-drifted\n" {
		t.Fatalf("undoResolve must restore the pre-resolve content; got %q", got)
	}
	if m2.blockStates["fix"].Resolved {
		t.Error("undoResolve must clear Resolved")
	}
	if _, ok := m2.driftResolveBackup["fix"]; ok {
		t.Error("undoResolve must consume the backup")
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

// TestDriftHint_LabelOverlapsGlyph verifies the hint letter paints directly
// over the pill's glyph (the cell just after the left cap) on the button's own
// line — not floating above or below the button.
func TestDriftHint_LabelOverlapsGlyph(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.width, m.height = 100, 40
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	m2 := mustModel(m.Update(tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m2.hintMode {
		t.Fatal("space must enter hint mode")
	}
	var lbl string
	var btn Button
	for l, b := range m2.hintLabels {
		if b.Kind == "drift-resolve" {
			lbl, btn = l, b
		}
	}
	if lbl == "" {
		t.Fatal("drift-resolve must carry a hint label")
	}
	rows := strings.Split(strip(m2.viewString()), "\n")
	screenRow := m2.bodyTop() + btn.Line - m2.yOff
	if screenRow < 0 || screenRow >= len(rows) {
		t.Fatalf("button row %d out of view (%d rows)", screenRow, len(rows))
	}
	r := []rune(rows[screenRow])
	at := 2 + btn.Col + 1 // 2-col left margin + left cap
	if at >= len(r) || string(r[at]) != lbl {
		t.Fatalf("hint letter %q must overlap the pill glyph at col %d; row=%q", lbl, at, rows[screenRow])
	}
}

// TestDriftHint_PillInvertedFill verifies that in hint mode the drift action
// pills keep their filled-shape look via the inverted-fill trick (muted text on
// a solid colSurface0 fill, caps in the fill color) rather than collapsing to
// greyed caps around an empty center.
func TestDriftHint_PillInvertedFill(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix")
	m.width, m.height = 100, 40 // tall enough that the drift region is in the body window
	m.blockStates["fix"] = blockRunState{Drifted: true}
	m.reflow()

	m2 := mustModel(m.Update(tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m2.hintMode {
		t.Fatal("space must enter hint mode")
	}
	got := m2.viewString()
	const surfaceBgParams = "48;2;49;50;68" // colSurface0 #313244 — the pill body fill
	if !strings.Contains(got, surfaceBgParams) {
		t.Fatal("hint mode must paint the drift pills' body with the solid colSurface0 fill")
	}
}

// driftedConfContent is the on-disk (drifted) target content used by the resolve-
// manually temp-file tests: it has `timeout = 99` where the patch expected `= 30`.
const driftedConfContent = "[server]\nhost = localhost\nport = 8080\ntimeout = 99\nmax_connections = 100\n"

// patchForTarget builds a well-formed unified patch (drifted against `timeout = 30`,
// proposing `= 60`) whose target is the given absolute path, so driftTargetPath
// resolves to it without an orchestrator.
func patchForTarget(abs string) string {
	return "--- a/" + abs + "\n+++ b/" + abs + "\n@@ -2,4 +2,4 @@\n" +
		" host = localhost\n port = 8080\n-timeout = 30\n+timeout = 60\n max_connections = 100\n"
}

// TestDriftResolve_OpensTempFileWhenMarkupSucceeds verifies the new flow: when
// ConflictMarkup can locate the hunk, resolve-manually opens the editor on a TEMP
// file (not the real target), the real target is untouched, and the temp carries
// conflict markers.
func TestDriftResolve_OpensTempFileWhenMarkupSucceeds(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.conf")
	if err := os.WriteFile(target, []byte(driftedConfContent), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("T", "# x")
	m.asker = nil // no-mux → ExecProcess path

	m2i, cmd := m.driftResolveDispatch(Button{Payload: patchForTarget(target)})
	m2 := m2i
	if cmd == nil {
		t.Fatal("resolve manually must return an editor ExecProcess cmd")
	}
	if m2.driftTempPath == "" {
		t.Fatal("markup success must set driftTempPath (temp-file flow active)")
	}
	if m2.driftTempPath == target {
		t.Fatal("the editor must open a TEMP file, not the real target")
	}
	if m2.driftTempTarget != target {
		t.Fatalf("driftTempTarget must be the real target, got %q", m2.driftTempTarget)
	}
	// Real target still drifted; temp carries markers.
	if got, _ := os.ReadFile(target); string(got) != driftedConfContent {
		t.Fatal("the real target must be untouched until the user saves a resolution")
	}
	tempBytes, err := os.ReadFile(m2.driftTempPath)
	if err != nil {
		t.Fatalf("temp file must exist: %v", err)
	}
	if !idiff.HasConflictMarkers(string(tempBytes)) {
		t.Fatal("the temp copy must contain conflict markers")
	}
}

// TestDriftResolve_ReadbackWritesRealFileWhenResolved verifies that on editor return
// with the markers removed, the reconciled content is written to the REAL target, the
// temp file is cleaned up, and drift is re-checked.
func TestDriftResolve_ReadbackWritesRealFileWhenResolved(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.conf")
	if err := os.WriteFile(target, []byte(driftedConfContent), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("T", "# x")
	m.asker = nil
	m2i, _ := m.driftResolveDispatch(Button{Payload: patchForTarget(target)})
	m2 := m2i

	// User resolves in the editor: markers gone, timeout set to 60.
	resolved := "[server]\nhost = localhost\nport = 8080\ntimeout = 60\nmax_connections = 100\n"
	if err := os.WriteFile(m2.driftTempPath, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	tempPath := m2.driftTempPath

	m3 := mustModel(m2.Update(driftResolveReloadMsg{}))
	if got, _ := os.ReadFile(target); string(got) != resolved {
		t.Fatalf("resolved content must be written back to the real target, got:\n%s", got)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatal("the temp file must be removed after read-back")
	}
	if m3.driftTempPath != "" {
		t.Fatal("driftTempPath must clear after read-back")
	}
}

// TestDriftResolve_UnresolvedMarkersLeaveFileUnchanged verifies that if the user
// saves with the openers still present, the real target is NOT modified and a status
// explains it; the temp file is removed.
func TestDriftResolve_UnresolvedMarkersLeaveFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.conf")
	if err := os.WriteFile(target, []byte(driftedConfContent), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("T", "# x")
	m.asker = nil
	m2i, _ := m.driftResolveDispatch(Button{Payload: patchForTarget(target)})
	m2 := m2i
	tempPath := m2.driftTempPath

	// User saves WITHOUT resolving (leaves the marked copy as-is).
	m3 := mustModel(m2.Update(driftResolveReloadMsg{}))
	if got, _ := os.ReadFile(target); string(got) != driftedConfContent {
		t.Fatal("unresolved markers must leave the real target unchanged")
	}
	if !strings.Contains(m3.status, "unresolved conflict markers") {
		t.Fatalf("status must flag unresolved markers, got %q", m3.status)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatal("the temp file must be removed even when left unresolved")
	}
}

// TestDriftResolve_FallsBackToRawFileWhenMarkupFails verifies that when ConflictMarkup
// can't locate the hunk, the flow falls back to opening the raw target (driftTempPath
// stays empty) — no regression from the legacy behaviour.
func TestDriftResolve_FallsBackToRawFileWhenMarkupFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.conf")
	// Content that shares NONE of the patch's context → unlocatable → ok=false.
	if err := os.WriteFile(target, []byte("totally different\ncontent here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel("T", "# x")
	m.asker = nil
	m2i, cmd := m.driftResolveDispatch(Button{Payload: patchForTarget(target)})
	m2 := m2i
	if cmd == nil {
		t.Fatal("fallback must still return an editor ExecProcess cmd")
	}
	if m2.driftTempPath != "" {
		t.Fatal("markup failure must fall back to the raw file (driftTempPath empty)")
	}
	if strings.Contains(m2.status, "couldn't determine") {
		t.Fatalf("target path must resolve for a well-formed patch, got %q", m2.status)
	}
}

// mustModel unwraps a (tea.Model, tea.Cmd) Update result to the concrete model.
func mustModel(mi tea.Model, _ tea.Cmd) model { return mi.(model) }
