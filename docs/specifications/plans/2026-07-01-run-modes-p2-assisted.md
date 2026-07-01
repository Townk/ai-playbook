# Run modes P2 — `--assisted` guided pager (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ai-playbook run --assisted --file <pb>` — the existing fullscreen pager driven as a *guided* walk: a **ready** cursor points at the next runnable step, the doc auto-scrolls that step to ~1/3 of the viewport, and a focusable footer `[ Run ] [ Skip ] [ Quit ]` confirms each step (on a failure → `[ Roll back ] [ Leave as-is ] [ Quit ]`). No modal ever occludes the document.

**Architecture:** A guided layer on top of the current viewer, entered when `m.assisted` is set (via a new `ui.SetAssisted` seam, mirroring `SetAutoRollback`). A `readyID` cursor names the next runnable step (`autorun.NextRunnable` over the existing `toAutorunBlocks()`). Pressing the footer's **Run** triggers the ready block's *existing* `runOrGate` path (so the env gate and rollback machinery are reused UNCHANGED); the single `resultMsg` completion signal advances the cursor (ok → next + auto-scroll) or raises the failure footer. The footer reuses the existing `confirmButtonLabel` primitive as `Screen: true` buttons (like `appendConfirmButtons`) — we do NOT refactor the verify-success confirm row. Plan 1 already shipped the shared substrate (`autorun.NextRunnable`, the `"skipped"` status const, `toAutorunBlocks`).

