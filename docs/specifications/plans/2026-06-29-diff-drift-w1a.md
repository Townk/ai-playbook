# Diff drift W1a — detection + drifted UI + [resolve manually] (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect when a `diff` block's patch no longer applies to its target, grey out its apply/view-diff buttons, and offer a "resolve manually" path that opens the in-process diff view.

**Architecture:** A new orchestrator `CheckDrift` runs `git apply --check` (forward + reverse) to classify each diff block as clean / already-applied / drifted. The viewer fires these checks asynchronously (the existing `orchCmd`→msg pattern) after the playbook loads, stores the verdict on a new `blockRunState.Drifted` axis, greys the apply/view-diff buttons for drifted blocks, and renders a drift message + a `[resolve manually]` tag-button (mirroring the failed-block "try another fix" button) that opens the FC1 diff view.

**Tech Stack:** Go; `internal/orchestrator` (git apply via the driver); `internal/ui` (bubbletea v2 model, render.go, results.go); `internal/diff` (FC1 view).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- Drift classification from two `git apply --check` runs (mirror `applyDiff`'s flags `--recount --ignore-whitespace`, via `o.Drv.Run`): forward `--check` Exit 0 → **clean**; else reverse `--check --reverse` Exit 0 → **already-applied**; else → **drifted**. Only **drifted** sets `Drifted`; already-applied is detected solely to AVOID false-drifting (reflecting external applies is OUT OF SCOPE).
- `blockRunState.Drifted bool` is a NEW axis, orthogonal to `Status`/`Action`. An already-applied (`Status=="ok"`) block is never drifted.
- Checks run ASYNC (never block render) via the `orchCmd` goroutine→`tea.Msg` idiom (no retained `tea.Program.Send`); fired after blocks exist (the stream-EOF reflow) and when the orchestrator lands (`orchReadyMsg`). Diff blocks are rebuilt on regenerate, so re-fire there too.
- Drifted UI: the tab's **apply-diff + view-diff (`diff`) buttons grey out** (reuse `buttonGlyph`'s `colOverlay0` dim) AND become **inert** (the click/hint dispatch early-returns for those kinds when drifted). Below the diff body: a drift message + a `[resolve manually]` tag-button (`Kind:"drift-resolve"`) → routes to `activateDiffButton` (the FC1 diff view, mux float / no-mux overlay).
- `[regenerate]` is W1b — NOT in this plan. Render only the single `[resolve manually]` button now.
- `gofmt -l`/`go vet`/`make lint` clean; touched packages pass `go test` (+ `-race` for `internal/ui`). A pre-existing `reengage_test` timeout is load-flaky — re-run in isolation if it trips.

---

### Task 1: Orchestrator `CheckDrift`

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Produces:
  ```go
  type DriftVerdict int
  const ( DriftClean DriftVerdict = iota; DriftApplied; DriftDrifted )
  func (o *Orchestrator) CheckDrift(diff string) (DriftVerdict, error)
  ```
  Consumed by Task 2.

**Context:** Mirror `applyDiff` (orchestrator.go:657-669) but with `--check` and a verdict. `writePatch` (orchestrator.go:716) writes the temp patch; defer-remove it. Two `o.Drv.Run` calls with `applyTimeout` (orchestrator.go:643).

- [ ] **Step 1: Write the failing test**

