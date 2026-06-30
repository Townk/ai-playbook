# Diff drift W1b — [regenerate] per-block re-author (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user click `[regenerate]` on a drifted `diff` block to have the model produce a fresh diff against the *current* target file, spliced into that one block in place — without the whole-playbook reset.

**Architecture:** A scoped re-engagement (new `KindReengageDriftRegen`, non-structured → the model returns the diff as TEXT) runs through a new `Orchestrator.DriftRegen(patch)` that reads the current target, asks for a fresh diff, and drains the stream to a string. The viewer fires it as an isolated per-block async Cmd (mirroring W1a's drift check — NEVER the shared streaming pipeline), splices the new patch into `m.md` by the block's `{id=X}` fence tag, reflows, and re-runs the W1a drift check.

**Tech Stack:** Go; `internal/orchestrator` (Reengage/Events/FanOut), `internal/launcher` (buildReengageEvents), `internal/author` (prompt), `internal/ui` (render/model), `internal/diff` (Parse).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By`; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- **TEXT path, never structured.** `KindReengageDriftRegen` must make `reengageStructured()` return `false` (→ plain `ToolInstruction`, no `submit_playbook`) so the model returns the diff as text. NEVER route through `submit_playbook`/`sess.lastPB` (shared with the commit-metadata seam).
- **Strict isolation from the shared streaming pipeline.** The per-block regenerate Cmd must NOT touch `m.reader`/`m.structured`/`m.bodyProvider`/`m.streaming`/`m.thinking`/`m.md=""` (that is `beginRegenerate`'s whole-pane reset, to AVOID). It behaves like W1a's `driftCheckCmds`: an off-loop `orch` call returning a self-contained msg.
- The whole-playbook `Regenerate` (`KindReengageRegenerate`) is UNCHANGED.
- `EventsFunc` signature is `func(kind ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error)` — pass `base` = current target content, `change` = stale patch. No `req` mutation.
- Target path: `diff.Parse(patch)[0].NewPath`, strip a leading `a/`/`b/`; resolve via `Orchestrator.projectRoot()`/`Drv.Cwd()` (where `applyDiff`/`createFile` anchor), NOT the process cwd.
- **Behavior:** success (got a diff) → splice → reflow → re-fire the W1a drift check (clears drift if it now applies); failure (model error / empty diff) → the block STAYS drifted, `RegenFailed` shows a *"regenerate didn't resolve it — resolve manually"* note. Persistence is session-local `m.md`; W1b does NOT change save behavior.
- `gofmt -l`/`go vet`/`make lint` clean; touched packages pass `go test` (+ `-race` for `internal/ui`). Pre-existing `reengage_test` timeout is load-flaky — re-run in isolation if it trips.

---

### Task 1: `KindReengageDriftRegen` + scoped prompt + `buildReengageEvents` case

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (the `ReengageKind` enum)
- Modify: `internal/launcher/session.go` (`reengageStructured` + `buildReengageEvents`)
- Create/Modify: `internal/author/` (a scoped drift-regen prompt builder)
- Test: `internal/author/*_test.go`, `internal/launcher/session_test.go`

**Interfaces:**
- Produces: `orchestrator.KindReengageDriftRegen` (a new `ReengageKind` const); `author.DriftRegenPrompt(currentFile, stalePatch string) (sys, user string)`; a `buildReengageEvents` `case KindReengageDriftRegen` that builds the scoped prompt and calls `RunHarnessEvents(sys, user, AuthorOptions{… Structured: false})`. Consumed by Task 2.

**Context:** The `ReengageKind` enum is at orchestrator.go:135-146 (`KindReengageRegenerate`/`Followup`/`FinalPlaybook`). `reengageStructured` (session.go:492) is `return kind != KindReengageFollowup`. `buildReengageEvents` (session.go:512) switches on kind building `sys`/`user` then `RunHarnessEvents` (events.go:181; `Structured` gates `StructuredToolInstruction` at events.go:203).

- [ ] **Step 1: Write the failing tests**

```go
// internal/author/driftregen_test.go
func TestDriftRegenPrompt_NamesFileAndStalePatch(t *testing.T) {
	sys, user := DriftRegenPrompt("package main\n\nfunc main() {}\n", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n")
	all := sys + "\n" + user
	for _, want := range []string{"no longer applies", "unified diff", "package main", "+new"} {
		if !strings.Contains(all, want) {
			t.Errorf("drift-regen prompt missing %q", want)
		}
	}
}
```
```go
// internal/launcher/session_test.go
func TestReengageStructured_DriftRegenIsText(t *testing.T) {
	if reengageStructured(orchestrator.KindReengageDriftRegen) {
		t.Fatal("KindReengageDriftRegen must be NON-structured (text diff back)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/author/ -run TestDriftRegenPrompt; go test ./internal/launcher/ -run TestReengageStructured_DriftRegen`
Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement**

`orchestrator.go`: add `KindReengageDriftRegen` to the `ReengageKind` const block (after `KindReengageFinalPlaybook`), with a doc comment ("re-author ONE drifted diff against the current target; non-structured, returns a unified diff as text").
`internal/author/driftregen.go`: 
```go
// DriftRegenPrompt builds the scoped system+user prompts for regenerating ONE diff
// block whose patch no longer applies. The model is asked to return a single fresh
// unified diff (text), not to call submit_playbook.
func DriftRegenPrompt(currentFile, stalePatch string) (sys, user string) {
	sys = "You previously produced a patch that no longer applies to its target file " +
		"(the file changed). Produce a FRESH unified diff that achieves the same intent " +
		"against the CURRENT file content. Output ONLY the unified diff (--- /+++ /@@ …), " +
		"no prose, no fences."
	user = "The stale patch (no longer applies):\n\n" + stalePatch +
		"\n\nThe CURRENT content of the target file:\n\n" + currentFile +
		"\n\nReturn the corrected unified diff."
	return sys, user
}
```
`session.go`: `reengageStructured` → `return kind != orchestrator.KindReengageFollowup && kind != orchestrator.KindReengageDriftRegen`. In `buildReengageEvents`, add:
```go
		case orchestrator.KindReengageDriftRegen:
			sys, user = author.DriftRegenPrompt(base, change) // base=current file, change=stale patch
```
(placed alongside the other `case`s; the existing `RunHarnessEvents(sys, user, …Structured: reengageStructured(kind))` tail handles the rest — confirm it uses the switch's `sys`/`user`.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/author/ ./internal/launcher/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/launcher/session.go internal/author/driftregen.go internal/author/driftregen_test.go internal/launcher/session_test.go
git commit -m "feat(reengage): KindReengageDriftRegen + scoped drift-regen prompt (text diff back)"
```

---

### Task 2: `Orchestrator.DriftRegen`

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `KindReengageDriftRegen` (T1), `re.Events`, `agentstream.FanOut`, `diff.Parse`, `projectRoot()`.
- Produces: `func (o *Orchestrator) DriftRegen(patch string) (string, error)` — returns the fresh diff text. Consumed by Task 4.

**Context:** Mirror `Regenerate` (orchestrator.go:334-377)'s Events path, but drain to a string instead of returning a stream. `FanOut` (agentstream/fanout.go:48) returns `(reader, activity, *Fan)`; `Fan.Body()` (fanout.go:43) holds the final text after EOF. `diff.Parse(patch)[0].NewPath` gives the target (strip `a/`/`b/`); read it via `os.ReadFile(filepath.Join(o.projectRoot(), rel))`.

- [ ] **Step 1: Write the failing test**

```go
func TestDriftRegen_DrainsFreshDiff(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("current\n"), 0o644)
	o := newTestOrchInDir(t, dir)
	// inject a stub EventsFunc that asserts it got the current file + emits a known diff
	fresh := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-current\n+fixed\n"
	o.Reengage.Events = func(kind orchestrator.ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error) {
		if kind != orchestrator.KindReengageDriftRegen { t.Fatalf("wrong kind %v", kind) }
		if !strings.Contains(base, "current") { t.Fatalf("base lacks current file: %q", base) }
		ch := make(chan agentstream.Event, 1)
		ch <- agentstream.Event{Kind: agentstream.Final, Text: fresh}
		close(ch)
		return ch, func() error { return nil }, nil
	}
	got, err := o.DriftRegen("--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-stale\n+fixed\n")
	if err != nil || strings.TrimSpace(got) != strings.TrimSpace(fresh) {
		t.Fatalf("DriftRegen = %q, %v; want the fresh diff", got, err)
	}
}
```
(Adapt to the real `Reengage` field access + `agentstream.Event` shape — read `agentstream/fanout.go` for the Event kinds (`Final`/`TextDelta`). `o.Reengage` may be a field or set via a setter; match the real struct.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run TestDriftRegen`
Expected: FAIL — `DriftRegen` undefined.

- [ ] **Step 3: Implement**

```go
// DriftRegen asks the model for a fresh unified diff for a drifted patch, against the
// CURRENT target file, and returns it as text. It does NOT touch the structured/lastPB
// capture path. The empty string with a nil error means the model produced nothing.
func (o *Orchestrator) DriftRegen(patch string) (string, error) {
	re := o.Reengage
	if re.Events == nil {
		return "", errors.New("regenerate unavailable")
	}
	files := diff.Parse(patch)
	if len(files) == 0 {
		return "", errors.New("could not parse patch target")
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(files[0].NewPath, "b/"), "a/")
	abs := rel
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(o.projectRoot(), rel)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	events, closeFn, err := re.Events(KindReengageDriftRegen, string(content), patch)
	if err != nil {
		return "", err
	}
	reader, _, fan := agentstream.FanOut(events, closeFn, 0)
	if _, err := io.ReadAll(reader); err != nil { // drain to EOF
		return "", err
	}
	return fan.Body(), nil
}
```
(Confirm the real `o.Reengage` accessor + `agentstream` import + `diff` import + `projectRoot()`; mirror how `Regenerate` reaches `re`/`re.Events`.)

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/orchestrator/ -run TestDriftRegen`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): DriftRegen — fresh diff for a drifted patch against the current file"
```

---

### Task 3: `m.md` single-block splice helper

**Files:**
- Create: `internal/ui/splice.go`, `internal/ui/splice_test.go`

**Interfaces:**
- Produces: `func replaceBlockBody(md, id, newBody string) (string, bool)` — returns the markdown with the body of the fenced block tagged `{id=<id>…}` replaced by `newBody` (the opening + closing fence lines kept), and `true` if the block was found. Consumed by Task 5.

**Context:** A diff block in `m.md` is ` ```diff {id=X …}\n<patch>\n``` ` (from `playbook.Render`/`fence`). Find the opening fence line whose tag contains the exact `{id=X` token (ids unique), then the next line that is a bare ` ``` `, and replace the lines strictly between. `normalizeFences` canonicalizes fences at parse, and diff bodies contain no fences, so boundary detection is safe.

- [ ] **Step 1: Write the failing tests**

```go
func TestReplaceBlockBody(t *testing.T) {
	md := "intro\n\n```diff {id=one}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n\n```diff {id=two}\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+d\n```\n\ntail\n"
	out, ok := replaceBlockBody(md, "two", "--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+D2\n")
	if !ok { t.Fatal("block two not found") }
	if !strings.Contains(out, "+D2") { t.Fatal("body not replaced") }
	if !strings.Contains(out, "+b") { t.Fatal("block one must be untouched") }
	if !strings.Contains(out, "```diff {id=two}") || !strings.Contains(out, "intro") || !strings.Contains(out, "tail\n") {
		t.Fatal("fence line / surrounding text must survive")
	}
	if _, ok := replaceBlockBody(md, "missing", "x"); ok {
		t.Fatal("a missing id must return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestReplaceBlockBody`
Expected: FAIL — `replaceBlockBody` undefined.

- [ ] **Step 3: Implement** (`internal/ui/splice.go`)

```go
package ui

import "strings"

// replaceBlockBody replaces the body of the fenced block tagged {id=<id>…} in md with
// newBody, keeping the opening and closing fence lines. Returns ok=false if not found.
func replaceBlockBody(md, id, newBody string) (string, bool) {
	lines := strings.Split(md, "\n")
	open := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") && (strings.Contains(t, "{id="+id+"}") || strings.Contains(t, "{id="+id+" ")) {
			open = i
			break
		}
	}
	if open == -1 {
		return md, false
	}
	close := -1
	for i := open + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			close = i
			break
		}
	}
	if close == -1 {
		return md, false
	}
	body := strings.Split(strings.TrimRight(newBody, "\n"), "\n")
	out := append([]string{}, lines[:open+1]...)
	out = append(out, body...)
	out = append(out, lines[close:]...)
	return strings.Join(out, "\n"), true
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/ -run TestReplaceBlockBody`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/splice.go internal/ui/splice_test.go
git commit -m "feat(ui): replaceBlockBody — splice one fenced block's body by id"
```

---

### Task 4: drift-regen button + "regenerating" status + the async Cmd

**Files:**
- Modify: `internal/ui/render.go` (the `[regenerate]` button in the drift region + a `"regenerating"` spinner case), `internal/ui/results.go` (`driftRegenMsg`), `internal/ui/inprocess.go` (`driftRegenCmd`), `internal/ui/model.go` (the click dispatch + the spin-tick predicate)
- Test: `internal/ui/render_test.go`, `internal/ui/drift_test.go`

**Interfaces:**
- Consumes: `Orchestrator.DriftRegen` (T2), `blockRunState.Drifted` (W1a).
- Produces: a `drift-regen` button (`Kind:"drift-regen"`, `Payload: src`); `type driftRegenMsg struct { ID, NewPatch string; Err error }`; `func (m model) driftRegenCmd(id, patch string) tea.Cmd`; `blockRunState.Status == "regenerating"`. Consumed by Task 5 (the handler).

**Context:** W1a's drift region (render.go ~791) has the `drift-resolve` button. Add a second `[regenerate]` tag-button beside it (mirror the same Button-registration + Col-accumulation). The `"running"` spinner renders via `runRegion` (render.go ~815, `case "running": spinnerLine(...)`) and animates via the `spinTick` predicate (model.go ~746, advances `SpinFrame` for `Status=="running"`). Add a `"regenerating"` status: a `case "regenerating":` spinner with label `"regenerating…"` + add `"regenerating"` to the spin-tick predicate.

- [ ] **Step 1: Write the failing test**

```go
func TestDriftedDiff_HasRegenerateButton(t *testing.T) {
	states := map[string]blockRunState{"fix": {Drifted: true}}
	_, buttons, _ := Render("```diff {id=fix}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n", 100, states, "")
	var resolve, regen bool
	for _, b := range buttons {
		if b.BlockID == "fix" && b.Kind == "drift-resolve" { resolve = true }
		if b.BlockID == "fix" && b.Kind == "drift-regen" { regen = true }
	}
	if !resolve || !regen {
		t.Fatalf("drifted block needs both buttons: resolve=%v regen=%v", resolve, regen)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestDriftedDiff_HasRegenerateButton`
Expected: FAIL — only one button.

- [ ] **Step 3: Implement**

`results.go`: `type driftRegenMsg struct { ID, NewPatch string; Err error }`. Add `RegenFailed bool` to `blockRunState` (used in Task 5's render note).
`render.go`: in the drift region, after the `drift-resolve` button, register a second button on the same (or next) line: `[regenerate]` glyph (reuse `glyphRetry` or similar) + label, `Button{Line, Col:<accumulated>, Width:2, Kind:"drift-regen", Payload:src, BlockID:blk.ID}` (mirror the resolve button's Col math). Add a `case "regenerating":` to the `runRegion` status switch rendering `spinnerLine(st.SpinFrame, "regenerating…", …)`.
`inprocess.go`:
```go
func (m model) driftRegenCmd(id, patch string) tea.Cmd {
	orch := m.orch
	if orch == nil { return nil }
	return func() tea.Msg {
		np, err := orch.DriftRegen(patch)
		return driftRegenMsg{ID: id, NewPatch: np, Err: err}
	}
}
```
`model.go`: in BOTH dispatch sites, route `b.Kind == "drift-regen"`: set `st := m.blockStates[b.BlockID]; st.Status = "regenerating"; st.RegenFailed = false; st.SpinFrame = 0; m.blockStates[b.BlockID] = st`, then `return m, tea.Batch(m.startTick(), m.driftRegenCmd(b.BlockID, b.Payload))`. Add `"regenerating"` to the spin-tick predicate (model.go ~746) so it animates.

- [ ] **Step 4: Run tests + the package**

Run: `go test ./internal/ui/ -run TestDriftedDiff_HasRegenerateButton`
Run: `go build ./... && go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/results.go internal/ui/inprocess.go internal/ui/model.go internal/ui/render_test.go
git commit -m "feat(ui): [regenerate] drift button + regenerating spinner + async DriftRegen cmd"
```

---

### Task 5: `driftRegenMsg` handler — splice, reflow, re-check, failure note

**Files:**
- Modify: `internal/ui/model.go` (the `driftRegenMsg` handler), `internal/ui/render.go` (the `RegenFailed` note)
- Test: `internal/ui/drift_test.go`

**Interfaces:**
- Consumes: `driftRegenMsg` + `driftRegenCmd` (T4), `replaceBlockBody` (T3), `driftCheckCmds` (W1a), `blockRunState.RegenFailed` (T4).

**Context:** On a fresh diff, splice it into `m.md` (T3), `reflow()`, clear the regenerating status, and re-run the W1a drift check (which clears `Drifted` if the new patch applies). On error/empty, keep `Drifted`, set `RegenFailed`, and the drift region shows the alternate note.

- [ ] **Step 1: Write the failing test**

```go
func TestDriftRegenMsg_SuccessSplicesAndRechecks(t *testing.T) {
	m := newTestModelWithDiffBlock(t, "fix") // m.md has ```diff {id=fix} ... ```
	m.blockStates["fix"] = blockRunState{Status: "regenerating", Drifted: true}
	fresh := "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+FRESH\n"
	m2i, _ := m.Update(driftRegenMsg{ID: "fix", NewPatch: fresh})
	m2 := m2i.(model)
	if m2.blockStates["fix"].Status == "regenerating" { t.Fatal("regenerating status must clear") }
	if !strings.Contains(m2.md, "+FRESH") { t.Fatal("m.md must carry the spliced patch") }
}

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestDriftRegenMsg`
Expected: FAIL — no handler.

- [ ] **Step 3: Implement**

`model.go` — add `case driftRegenMsg:` (near the `driftMsg` handler):
```go
case driftRegenMsg:
	st := m.blockStates[msg.ID]
	st.Status = ""
	if msg.Err != nil || strings.TrimSpace(msg.NewPatch) == "" {
		st.RegenFailed = true            // stays Drifted; the region shows the note
		m.blockStates[msg.ID] = st
		m.reflow()
		return m, nil
	}
	st.RegenFailed = false
	m.blockStates[msg.ID] = st
	if newMd, ok := replaceBlockBody(m.md, msg.ID, msg.NewPatch); ok {
		m.md = newMd
	}
	m.reflow()
	return m, m.driftCheckCmds() // re-check; clears Drifted if the fresh patch applies
```
`render.go` — in the drift region message, when `r.states[blk.ID].RegenFailed`, render *"⚠ regenerate didn't resolve it — resolve manually"* instead of the plain drift message (the two tag-buttons still render).

- [ ] **Step 4: Run tests + the package (-race)**

Run: `go test ./internal/ui/ -run TestDriftRegenMsg`
Run: `go build ./... && go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/render.go internal/ui/drift_test.go
git commit -m "feat(ui): driftRegenMsg handler — splice fresh patch, reflow, re-check drift"
```

---

## Self-Review

**Spec coverage (W1b):** Fork 1 text-diff-back — `KindReengageDriftRegen` + scoped prompt + non-structured `buildReengageEvents` case (T1) + `DriftRegen` drain (T2) ✓; Fork 2 `m.md` splice by `{id=X}` (T3) ✓; Fork 3 per-block async — `drift-regen` button + `regenerating` status + `driftRegenCmd`/`driftRegenMsg` (T4) + the splice/reflow/re-check handler + failure note (T5) ✓. Behavior: success→re-check clears drift; failure→stays drifted + RegenFailed note (T5). Isolation from the shared pipeline: the cmd only calls `orch.DriftRegen` + returns a msg (T4/T2) — never touches `m.reader`/`structured`/`bodyProvider`/`streaming` ✓.

**Type consistency:** `KindReengageDriftRegen` (T1) ↔ `DriftRegen`'s `re.Events(KindReengageDriftRegen,…)` (T2) ↔ `driftRegenCmd` → `driftRegenMsg{ID,NewPatch,Err}` (T4) ↔ the handler (T5); `replaceBlockBody(md,id,newBody)(string,bool)` (T3) ↔ the handler (T5); `RegenFailed` (T4 field) ↔ the render note (T5); `driftCheckCmds` reused from W1a (T5).

**Risks (from the spec):** the `m.md` splice (T3 — unique ids + fence-free diff bodies + `normalizeFences` mitigate; thorough test); strict pipeline isolation (T4 — the cmd is a self-contained orch call like `driftCheckCmds`, NOT `beginRegenerate`); the scoped prompt must yield a unified diff (T1 — `Structured:false` + an explicit "output ONLY the diff" instruction).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T1: that `buildReengageEvents`' tail `RunHarnessEvents(sys, user, …)` consumes the switch's `sys`/`user`; the exact `author` prompt-builder placement.
- T2: the real `o.Reengage` accessor + `agentstream.Event` kinds (`Final`/`TextDelta`) + the `Regenerate` Events-path idiom to mirror; the orchestrator test's stub-EventsFunc feasibility.
- T3: `normalizeFences`/`fence` exact output (the `{id=X}` vs `{id=X …}` tag forms) — the helper matches both.
- T4: the drift-region Col math (mirror W1a's `drift-resolve`); the `runRegion` status switch + the spin-tick predicate (model.go ~746); both click-dispatch sites.
- T5: the `newTestModelWithDiffBlock` harness (W1a's drift tests); the drift-region message render (W1a Task 4) to add the `RegenFailed` branch.