**Tech Stack:** Go; bubbletea v2 (`charm.land/bubbletea/v2`); lipgloss v2; `internal/ui` (model/render/confirm_gate); `internal/launcher` (`runcmd.go`); `internal/autorun` (`NextRunnable`, `StatusSkipped`).

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits (`git commit`, NEVER `--no-gpg-sign`); verify `git log -1 --format=%G?` == `G`. **NO `Co-Authored-By`/AI-attribution trailers.** `git add` explicit paths only. Commit only at each task's final step.
- **Repo at `~/Projects/langs/go/ai-playbook`** — all `go`/`git` run there (`cd …` or `git -C …`); the default shell cwd is a different repo.
- **`--assisted` is the FULLSCREEN pager in guided mode — NOT a line-based `[y/n/q]` stdin prompt.** (The ch.07 example's current "Assisted run" prose describes a line-based flow; Task 6 REWRITES it to the guided-pager reality.)
- **Reuse, don't refactor, the confirm row.** The verify-success confirm (`confirmResolved`/`resolveConfirm`/`confirmButtonsRowString`, `model.go`) MUST keep working — reuse only the `confirmButtonLabel(label, kind, accent, focused)` primitive (`model.go:2797`) for the assisted footer. The existing confirm tests must stay green.
- **Reuse the run/gate/rollback machinery UNCHANGED.** Footer **Run** calls the existing `runOrGate(readyButton)` (`confirm_gate.go:101`) so the env gate fires exactly as in manual mode; failure **Roll back** calls the existing `beginRollback(failedID)` (`model.go:2282`). Do NOT fork these.
- **`--assisted` and `--auto` are mutually exclusive** (usage error if both). `--assisted` goes through the pager (`uiMainFn`), NOT the headless `autorun.Run`.
- **Exit codes:** clean quit/completion (`q`) → 0; `Ctrl-C` or quitting with an unresolved failure → non-zero (1); **Roll back** resolves a failure → 0 (spec: "non-zero if any step failed AND was not rolled back").
- Verification gates (in the repo): `gofmt -l internal/ui internal/launcher` (empty), `go build ./...`, `go vet ./...`, `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` (clean — catches what `go test`/`build`/`gofmt` miss), `go test` on touched packages (`internal/ui` also `-race`; the `ui` suite is ~2.5 min and `reengage_test` is load-flaky — re-run in isolation if it trips).

---

### Task 1: Plumbing — `--assisted` flag, `SetAssisted` seam, exit-code

**Files:**
- Modify: `internal/launcher/runcmd.go` (`resolveRunArgs`, `RunMain`) + `internal/launcher/runcmd_test.go`
- Modify: `internal/ui/main.go` (`SetAssisted`/`pendingAssisted`, consume-once, `Main` exit-code capture) + `internal/ui/model.go` (`assisted`, `exitCode` fields) + `internal/ui/main_test.go` (or where `Set*` is tested)

**Interfaces:**
- Produces: `func ui.SetAssisted(v bool)`; `model.assisted bool`; `model.exitCode int` (read by `Main` after `prog.Run()`). `resolveRunArgs` gains `modeAssisted` (the `runMode` enum already exists from P1 with `modeDefault`/`modeAuto`).

**Context:**
- `resolveRunArgs` (`runcmd.go`, P1's `runArgs`): add `--assisted` (bool) → `Mode: modeAssisted`. Validation: `--assisted` + `--auto` both set → `fmt.Errorf` usage error; `--assisted` + `--auto-rollback`/`--no-auto-rollback` → leave as-is (those are auto/default-pager flags; a reviewer-neutral choice is to error if `--no-auto-rollback` is combined with `--assisted`, matching its "only with --auto" rule).
- `RunMain` (`runcmd.go:76`): add a `case ra.Mode == modeAssisted` before the default path → `setAssistedFn(true)` then `runFile(ra.Value)`/`runPlaybook(ra.Value)` by `ra.Kind` (the SAME viewer path as default — assisted rides the pager). Add the seam `var setAssistedFn = ui.SetAssisted` (mirror `setAutoRollbackFn`, `runcmd.go:56`).
- `main.go`: add `var pendingAssisted bool` + `func SetAssisted(v bool) { pendingAssisted = v }` (mirror `SetAutoRollback`, `main.go:39-43`); consume at the `main.go:423-449` cluster: `m.assisted = pendingAssisted; pendingAssisted = false`.
- **Exit-code capture:** `Main()` (`main.go:229`, returns `int`) currently does `if _, err := prog.Run(); err != nil { … }`. Change to capture the final model: `fm, err := prog.Run()`; keep the `err` branch (`return 1`); then before `return 0`, add `if mm, ok := fm.(model); ok && mm.exitCode != 0 { return mm.exitCode }`. Add `exitCode int` to the model struct (default 0).

- [ ] **Step 1: Write the failing tests**

```go
// runcmd_test.go — flag parsing + mutual exclusion
func TestResolveRunArgs_Assisted(t *testing.T) {
	ra, err := resolveRunArgs([]string{"--assisted", "--file", "x.md"})
	if err != nil || ra.Mode != modeAssisted || ra.Kind != "file" || ra.Value != "x.md" {
		t.Fatalf("--assisted: %+v err=%v", ra, err)
	}
	if _, err := resolveRunArgs([]string{"--assisted", "--auto", "--file", "x.md"}); err == nil {
		t.Error("--assisted with --auto must error (mutually exclusive)")
	}
}
```
```go
// main_test.go — seam consume-once (extract takeAssisted() if the cluster uses helpers, else assert via a test that Main-consumption clears it)
func TestSetAssisted_ConsumeOnce(t *testing.T) {
	SetAssisted(true)
	if !pendingAssisted { t.Fatal("SetAssisted must set pendingAssisted") }
	// consumption clears it — mirror the existing pendingAutoRollback consume-once test pattern.
}
```

- [ ] **Step 2: Run to verify they fail** — `cd ~/Projects/langs/go/ai-playbook && go test ./internal/launcher/ -run Assisted; go test ./internal/ui/ -run SetAssisted` → FAIL.
- [ ] **Step 3: Implement** the flag, validation, `RunMain` branch, the `setAssistedFn`/`SetAssisted` seam, the model fields, and the `Main` exit-code capture.
- [ ] **Step 4: Run to verify they pass** — the two tests above; `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/launcher/runcmd.go internal/launcher/runcmd_test.go internal/ui/main.go internal/ui/model.go internal/ui/main_test.go && git commit -m "feat(run): --assisted flag + SetAssisted seam + Main exit-code"`

---

### Task 2: Assisted engine — cursor, advance, skip, scroll (no footer UI yet)

**Files:**
- Modify: `internal/ui/model.go` (assisted state fields + methods) + a new `internal/ui/assisted.go` for the engine (keep model.go from growing further) + `internal/ui/assisted_test.go`

**Interfaces:**
- Consumes: `toAutorunBlocks()` (`model.go:2258`), `autorun.NextRunnable` (`internal/autorun/sequence.go:55`), `autorun.StatusSkipped`, `markRunning`, `beginRollback` (existing).
- Produces (consumed by Tasks 3-4):
  - Model fields: `readyID string` (cursor; "" when none), `assistedFooter string` (`""` hidden | `"step"` | `"failure"` | `"done"`), `footerFocus int`, `assistedFailedID string`. (`assisted`/`exitCode` exist from Task 1.)
  - `func (m model) assistedNextID() string` — the next runnable block id via `autorun.NextRunnable(m.toAutorunBlocks())`; "" when none.
  - `func (m model) startAssisted() model` — sets `readyID = assistedNextID()`; if non-empty → `assistedFooter="step"`, `footerFocus=0`, scroll to it; else `assistedFooter="done"`.
  - `func (m model) assistedAdvance() model` — recompute `readyID`; non-empty → `assistedFooter="step"`, `footerFocus=0`, scroll to it; empty → `assistedFooter="done"`, `readyID=""`.
  - `func (m model) assistedSkip() model` — set `m.blockStates[readyID].Status = autorun.StatusSkipped`; then `assistedAdvance()`.
  - `func (m model) scrollToFraction(id string, num, den int) model` — `line := m.lineForBlock(id); m.yOff = line - m.body()*num/den; clampScroll()`. `lineForBlock(id)` scans `m.buttons` for the first `b.BlockID==id` (every block has a copy button carrying its `.Line`); returns 0 if not found.

**Context:**
- **Start** is invoked from `Main` after model construction (Task 1 sets `m.assisted`): in `main.go`, right before `tea.NewProgram`, add `if m.assisted { m = m.startAssisted() }`. (A first `reflow()` must have populated `m.buttons`/`m.lines` so `lineForBlock` works — `newModel`+`reflow` already run before this point; confirm.)
- **Advance hook** lives in the `resultMsg` handler (`model.go:1736`), AFTER the status switch settles `st.Status` (post `model.go:1793`) and AFTER `m.blockStates[msg.ID]=st`: guard on `m.assisted && msg.ID == m.readyID && prevAction != "rollback"`:
  - `st.Status == "ok"` → `m = m.assistedAdvance()`.
  - `st.Status == "failed"` → `m.assistedFailedID = msg.ID; m.assistedFooter = "failure"; m.footerFocus = 0; m.exitCode = 1`.
  This runs before the existing auto-rollback/verify-followup arms; gate it so it does NOT fire for non-assisted runs. (Do not disturb the existing `m.autoRollback` arm — in assisted mode `m.autoRollback` is false, so it won't fire.)
- **Scroll math** mirrors `pinAnnouncement` (`model.go:3301-3328`): set `m.yOff` from the block's line minus `body()/3`, then `clampScroll()` (`model.go:860`). Place the ready step ~1/3 down.
- **Skip propagation:** `autorun.NextRunnable` already treats a block whose need is `"skipped"` (or any non-`"ok"`) as unrunnable, so skipping a step's dependents naturally never become `readyID` — no transitive marking needed (they render as un-run; that is acceptable per spec).

- [ ] **Step 1: Write the failing tests** (state transitions, driven directly + via `resultMsg` like the rollback tests):

```go
// assisted_test.go
func TestStartAssisted_SetsCursorToFirstRunnable(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow()
	m = m.startAssisted()
	if m.readyID != "a" || m.assistedFooter != "step" {
		t.Fatalf("start: readyID=%q footer=%q, want a/step", m.readyID, m.assistedFooter)
	}
}

func TestAssisted_AdvanceOnOk_ThenDone(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow(); m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "running", Action: "run"}
	m2 := mustModel(m.Update(resultMsg{ID: "a", Exit: 0}))
	if m2.readyID != "b" { t.Fatalf("after a ok, cursor should be b; got %q", m2.readyID) }
	m2.blockStates["b"] = blockRunState{Status: "running", Action: "run"}
	m3 := mustModel(m2.Update(resultMsg{ID: "b", Exit: 0}))
	if m3.assistedFooter != "done" || m3.readyID != "" {
		t.Fatalf("after b ok, should be done; got footer=%q ready=%q", m3.assistedFooter, m3.readyID)
	}
}

func TestAssisted_FailureRaisesFailureFooter(t *testing.T) {
	m := newModel("T", "```bash {id=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow(); m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "running", Action: "run"}
	m2 := mustModel(m.Update(resultMsg{ID: "a", Exit: 1}))
	if m2.assistedFooter != "failure" || m2.assistedFailedID != "a" || m2.exitCode != 1 {
		t.Fatalf("failure: footer=%q failed=%q exit=%d", m2.assistedFooter, m2.assistedFailedID, m2.exitCode)
	}
}

func TestAssisted_SkipMarksSkippedAndAdvances(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true
	m.reflow(); m = m.startAssisted()
	m2 := m.assistedSkip()
	if m2.blockStates["a"].Status != autorun.StatusSkipped { t.Error("a must be skipped") }
	if m2.readyID != "b" { t.Errorf("cursor should advance to b; got %q", m2.readyID) }
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/ui/ -run Assisted` → FAIL (undefined methods).
- [ ] **Step 3: Implement** `internal/ui/assisted.go` (the fields go on the model struct in model.go; the methods in assisted.go) + the `resultMsg` advance hook + the `main.go` `startAssisted` call.
- [ ] **Step 4: Run to verify they pass** — `go test ./internal/ui/ -run Assisted`; `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/ui/assisted.go internal/ui/assisted_test.go internal/ui/model.go internal/ui/main.go && git commit -m "feat(ui): assisted engine — ready cursor, advance, skip, scroll-to-⅓"`

---

### Task 3: The focusable footer (render + Screen buttons + reservation)

**Files:**
- Modify: `internal/ui/assisted.go` (footer render + button register) + `internal/ui/model.go` (`body()` reservation, `View()` painting, `reflow()` call) + `internal/ui/assisted_test.go`

**Interfaces:**
- Consumes: `confirmButtonLabel(label, kind, accent string, focused bool) string` (`model.go:2797`), the `Screen: true` Button pattern (`appendConfirmButtons`, `model.go:2835`), `body()`/`View()`.
- Produces:
  - `func (m model) assistedFooterButtons() []footerBtn` — the button set for the current `assistedFooter` mode. `type footerBtn struct{ Label, Kind, Accent string }`. `"step"` → `{Run, "assist-run", colGreen}`, `{Skip, "assist-skip", colSubtext}`, `{Quit, "assist-quit", colPeach}`. `"failure"` → `{Roll back, "assist-rollback", colPeach}` (only if `m.anyRollbackable()`), `{Leave as-is, "assist-leave", colSubtext}`, `{Quit, "assist-quit", colPeach}`. `"done"` → `{Quit, "assist-quit", colGreen}`. `""` → nil.
  - `func (m model) assistedFooterRowString() string` — a context line (`Step <n>/<total> · <id> · <cmd>` for step; `Step failed · <id>` for failure; `Assisted run complete — <ran> ran, <skipped> skipped` for done) + the buttons row built from `confirmButtonLabel(b.Label, b.Kind, b.Accent, i==m.footerFocus)`.
  - `func (m *model) appendAssistedFooter()` — like `appendConfirmButtons`: compute the screen row (`m.height-3`, reuse `confirmButtonsScreenRow` semantics) and register a `Screen: true` Button per footer button with its Kind + BlockID `"assist"`.

**Context:**
- Footer shows only when `m.assisted && m.assistedFooter != "" && !m.askMode` and the ready block is not mid-run (`m.blockStates[m.readyID].Status != "running"`). While a step runs, the footer is hidden (the block shows its own `running…` spinner); it reappears on the `resultMsg` advance.
- `body()` (`model.go:785`): when the footer is shown, shrink the viewport by the footer height (context line(s) + buttons row + padding) — mirror the `confirmQuestionLines()+3` shrink at `model.go:788`. Add a `assistedFooterLines()` helper.
- `View()` (`model.go:3472`): paint the footer context + buttons row into the reserved bottom rows, mirroring how the confirm question+buttons are painted. `reflow()` (`model.go:807`) calls `appendAssistedFooter()` alongside `appendConfirmButtons()`.
- Do NOT show the assisted footer and the verify-confirm row simultaneously (assisted runs don't hit the verify-success wrap-up; but guard `assistedFooter` rendering behind `!m.confirmResolved` to be safe).

- [ ] **Step 1: Write the failing tests**

```go
func TestAssistedFooter_StepButtons(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted()
	out := strip(m.viewString())
	for _, w := range []string{"Run", "Skip", "Quit"} {
		if !strings.Contains(out, w) { t.Errorf("step footer missing %q:\n%s", w, out) }
	}
	// Screen buttons registered for click.
	if buttonForBlock(m.buttons, "assist", "assist-run") == nil { t.Error("no assist-run Screen button") }
}

func TestAssistedFooter_FailureButtons(t *testing.T) {
	m := newModel("T", "```bash {id=a rollback=undo-a}\ntrue\n```\n\n```bash {id=undo-a}\ntrue\n```\n\n```bash {id=boom needs=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.assistedFooter = "failure"; m.assistedFailedID = "boom"; m.reflow()
	out := strip(m.viewString())
	for _, w := range []string{"Roll back", "Leave as-is", "Quit"} {
		if !strings.Contains(out, w) { t.Errorf("failure footer missing %q:\n%s", w, out) }
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/ui/ -run AssistedFooter` → FAIL.
- [ ] **Step 3: Implement** the footer render, `appendAssistedFooter`, the `body()` reservation, and the `View()` painting.
- [ ] **Step 4: Run to verify they pass** — the footer tests; `go build ./...`; a quick manual `go test ./internal/ui/ -run 'Confirm'` to confirm the verify-confirm row still renders (shared `confirmButtonLabel` untouched).
- [ ] **Step 5: Commit** — `git add internal/ui/assisted.go internal/ui/model.go internal/ui/assisted_test.go && git commit -m "feat(ui): assisted footer — focusable Run/Skip/Quit + failure buttons"`

---

### Task 4: Footer input — keyboard + click → actions

**Files:**
- Modify: `internal/ui/model.go` (key handling near `model.go:1512`; click dispatch near `model.go:1241`) + `internal/ui/assisted.go` (action handlers) + `internal/ui/assisted_test.go`

**Interfaces:**
- Consumes: `runOrGate` (`confirm_gate.go:101`), `beginRollback` (`model.go:2282`), `assistedSkip`/`assistedAdvance` (Task 2), `emitAction`.
- Produces: `func (m model) assistedActivate(kind string) (model, tea.Cmd)` — dispatch a footer button by Kind.

**Context:**
- **`assistedActivate(kind)`** action table:
  - `"assist-run"` → build the ready block's run Button (`Button{Kind:"run", Payload: m.blockCommand(m.readyID), BlockID: m.readyID}`, as `beginRollback` does at `model.go:2327`), set `m.assistedFooter=""` (hide footer while running), and `return m.runOrGate(that)` (env gate + run reused unchanged).
  - `"assist-skip"` → `return m.assistedSkip(), nil` (+ `reflow`).
  - `"assist-rollback"` → `m.assistedFooter=""; m.exitCode=0` (failure resolved by rollback); `return m.beginRollback(m.assistedFailedID)` (the visible chain; the rollback `resultMsg`s won't re-trigger assisted advance because they carry `prevAction=="rollback"`, already excluded in Task 2's hook). After the chain the run is effectively over — set `assistedFooter="done"` when the rollback settles (piggyback on the existing rollback completion, or leave the "done" footer to the next reflow; simplest: set `assistedFooter="done"` immediately after firing rollback).
  - `"assist-leave"` → `m.exitCode` stays 1; `return m, tea.Quit`.
  - `"assist-quit"` → `return m, tea.Quit` (exit code = current `m.exitCode`: 0 unless an unresolved failure set it).
- **Keyboard** (`model.go`, add beside the `if m.confirmResolved` block at `model.go:1512`): `if m.assisted && m.assistedFooter != "" && !m.askMode { switch msg.String() { case "left","h": m.footerFocus = max(0, m.footerFocus-1); case "right","l": m.footerFocus = min(len(buttons)-1, m.footerFocus+1); case "tab": m.footerFocus = (m.footerFocus+1) % len(buttons); case "enter","space"," ": return m.assistedActivate(buttons[m.footerFocus].Kind) } }`. Gate BEFORE the Space-leader/global switch so footer keys are captured only while a footer is active.
- **Ctrl-C exit code:** in the global `q/esc/ctrl+c` handler, when `m.assisted` and the key is `ctrl+c`, set `m.exitCode = 1` before `tea.Quit` (abort = non-zero; `q` stays 0). (See the user convention: Ctrl-C aborts, ESC/`q` is a clean exit.)
- **Click** (`model.go:1241`, the confirm-Kind dispatch): add `if strings.HasPrefix(b.Kind, "assist-") { return m.assistedActivate(b.Kind) }` alongside the `confirm-yes`/`confirm-no` handling.

- [ ] **Step 1: Write the failing tests**

```go
func TestAssisted_RunActivatesReadyBlock(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted()
	m2, _ := m.assistedActivate("assist-run")
	if m2.blockStates["a"].Status != "running" { t.Errorf("Run must mark ready block running; got %q", m2.blockStates["a"].Status) }
	if m2.assistedFooter != "" { t.Error("footer must hide while the step runs") }
}

func TestAssisted_RollbackResolvesFailure(t *testing.T) {
	m := newModel("T", "```bash {id=a rollback=undo-a}\ntrue\n```\n\n```bash {id=undo-a}\ntrue\n```\n\n```bash {id=boom needs=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted()
	m.blockStates["a"] = blockRunState{Status: "ok"}
	m.assistedFooter = "failure"; m.assistedFailedID = "boom"; m.exitCode = 1
	m2, _ := m.assistedActivate("assist-rollback")
	if m2.blockStates["a"].Status != "rolledback" { t.Errorf("rollback must fire (a→rolledback); got %q", m2.blockStates["a"].Status) }
	if m2.exitCode != 0 { t.Errorf("Roll back resolves the failure → exit 0; got %d", m2.exitCode) }
}

func TestAssisted_LeaveAsIsKeepsNonZeroExit(t *testing.T) {
	m := newModel("T", "```bash {id=a}\nfalse\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted()
	m.assistedFooter = "failure"; m.assistedFailedID = "a"; m.exitCode = 1
	m2, _ := m.assistedActivate("assist-leave")
	if m2.exitCode != 1 { t.Errorf("Leave as-is keeps exit 1; got %d", m2.exitCode) }
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/ui/ -run 'Assisted_Run|Assisted_Rollback|Assisted_Leave'` → FAIL.
- [ ] **Step 3: Implement** `assistedActivate` + the keyboard block + the Ctrl-C exit-code + the click dispatch.
- [ ] **Step 4: Run to verify they pass** — the Task-4 tests + `go test ./internal/ui/ -run Assisted` (all assisted) + `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/ui/model.go internal/ui/assisted.go internal/ui/assisted_test.go && git commit -m "feat(ui): assisted footer input — Run/Skip/Quit + failure Roll back/Leave"`

---

### Task 5: Render — ready cursor + `"skipped"` status

**Files:**
- Modify: `internal/ui/render.go` (renderer struct + the tab/number/run-button render + `runRegion` status switch) + `internal/ui/model.go` (thread `readyID`/`assisted` into `Render`) + `internal/ui/render_test.go` (or `assisted_test.go`)

**Interfaces:**
- Consumes: the renderer's existing `nextNumAssigned` anchor (`render.go:122, 682-690`), the run-button `default:` arm (`render.go:764`), the `runRegion` status switch (`render.go:987-1035`).
- Produces: the ready block shows a caret `▶` + a ready-styled Run button; a `"skipped"` block renders `– skipped` (grey, no button).

**Context:**
- **Thread the cursor:** `Render(...)` is called from `reflow()` (`model.go:804`) with variadic `flags ...bool`. Add the assisted cursor via the renderer — simplest: pass `m.readyID` when `m.assisted` through a new render input (extend the `Render` signature or set a field on the `renderer` struct the way `nextNumAssigned` is threaded). Store `r.readyID string` (empty when not assisted). In the run-button `default:` arm (`render.go:764`), when `r.readyID != "" && blk.ID == r.readyID`: prefix a caret `▶` in the tab gutter and render the Run button in the "ready" (focused/highlighted) style (reuse the flash/focused highlight tone used elsewhere, e.g. `colGreen` bg). Non-ready blocks render exactly as today.
- **`"skipped"` status:** add `"skipped"` to the `runRegion` outer switch (`render.go:1001` case list) with an inner label `"– skipped"` in `colSubtext`; and add a guard to the number-color block (`render.go:683`) so a `"skipped"` block's number greys (it already will, since `Status != ""`, but ensure `runRegion` renders the skipped label so the block reads as intentionally skipped, not merely un-run).

- [ ] **Step 1: Write the failing tests**

```go
func TestRender_ReadyCursorMarksNextStep(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n\n```bash {id=b needs=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.assisted = true; m.reflow(); m = m.startAssisted() // readyID=a
	out := strip(m.viewString())
	if !strings.Contains(out, "▶") { t.Errorf("ready step should show a ▶ caret:\n%s", out) }
}

func TestRender_SkippedStatus(t *testing.T) {
	m := newModel("T", "```bash {id=a}\ntrue\n```\n")
	m.width, m.height = 80, 24
	m.blockStates["a"] = blockRunState{Status: "skipped"}
	m.reflow()
	out := strip(m.viewString())
	if !strings.Contains(out, "skipped") { t.Errorf("a skipped block should render 'skipped':\n%s", out) }
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/ui/ -run 'ReadyCursor|SkippedStatus'` → FAIL.
- [ ] **Step 3: Implement** the caret + ready-button render and the `"skipped"` cases.
- [ ] **Step 4: Run to verify they pass** — those tests + `go test ./internal/ui/ -run 'Block|Render'` (existing render tests stay green) + `go build ./...`.
- [ ] **Step 5: Commit** — `git add internal/ui/render.go internal/ui/model.go internal/ui/render_test.go && git commit -m "feat(ui): render assisted ready cursor + skipped status"`

---

### Task 6: Docs — rewrite the assisted section + drop the `--assisted` ⏳

**Files:**
- Modify: `examples/07-run-modes.md`, `docs/guides/tutorial.md`

**Context:** `--assisted` now ships as the FULLSCREEN GUIDED PAGER — but `examples/07-run-modes.md`'s "## Assisted run" section currently documents a line-based `[y/n/q]` stdin flow (a stale mockup). REWRITE that section to describe the shipped reality: `ai-playbook run --assisted --file <path>` opens the interactive viewer in guided mode; a **ready** cursor (▶) points at the next runnable step and the doc auto-scrolls it into view; a footer with focusable **Run / Skip / Quit** buttons confirms each step (←/→/Tab to move focus, Enter/Space or click to select); on a step failure the footer offers **Roll back / Leave as-is / Quit**; the whole document stays scrollable so you can read each step's prose before running it. Remove the `<!-- ⏳ … -->` marker above that section. Keep the `## Auto run` section as shipped (P1). Also update `docs/guides/tutorial.md`: drop the `--assisted` ⏳ from the ch.07 features line (`:164`), the assisted invocation line (`:176`), and the coverage-matrix row (`:268`) — assisted is no longer pending. LEAVE every `validate` ⏳ (ch.08) in place (still unbuilt).

- [ ] **Step 1: Edit** both files per Context (rewrite the assisted prose; remove only the `--assisted` ⏳ markers).
- [ ] **Step 2: Verify** — `grep -n "⏳\|y/n/q" examples/07-run-modes.md docs/guides/tutorial.md`: no `--assisted` ⏳ and no `[y/n/q]` line-based mockup remain; the `validate` ⏳ markers still present; the assisted prose now describes the guided pager (ready cursor, footer buttons, auto-scroll).
- [ ] **Step 3: Commit** — `git add examples/07-run-modes.md docs/guides/tutorial.md && git commit -m "docs(run): document shipped --assisted guided pager (ch.07)"`

---

## Final verification (after all tasks)

- [ ] `cd ~/Projects/langs/go/ai-playbook && gofmt -l internal/ui internal/launcher` → empty; `go build ./... && go vet ./...` clean; `go run github.com/gordonklaus/ineffassign@v0.2.0 ./...` clean.
- [ ] `go test ./internal/launcher/ && go test -race ./internal/ui/` → PASS (the verify-confirm tests + all assisted tests).
- [ ] `go install ./cmd/ai-playbook`, then **live-verify** (this needs a TTY — hand to the user): `ai-playbook run --assisted --file examples/07-run-modes.md` → ready cursor on `build`, footer `[ Run ] [ Skip ] [ Quit ]`; Run each step, watch the cursor advance + auto-scroll to ~1/3; try Skip; force a failure (edit a block to `false`) → failure footer → Roll back / Leave as-is; `q` exits 0, `Ctrl-C` exits non-zero.

## Self-review notes (coverage vs spec §4)

- Spec §4 ready cursor → Task 2 (`readyID`) + Task 5 (render caret). Auto-scroll to ⅓ → Task 2 (`scrollToFraction`). Focusable `[ Run ][ Skip ][ Quit ]` footer reusing the confirm primitive → Task 3 + Task 4. Failure `[ Roll back ][ Leave as-is ][ Quit ]` → Tasks 3-4 (reuses `beginRollback`). Skip + skip-propagation → Task 2 (`assistedSkip` + `NextRunnable`). Env gate once at start → reused UNCHANGED via `runOrGate` on the footer Run (Task 4; no new gate code). Exit codes → Task 1 (`exitCode` + Main capture) + Task 4 (Ctrl-C/Leave). End-of-run summary → Task 3 (`"done"` footer). Docs → Task 6 (rewrite, not just un-⏳).
- Not in scope: `--auto` (shipped, P1); `validate` (ch.08); any change to the default free-form pager beyond the `"skipped"` render case and the assisted-gated cursor.