```go
func TestCheckDrift_CleanAppliedDrifted(t *testing.T) {
	dir := t.TempDir()
	// a git repo with a file; build a patch against it
	o := newTestOrchInDir(t, dir) // driver cwd == dir (reuse the apply/undo harness)
	mustRun(t, dir, "git", "init", "-q")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	mustRun(t, dir, "git", "add", "."); mustRun(t, dir, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	patch := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

	if v, _ := o.CheckDrift(patch); v != DriftClean {
		t.Fatalf("fresh patch should be clean, got %v", v)
	}
	// apply it → now it's already-applied
	o.Do(Action{Kind: KindApplyDiff, Payload: patch})
	if v, _ := o.CheckDrift(patch); v != DriftApplied {
		t.Fatalf("applied patch should be already-applied, got %v", v)
	}
	// drift the file so neither forward nor reverse applies
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("totally\ndifferent\ncontent\n"), 0o644)
	if v, _ := o.CheckDrift(patch); v != DriftDrifted {
		t.Fatalf("a changed target should be drifted, got %v", v)
	}
}
```
(`newTestOrchInDir`/`mustRun` — reuse/extend the existing orchestrator test harness that builds an Orchestrator with a driver cwd == a temp dir; if a git-repo helper exists, use it.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run TestCheckDrift`
Expected: FAIL — `CheckDrift`/`DriftVerdict` undefined.

- [ ] **Step 3: Implement**

```go
type DriftVerdict int

const (
	DriftClean DriftVerdict = iota // patch applies forward
	DriftApplied                   // patch reverse-applies (already applied)
	DriftDrifted                   // neither — the target changed incompatibly
)

// CheckDrift classifies whether diff still applies to its target. It never mutates
// the working tree (git apply --check).
func (o *Orchestrator) CheckDrift(diff string) (DriftVerdict, error) {
	patch, err := writePatch(diff)
	if err != nil {
		return DriftDrifted, err
	}
	defer os.Remove(patch)
	base := "git apply --check --recount --ignore-whitespace "
	if o.Drv.Run(base+"-- "+shquote(patch), applyTimeout).Exit == 0 {
		return DriftClean, nil
	}
	if o.Drv.Run(base+"--reverse -- "+shquote(patch), applyTimeout).Exit == 0 {
		return DriftApplied, nil
	}
	return DriftDrifted, nil
}
```

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): CheckDrift — classify a diff as clean/applied/drifted via git apply --check"
```

---

### Task 2: `blockRunState.Drifted` + async drift-check dispatch

**Files:**
- Modify: `internal/ui/results.go` (`Drifted` field + `driftMsg`), `internal/ui/inprocess.go` (`driftCheckCmds`), `internal/ui/model.go` (the `driftMsg` handler + firing the checks)
- Test: `internal/ui/model_test.go` (or a new `drift_test.go`)

**Interfaces:**
- Consumes: `orchestrator.CheckDrift`/`DriftVerdict` (T1).
- Produces: `blockRunState.Drifted bool`; `driftMsg{ID string; Verdict orchestrator.DriftVerdict}`; `func (m model) driftCheckCmds() tea.Cmd`. Consumed by Tasks 3-4 (the render reads `Drifted`).

**Context:** Diff blocks exist only after the playbook streams in (`reflow` builds `m.blocks`, model.go:508). The orchestrator may arrive later (`orchReadyMsg`). So fire `driftCheckCmds()` from BOTH the `orchReadyMsg` handler and after the stream-EOF reflow, and after a regenerate's re-arm. Each check is an async Cmd returning a `driftMsg`; the handler sets `Drifted` + `reflow()`s.

- [ ] **Step 1: Write the failing test**

```go
func TestDriftMsg_SetsDriftedAndReflows(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix") // a model whose blocks include a diff block id "fix"
	m2, _ := m.Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftDrifted})
	if !m2.(model).blockStates["fix"].Drifted {
		t.Fatal("driftMsg DriftDrifted must set Drifted")
	}
	m3, _ := m2.(model).Update(driftMsg{ID: "fix", Verdict: orchestrator.DriftClean})
	if m3.(model).blockStates["fix"].Drifted {
		t.Fatal("DriftClean must clear Drifted")
	}
}

func TestDriftCheckCmds_OnePerDiffBlock(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix") // also has a shell block
	m.orch = stubOrch(t) // non-nil orch whose CheckDrift returns DriftClean
	cmd := m.driftCheckCmds()
	if cmd == nil { t.Fatal("expected drift-check cmds for the diff block") }
}
```
(Adapt to the real model test harness — `newTestModelWithDiffBlock`/`stubOrch`: build a model with `m.blocks` containing one diff block + a non-diff block, and a non-nil `m.orch`. If a stub orchestrator isn't feasible, assert `driftCheckCmds` returns nil when `m.orch==nil` and non-nil when set + a diff block exists.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run 'TestDriftMsg|TestDriftCheckCmds'`
Expected: FAIL — `Drifted`/`driftMsg`/`driftCheckCmds` undefined.

- [ ] **Step 3: Implement**

`results.go`: add `Drifted bool` to `blockRunState` (after `FollowupExhausted`); add `type driftMsg struct { ID string; Verdict orchestrator.DriftVerdict }`.
`inprocess.go`:
```go
func (m model) driftCheckCmds() tea.Cmd {
	if m.orch == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, blk := range m.blocks {
		if blk.Type != "diff" {
			continue
		}
		id, patch, orch := blk.ID, blk.Payload, m.orch
		cmds = append(cmds, func() tea.Msg {
			v, _ := orch.CheckDrift(patch)
			return driftMsg{ID: id, Verdict: v}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
```
`model.go`: add a `case driftMsg:` (near `resultMsg`, model.go:1315):
```go
case driftMsg:
	st := m.blockStates[msg.ID]
	st.Drifted = msg.Verdict == orchestrator.DriftDrifted
	m.blockStates[msg.ID] = st
	m.reflow()
	return m, nil
```
Fire `m.driftCheckCmds()`: (a) in the `orchReadyMsg` handler (after `m.orch` is installed), batch it into the returned cmd; (b) at the stream-EOF reflow where `m.structured` rebuilds `m.md` (model.go:651-655) — after the reflow, return `m.driftCheckCmds()`; (c) in the `reArmStreamMsg` handler (model.go:1514, after `clear(m.blockStates)`) so a regenerate re-checks. (Batch with any existing returned cmd; never block.)

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run 'TestDriftMsg|TestDriftCheckCmds'`
Run: `go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/results.go internal/ui/inprocess.go internal/ui/model.go internal/ui/*_test.go
git commit -m "feat(ui): async diff-drift check → blockRunState.Drifted"
```

---

### Task 3: Grey out + disable apply/view-diff on a drifted block

**Files:**
- Modify: `internal/ui/render.go` (`buttonGlyph` dim for drifted), `internal/ui/model.go` (inert dispatch)
- Test: `internal/ui/render_test.go`

**Interfaces:**
- Consumes: `blockRunState.Drifted` (T2).

**Context:** `buttonGlyph` (render.go:222) already dims via `bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyph)` when `r.shellDisabled && isShellActionKind(kind)`. Drift is per-block, so add a per-block dim: pass the block's `Drifted` (or look it up via `r.states[blockID]`) and dim when `Drifted && (kind=="apply-diff" || kind=="diff")`. Then make those buttons inert: in BOTH dispatch sites (mouse model.go:842, hint model.go:1057) early-return when the clicked button is `apply-diff`/`diff` and `m.blockStates[b.BlockID].Drifted` (mirror the `shellActionsReady` swallow at model.go:991).

- [ ] **Step 1: Write the failing test**

```go
func TestDriftedDiff_GreysApplyAndViewDiff(t *testing.T) {
	states := map[string]blockRunState{"fix": {Drifted: true}}
	lines, buttons, _ := Render("```diff {id=fix}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n", 100, states, "")
	_ = buttons
	// the apply/view-diff glyphs render in the muted overlay color when drifted
	joined := joinText(lines)
	if !strings.Contains(joined, colOverlay0Render(glyphViewDiff)) { // helper: the dimmed glyph
		t.Fatal("view-diff button should be greyed on a drifted block")
	}
}
```
(Adapt the assertion to how the test suite inspects styled output — e.g. assert the dimmed glyph appears, or that the apply/view-diff buttons are rendered with the overlay color. If a direct color assertion is awkward, assert via a small helper that renders `buttonGlyph` for a drifted state and compares.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestDriftedDiff_GreysApplyAndViewDiff`
Expected: FAIL — buttons render normally.

- [ ] **Step 3: Implement**

In `render.go`, extend the dim condition in `buttonGlyph` (or at the diff-cluster call sites render.go:667-692) so apply-diff + diff glyphs render with `colOverlay0` when `r.states[blockID].Drifted`. In `model.go`, in both dispatch blocks, before dispatching an `apply-diff`/`diff`/`view-diff` button: `if m.blockStates[b.BlockID].Drifted { return m, nil }` (swallow — drifted apply/view-diff are inert; the drift region's button is the live path).

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run TestDriftedDiff && go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/model.go internal/ui/render_test.go
git commit -m "feat(ui): grey out + disable apply/view-diff on a drifted block"
```

---

### Task 4: Drift region + [resolve manually]

**Files:**
- Modify: `internal/ui/render.go` (the drift region below the diff body), `internal/ui/model.go` (route `drift-resolve` → `activateDiffButton`)
- Test: `internal/ui/render_test.go`

**Interfaces:**
- Consumes: `blockRunState.Drifted` (T2), `activateDiffButton` (FC1, model.go:385).

**Context:** Mirror the failed-block "try another fix" tag-button (render.go:807-849): a line of content + a registered `Button{Line, Col, Width:2, Kind, BlockID}`, Col computed by accumulating `indentW(2) + lipgloss.Width(...)` of preceding elements. Emit this region in `code()` right after the diff body (before the `runRegion` call, render.go:757) when `r.states[blk.ID].Drifted`.

- [ ] **Step 1: Write the failing test**

```go
func TestDriftedDiff_RegionAndResolveButton(t *testing.T) {
	states := map[string]blockRunState{"fix": {Drifted: true}}
	src := "```diff {id=fix}\n--- a/cmd/x.go\n+++ b/cmd/x.go\n@@ -1 +1 @@\n-a\n+b\n```\n"
	lines, buttons, _ := Render(src, 100, states, "")
	if !strings.Contains(joinText(lines), "no longer applies") {
		t.Fatalf("missing drift message:\n%s", joinText(lines))
	}
	var has bool
	for _, b := range buttons { if b.BlockID == "fix" && b.Kind == "drift-resolve" { has = true } }
	if !has { t.Fatal("drifted block must have a drift-resolve button") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestDriftedDiff_RegionAndResolveButton`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `code()` (after the diff body, before `runRegion`), when `r.states[blk.ID].Drifted`:
- append a message line: `indentStr + lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render("⚠ ") + colSubtext-styled "this diff no longer applies — the target file changed since it was written"`.
- append a tag-button line with `[resolve manually]`: a glyph (reuse `glyphViewDiff` or a wrench/edit glyph) + label, registering `Button{Line: lineIdx, Col: <accumulated indentW + widths>, Width: 2, Kind: "drift-resolve", Payload: src, BlockID: blk.ID}` (capture `lineIdx := len(r.lines)` before appending the line; `Payload: src` = the patch). (ONE button — `[regenerate]` is W1b.)
In `model.go`, in both dispatch sites, route `b.Kind == "drift-resolve"` to `return m.activateDiffButton(b)` (the FC1 diff view; `b.Payload` is the patch, so it works unchanged). Ensure the drift-resolve button is NOT swallowed by the Task-3 inert gate (that gate keys on `apply-diff`/`diff`/`view-diff`, not `drift-resolve`).

- [ ] **Step 4: Run tests + the package (-race)**

Run: `go test ./internal/ui/ -run TestDriftedDiff_RegionAndResolveButton`
Run: `go build ./... && go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/model.go internal/ui/render_test.go
git commit -m "feat(ui): drift region + [resolve manually] → in-process diff view"
```

---

## Self-Review

**Spec coverage (W1a):** drift detection via `git apply --check` forward/reverse (T1) ✓; `blockRunState.Drifted` + async dispatch at load/orchReady/regen (T2) ✓; grey-out + inert apply/view-diff (T3) ✓; drift region + [resolve manually] → FC1 diff view (T4) ✓. **[regenerate] = W1b (deferred).** Already-applied detected but not acted on (external-applied reflection out of scope) — T1.

**Type consistency:** `CheckDrift → DriftVerdict` (T1) ↔ `driftMsg.Verdict` + the handler (T2) ↔ `blockRunState.Drifted` read by the render (T3, T4); `drift-resolve` button (T4) ↔ `activateDiffButton` (FC1).

**Risks (from grounding):** the async check must never block render (T2 — batch cmds, fire post-EOF/orchReady/regen); drift is per-block so it can't reuse the renderer-wide `shellDisabled` (T3 — per-block dim + inert gate); the drift region's tag-button Col math must accumulate widths like the followup button (T4); re-fire the check after regenerate since blocks are rebuilt (T2).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: the orchestrator test harness for a driver cwd == temp dir + a git repo (reuse/extend the apply/undo tests).
- T2: the exact `orchReadyMsg` handler + the stream-EOF reflow site (model.go:651) + `reArmStreamMsg` (model.go:1514) to fire `driftCheckCmds`; the model test harness for `newTestModelWithDiffBlock`/`stubOrch`.
- T3: `buttonGlyph` (render.go:222) + the diff-cluster call sites (render.go:667-692) for the per-block dim; the two dispatch sites (model.go:842, 1057) for the inert gate; the test suite's way to assert a dimmed glyph.
- T4: the followup tag-button render+Col math (render.go:807-849) to mirror; the `code()` insertion point after the diff body (render.go:757); `joinText`/`Render` test signature.
