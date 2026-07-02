# `--assisted` env-gate-at-start fix + coverage (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In `--assisted` mode, confirm a playbook's declared variables **as soon as it loads** (before the ready cursor + footer), matching the spec — not deferred to the first `[Run]` press. Plus tutorial coverage so this path is demonstrable/testable.

**Bug:** the shipped `--assisted` reuses `runOrGate` (gate on first `[Run]`), diverging from the spec. Spec `docs/specifications/run-modes-assisted-auto.md`:
- §4 **Start.** "if project-bound with declared env, run the env gate **once** (reuse `beginGate`, `gateSatisfied`); then set `readyID`, auto-scroll, and raise the step footer."
- §5 "`--assisted`: reuse the interactive `beginGate` **once at start**."

**Architecture:** Add an "assisted-start" flavor of the confirm gate: when the guided walk begins (`maybeStartAssisted`), if there are unconfirmed declared vars, raise the existing `beginGate` dialogs; on completion, **export the vars and enter the ready state** (`startAssisted`) — running no block (unlike the normal gate which runs the deferred block). No vars ⇒ straight to the ready footer as today.

**Tech Stack:** Go; bubbletea v2; `internal/ui` (`confirm_gate.go`, `assisted.go`, `model.go`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (`git commit`, NEVER `--no-gpg-sign`; if signing times out, STOP + report BLOCKED — user re-unlocks gpg with `! echo x | gpg --clearsign`); verify `git log -1 --format=%G?` == `G`. **NO `Co-Authored-By`/AI-attribution trailers.** `git add` explicit paths only.
- **Repo at `~/Projects/langs/go/ai-playbook`** — `go`/`git` run there.
- The existing confirm gate (used by the default pager via `runOrGate`) and the verify-success confirm MUST keep working — this ADDS an assisted flavor, it doesn't change the default/manual gate path.
- Gates: `gofmt -l`, `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` (clean), `go test` (+ `-race` for `internal/ui` — the suite is slow ~2.5min; `reengage_test` is load-flaky, re-run in isolation if it trips).

---

### Task 1: fire the env gate at assisted start

**Files:**
- Modify: `internal/ui/confirm_gate.go` (`confirmGate` struct + `beginAssistedGate` + `afterGroup` branch + `enterAssistedReady`), `internal/ui/assisted.go` (`maybeStartAssisted` → returns a cmd + fires the gate; `assistedStartMsg`), `internal/ui/model.go` (the `maybeStartAssisted` call site + the `assistedStartMsg` Update arm)
- Test: `internal/ui/assisted_test.go`

**Interfaces:**
- Produces: `assistedStartMsg struct{}`; `confirmGate.assistedStart bool`; `func (m model) beginAssistedGate() (model, tea.Cmd)`; `func (m model) enterAssistedReady(values map[string]string) (model, tea.Cmd)`; `maybeStartAssisted` becomes `func (m model) maybeStartAssisted() (model, tea.Cmd)`.

**Context:** Read `confirm_gate.go` first — `beginGate` (`:114`), `runGateBlock` (`:runs the deferred block via export tea.Sequence + emitAction`), `afterGroup` (`calls runGateBlock(g.block, g.values) when all groups confirmed`), `advanceGate`, `buildConfirmVars`/`groupSizes`/`buildExportCmd`/`orchDriver`/`gateExportTimeout`.

1. **`confirmGate` struct** — add `assistedStart bool` (marks a gate whose completion enters the assisted ready state instead of running a block).
2. **`beginAssistedGate()`** (mirror `beginGate` but for assisted start, deferring no block):
   ```go
   func (m model) beginAssistedGate() (model, tea.Cmd) {
       vars := buildConfirmVars(m.confirmEnv, m.projectRoot, os.Getenv)
       if len(vars) == 0 || m.gateSatisfied {
           m.gateSatisfied = true
           return m.startAssisted(), nil // nothing to confirm → straight to the ready footer
       }
       g := &confirmGate{values: map[string]string{}, assistedStart: true}
       for _, v := range vars { g.values[v.Name] = v.Value }
       i := 0
       for _, sz := range groupSizes(len(vars)) { g.groups = append(g.groups, vars[i:i+sz]); i += sz }
       m.gate = g
       return m.raiseGroupConfirm()
   }
   ```
3. **`afterGroup()`** — branch on `assistedStart` when all groups are confirmed:
   ```go
   func (m model) afterGroup() (model, tea.Cmd) {
       g := m.gate
       if g.gi < len(g.groups) { return m.raiseGroupConfirm() }
       values, assisted, block := g.values, g.assistedStart, g.block
       m.gate = nil
       if assisted { return m.enterAssistedReady(values) }
       return m.runGateBlock(block, values)
   }
   ```
4. **`enterAssistedReady(values)`** — export the vars (same driver path as `runGateBlock`), THEN enter the ready state via `assistedStartMsg` (so the footer only appears after the export):
   ```go
   func (m model) enterAssistedReady(values map[string]string) (model, tea.Cmd) {
       m.gateSatisfied = true
       exportCmd := buildExportCmd(values)
       drv := m.orchDriver()
       return m, tea.Sequence(
           func() tea.Msg { if drv != nil && exportCmd != "" { drv.RunMain(exportCmd, gateExportTimeout) }; return nil },
           func() tea.Msg { return assistedStartMsg{} },
       )
   }
   ```
5. **`assistedStartMsg`** (in `assisted.go`) + its Update arm (in `model.go`, next to the other msg cases): `case assistedStartMsg: m = m.startAssisted(); return m, nil`.
6. **`maybeStartAssisted`** (`assisted.go`) → return a cmd + fire the gate:
   ```go
   func (m model) maybeStartAssisted() (model, tea.Cmd) {
       if m.assisted && !m.assistedStarted {
           m.assistedStarted = true
           return m.beginAssistedGate()
       }
       return m, nil
   }
   ```
7. **Call site** (`model.go:1085`, the stream-EOF handler): change `m = m.maybeStartAssisted()` to `m, startCmd := m.maybeStartAssisted()` and fold `startCmd` into that arm's returned cmd (it currently returns `m, m.driftCheckCmds()` — make it `return m, tea.Batch(startCmd, m.driftCheckCmds())`).

Note: the footer `[Run]` still calls `runOrGate`, but by the time the footer shows, `gateSatisfied` is true, so `runOrGate` won't re-gate — the first step runs directly. The default/manual pager path (non-assisted `runOrGate` → `beginGate`) is untouched.

- [ ] **Step 1: Write the failing tests**

```go
// a project-bound playbook body with a declared env var, run assisted
func assistedEnvModel(t *testing.T) model {
	body := "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n"
	m := newModel("T", body)
	m.width, m.height = 80, 24
	m.assisted = true
	m.confirmEnv = map[string]frontmatter.EnvValue{"DATA_DIR": {Value: "/tmp/x", Why: "the data dir"}}
	m.reflow()
	return m
}

func TestAssisted_EnvGateFiresAtStart(t *testing.T) {
	m := assistedEnvModel(t)
	m2, _ := m.maybeStartAssisted()
	// The gate is raised BEFORE the footer/cursor: an ask overlay is up, no step footer yet.
	if m2.gate == nil || !m2.askMode {
		t.Fatalf("declared vars → the env gate must be raised at start; gate=%v askMode=%v", m2.gate != nil, m2.askMode)
	}
	if m2.assistedFooter == "step" || m2.readyID != "" {
		t.Fatalf("the step footer/cursor must NOT show until the vars are confirmed; footer=%q ready=%q", m2.assistedFooter, m2.readyID)
	}
}

func TestAssisted_NoVars_NoGate_StraightToFooter(t *testing.T) {
	m := assistedEnvModel(t)
	m.confirmEnv = nil // no declared vars
	m2, _ := m.maybeStartAssisted()
	if m2.gate != nil { t.Fatal("no vars → no gate") }
	if m2.assistedFooter != "step" || m2.readyID != "a" {
		t.Fatalf("no vars → straight to the ready footer on step a; footer=%q ready=%q", m2.assistedFooter, m2.readyID)
	}
	if !m2.gateSatisfied { t.Error("gateSatisfied should be set when there's nothing to confirm") }
}

func TestAssisted_AfterGateConfirmed_ShowsFooter(t *testing.T) {
	// After the assisted-start gate completes, the assistedStartMsg enters the ready state.
	m := assistedEnvModel(t)
	m.gateSatisfied = true // simulate post-confirm
	m3 := mustModel(m.Update(assistedStartMsg{}))
	if m3.assistedFooter != "step" || m3.readyID != "a" {
		t.Fatalf("assistedStartMsg must enter the ready footer; footer=%q ready=%q", m3.assistedFooter, m3.readyID)
	}
}
```
(Adapt the `frontmatter.EnvValue` import + `mustModel`/`newModel` helpers to the real test file. If asserting `askMode`/`gate` directly is awkward, assert via the raised ask overlay the gate produces — read how existing confirm-gate tests observe the gate.)

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/ui/ -run 'Assisted_EnvGate|Assisted_NoVars|Assisted_AfterGate'` → FAIL.
- [ ] **Step 3: Implement** the struct field + `beginAssistedGate` + `afterGroup` branch + `enterAssistedReady` + `assistedStartMsg` (+ Update arm) + `maybeStartAssisted` signature + the call-site batch.
- [ ] **Step 4: Run to verify they pass** — the new tests; then `go test ./internal/ui/ -run 'Assisted|Confirm|Gate'` (existing assisted + confirm-gate tests stay green — the default `runOrGate`/`beginGate` path is unchanged); `go build ./...`; `go test -race ./internal/ui/`.
- [ ] **Step 5: Commit** — `git add internal/ui/confirm_gate.go internal/ui/assisted.go internal/ui/model.go internal/ui/assisted_test.go && git commit -m "fix(ui): --assisted confirms declared vars at load (env gate at start, per spec)"`

---

### Task 2: tutorial coverage for `--assisted` + variables

**Files:**
- Modify: `examples/06-portable-and-env.md`, `examples/07-run-modes.md`

**Context:** No example exercises `--assisted`/`--auto` against a variable-bearing playbook. ch.06 (`examples/06-portable-and-env.md`) is the variables chapter (declares `env:` PROJECT_ROOT/DATA_DIR, `project_bound`). Add the coverage there + a cross-ref from ch.07.

1. **`examples/06-portable-and-env.md`** — in the "## The env map and the confirmation gate" section, add a short paragraph: running this playbook non-interactively still honors the variables — `ai-playbook run --assisted --file examples/06-portable-and-env.md` opens the guided viewer and **confirms the variables the moment it loads** (before the first step); `ai-playbook run --auto --file …` requires them already set in the environment (it can't prompt) and errors listing any missing ones. Keep the chapter's tone; do not disturb the existing gate prose.
2. **`examples/07-run-modes.md`** — in the "## Assisted run" section, add one cross-ref sentence: if a playbook declares variables (Chapter 06), `--assisted` confirms them as soon as the viewer opens, before the first step's footer appears.

- [ ] **Step 1: Edit** both files per Context.
- [ ] **Step 2: Verify** — re-read both sections; the ch.06 note gives the runnable `--assisted` command + the load-time-confirm wording; the ch.07 cross-ref points at ch.06. (No test — docs.) `grep -n "assisted" examples/06-portable-and-env.md` shows the new note.
- [ ] **Step 3: Commit** — `git add examples/06-portable-and-env.md examples/07-run-modes.md && git commit -m "docs(examples): cover --assisted/--auto with variables (ch.06 + ch.07 cross-ref)"`

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/ui` empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean; `go test -race ./internal/ui/` PASS.
- [ ] `go install ./cmd/ai-playbook`, then **live-verify** (needs a TTY): `ai-playbook run --assisted --file examples/06-portable-and-env.md` → the variable-confirmation dialog appears **immediately on load**, before the ready cursor + footer; confirm it → the vars export, the ready footer appears on the first step; run through. (`--auto` with the vars exported → runs; without → errors listing the missing var.)

## Self-review notes (coverage vs spec)

- Spec §4/§5 (env gate once at assisted start) → Task 1 (`beginAssistedGate` at `maybeStartAssisted`; `afterGroup`→`enterAssistedReady` exports + enters ready via `assistedStartMsg`; no block run). The default/manual `runOrGate`→`beginGate` path is untouched (still gate-on-first-Run for the free-form pager, which is correct there). Coverage → Task 2.
