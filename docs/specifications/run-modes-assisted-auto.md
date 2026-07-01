# Run modes — `--assisted` & `--auto` (spec)

Status: proposed (2026-07-01). Implements ROADMAP Phase 2 "run modes" and the two ⏳
sections of the tutorial's Chapter 07 (`docs/guides/tutorial.md:160-179`,
`examples/07-run-modes.md`). Adds two non-default ways to run a playbook from the `run`
subcommand; the default fullscreen pager is unchanged.

## Goal

Two new run modes on `ai-playbook run`, mutually exclusive with each other:

- **`--auto`** — inline, headless (no TUI). Runs every runnable block in `needs=` order,
  streams output to stdout, stops at the first failure, rolls back completed steps by
  default, writes a run log + prints a summary, and exits non-zero on any failure or
  interrupt. Built for shell scripts and CI.
- **`--assisted`** — the fullscreen pager, driven as a *guided* walk: a **ready** cursor
  points at the next runnable step, the doc auto-scrolls that step into view, and a
  focusable footer (`[ Run ] [ Skip ] [ Quit ]`) confirms each step. On a failure the
  footer offers `[ Roll back ] [ Leave as-is ] [ Quit ]`. Built for stepping through a
  playbook someone else wrote, with the literate prose fully readable.

Both reuse the existing orchestrator (`orch.Do`) + pty driver + the block/needs model.
Only `--auto` adds a non-bubbletea execution path.

## Background — current state (grounded)

- **Flags.** `internal/launcher/runcmd.go` `resolveRunArgs` (`runcmd.go:220-262`) parses
  only `--playbook`/`--file`/`--auto-rollback` today. `--auto-rollback` (opt-in, default
  `false`) reaches the UI through the package-var seam
  `setAutoRollbackFn = ui.SetAutoRollback` (`runcmd.go:56`, called `:74`); `ui.Main`
  consumes `pendingAutoRollback` once (`internal/ui/main.go:39-43, 448-449`). Peer seams:
  `SetReengage`, `SetProjectRoot`, `SetShell`. `RunMain` (`runcmd.go:62-74`) rewrites
  `os.Args` to a bare `run --file <path>` and calls `uiMainFn()` (the pager).
- **Run flow (pager).** Every forward run is user-triggered: a click (`model.go:1137`) or
  hint key (`model.go:1387`) → `runOrGate(b)` (`confirm_gate.go:101-107`) →
  `markRunning` + `emitAction` (`model.go:402-407`) → `orchCmd` (`inprocess.go:177-209`)
  calls `orch.Do(orchestrator.Action{Kind,ID,Payload})` off the event loop and returns
  `resultMsg{ID,Exit,Logpath}` → the `resultMsg` handler (`model.go:1735-1890`) maps
  exit+action→`Status`. There is **no** sequential/auto-advance driver today.
- **Ordered blocks + deps.** `m.blocks []Block` (rebuilt each `reflow()`,
  `model.go:802`), `needsSatisfied(blk, states)` (`render.go:1181-1190`, a need is met
  when `states[n].Status=="ok"`), `anyRollbackable()` (`model.go:2245-2252`).
- **Rollback.** `beginRollback(failedID)` (`model.go:~2260`) collects `(origin,target)`
  pairs in reverse for every applied (`Status=="ok"`) block with a `Rollback` target,
  marks origins `"rolledback"`, runs the targets, and settles a suffix on the failed
  block. `resetDependents` (`render.go:1217`) clears stale dependent results.
- **Confirm gate.** `beginGate(block)` (`confirm_gate.go:114-135`) is a once-per-session
  env gate (`gateSatisfied`, `confirmEnv`) that exports confirmed vars into the shell then
  runs the deferred block. Already written to be reusable at an assisted-run start
  (comment `confirm_gate.go:112`).
- **Focusable confirm row.** The verify-success wrap-up renders a native
  `[ Yes ] [ No ]` row with keyboard focus: `confirmFocus` (`model.go:290-295`),
  `confirmRowString` (`model.go:2727`), `resolveConfirm`, key handling
  (`model.go:1506-1523`: ←/→/h/l/Tab move focus, Enter/Space select; mouse click also
  resolves). This is the component the assisted footer generalizes.
- **Run log + data dir.** `writeRunLog` (`inprocess.go:437`) writes per-run output to a
  `os.CreateTemp` file today. The durable data dir is
  `AI_PLAYBOOK_DATA_DIR` else `${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook`
  (`internal/cache/cache.go:45-57`).
- **Block model.** `Block{ID,Type,Lang,Needs,Static,File,Rollback,Payload}`
  (`block.go:8-17`); `classifyType` → "shell"/"run"/"diff"/"static"/"create"
  (`block.go:51-63`). `blockRunState.Status ∈ {"","running","ok","failed","stopped",
  "rolledback"}` (`results.go:57-111`).

## 1. Flags & mode plumbing

Add to `resolveRunArgs` (`runcmd.go`):

- `--assisted` (bool), `--auto` (bool) — **mutually exclusive**; both set → usage error.
- `--no-auto-rollback` (bool) — valid **only** with `--auto` (auto-rollback is ON by
  default under `--auto`); set without `--auto` → usage error.
- `--auto-rollback` stays the **default-pager** opt-in; combined with `--auto` or
  `--assisted` → usage error (redundant/meaningless there).

`resolveRunArgs` returns a `runMode` (`enum: default | assisted | auto`) plus the existing
source/rollback fields. In `RunMain`:

- `mode == auto` → **branch before the TUI** to the headless runner (§3); `RunMain`
  returns its exit code directly. Never calls `uiMainFn()`.
- `mode == assisted` → plumb through a new `pendingRunMode`/`ui.SetRunMode` seam
  (mirroring `pendingAutoRollback`, `main.go:39-43, 448-449`), then call `uiMainFn()` as
  today. The pager reads `m.runMode == assisted` and enters guided mode (§4).
- `mode == default` → unchanged.

## 2. Shared core (used by both surfaces)

New package `internal/autorun` holding pure, orchestrator-agnostic helpers so the pager
(§4) and the headless runner (§3) share one definition of "what runs next" and "how to
roll back". No bubbletea, no I/O.

- `func Sequence(blocks []Block, states map[string]blockRunState) []Step` — the ordered
  forward-runnable steps in document order. A block is a step iff it is **not** `Static`,
  **not** a rollback target (never appears as another block's `Rollback`), and its
  primary action is one of run/apply(diff)/create(file). Each `Step` carries
  `{ID, Kind (run|apply|create), Command/Payload}`. Ordering is document order; `needs=`
  is enforced at run time, not by reordering.
- `func NextRunnable(blocks, states) (Step, bool)` — the first `Sequence` step whose
  `Status` is `""` (never run) **and** whose `needs=` are all `"ok"`. `false` when none
  remain. Skips blocks already `ok`/`skipped`; a step whose need was **skipped** is
  itself unrunnable → auto-skipped (see §3/§4).
- `func RollbackPairs(blocks, states) []Pair` — reverse-order `(originID, targetID)` for
  every `Status=="ok"` block with a non-empty `Rollback`. **Extracted from the current
  `beginRollback`** so both surfaces share it; `beginRollback` is refactored to call it.
- `func Summarize(steps []StepResult) string` and `func WriteRunLog(dir, slug string,
  results []StepResult) (path string, err error)` — the run-summary table + the durable
  per-run log (§3). `StepResult{ID, Command, Exit, Status (ran|skipped|failed|
  rolledback|cancelled), OutputPath}`.

A new `blockRunState.Status` value **`"skipped"`** is added (assisted `Skip`, and any
block whose need was skipped) — rendered neutral/grey, counts as "not ok" for `needs=`.

## 3. `--auto` — headless inline runner

`internal/autorun.Run(cfg) int` — no bubbletea. Constructs the orchestrator + driver the
same way the pager does (factor the shared construction out of `ui` so the launcher can
build one headless), then:

1. **Env preflight.** If the playbook declares required env vars (`frontmatter` env-map)
   and any are **unset in the process environment**, print each missing var + its `why`
   and return non-zero **before running anything** (matches `examples/07-run-modes.md`
   `[!TIP]` — auto never prompts).
2. **Loop** `NextRunnable`: print a header line `[<id>] <command>`, run via `orch.Do`,
   stream stdout/stderr inline, record a `StepResult`. On exit 0 → mark `ok`, continue.
   On the **first** non-zero exit → stop the loop (never continue past a failure).
3. **Rollback on failure** (unless `--no-auto-rollback`, or `RollbackPairs` is empty):
   run each pair's target via `orch.Do` in reverse, printing `↺ rolling back <id>`; mark
   origins `rolledback`. With `--no-auto-rollback` (or nothing to roll back): stop and
   leave state as-is.
4. **Interrupt.** A `SIGINT` handler sends `SIGTERM` to the running child (the driver's
   stop path), records the step `cancelled`, prints a cancellation summary, returns
   non-zero.
5. **Finish.** `WriteRunLog` under the data dir at
   `${data}/runs/<UTCstamp>-<slug>.json` (per-step `{id, command, exit, output}`), print
   `Summarize(...)` to stdout, and return: `0` iff every step ran green; otherwise the
   failed step's exit code (non-zero).

Output is plain text (pipeable, `| tee run.log`); the summary names the failed block.

## 4. `--assisted` — guided pager

The existing fullscreen viewer, entered in guided mode when `m.runMode == assisted`. No
modal overlay — the document stays fully readable and scrollable throughout.

**Ready cursor.** A model field `readyID string` names the next runnable step
(`NextRunnable`). The ready block renders with a distinct frame + a gutter caret `▶`, and
its Run button uses a **ready** (focused) style. Only the ready block responds to the
footer's Run — this enforces the sequence while leaving every other block inert.

**Start.** On enter: if project-bound with declared env, run the env gate **once**
(reuse `beginGate`, `gateSatisfied`); then set `readyID = NextRunnable(...)`, auto-scroll
to it (below), and show the footer.

**Footer (generalized focusable row).** Generalize the `confirmFocus`/`confirmRowString`
component (`model.go:290-295, 2727, 1506-1523`) from 2 to **N labelled buttons** with the
same interaction contract (←/→/h/l/Tab move focus, Enter/Space select, mouse click
selects). Two configurations:

- **Step prompt:** `[ Run ] [ Skip ] [ Quit ]` (default focus = Run). It echoes the ready
  step as context: `Step <n>/<total> · <id> · <command>`.
- **Failure prompt:** `[ Roll back ] [ Leave as-is ] [ Quit ]` — shown when the ready
  step's run returned non-zero (default focus = Roll back only if `anyRollbackable()`,
  else Leave as-is).

**Advance / auto-scroll.** When **Run** is selected, run the ready step (existing
`markRunning` + `emitAction`; output streams inline/mux as today). On its `resultMsg`:

- exit 0 → mark `ok`, compute the next `readyID`, and **auto-scroll so that block sits at
  ~1/3 of the viewport height** — a helper `scrollBlockToFraction(id, 1.0/3)` that sets
  the pager offset from the block's first line index and `viewportHeight`, **regardless
  of where the user had scrolled**. Then re-show the step footer. No next step → show a
  final summary and stop.
- exit ≠ 0 → keep the block's ✗, switch the footer to the failure prompt.
  - **Roll back** → drive the visible `beginRollback(readyID)` chain (§2/existing), then
    stop the guided run (never continue past a failure).
  - **Leave as-is** → stop, leave state.
  - **Quit** → exit.

**Skip.** `Skip` marks the ready step `"skipped"`; any later block whose `needs=` includes
a skipped block is itself unrunnable (`NextRunnable` passes over it → effectively
auto-skipped), matching the ch.07 promise. Advance + auto-scroll as above.

**Quit / interrupt.** `Quit` (footer) exits the viewer cleanly. `Ctrl-C` aborts a running
block (existing stop path) and quits. **End-of-run summary** (ran/skipped/failed/
rolled-back) is shown on normal completion and on quit.

**Exit code.** The assisted viewer returns non-zero if any step failed and was not
rolled back, or on `Ctrl-C`; `Quit` with no failures → `0`.

## 5. Env gate behavior (both modes)

- `--auto`: **no prompting** — required vars must be present in the environment; missing →
  error + list (§3.1). Present → run silently (the `[!TIP]`).
- `--assisted`: reuse the interactive `beginGate` **once at start** (the pager already
  has the overlay); after it's satisfied, the guided walk proceeds.

## 6. Docs

- `examples/07-run-modes.md` already documents assisted/auto accurately — **remove the two
  `<!-- ⏳ … -->` markers** (the "Assisted run" and "Auto run" sections).
- Fix the one stale line in that file's **"Manual step-through"** section: it claims the
  default `run --file` "waits for you to press Enter" in the terminal, but the shipped
  default opens the fullscreen pager. Reword to describe the viewer (click **Run**, `q`
  to quit) — the terminal-driven modes are `--assisted`/`--auto`.
- `docs/guides/tutorial.md:164` — drop the ⏳ from the ch.07 features line.
- Reconcile the ROADMAP flag note (`ROADMAP.md:49-65`): document the shipped polarity —
  `--auto-rollback` (default-pager opt-in) **and** `--no-auto-rollback` (`--auto` opt-out).

## Components (decomposition)

- **`internal/autorun`** (new) — `Sequence`/`NextRunnable`/`RollbackPairs`/`Summarize`/
  `WriteRunLog` + the `Step`/`StepResult` types. Pure; unit-tested in isolation.
- **Headless construction seam** — factor the orchestrator+driver build out of `ui` so
  the launcher can construct one without bubbletea.
- **`internal/launcher/runcmd.go`** — `--assisted`/`--auto`/`--no-auto-rollback` flags +
  mutual-exclusion validation; `runMode`; the `auto`→headless branch; `SetRunMode` seam.
- **`internal/ui`** — `blockRunState.Status "skipped"`; `readyID` cursor + ready render;
  `scrollBlockToFraction`; generalized N-button focusable footer; the assisted advance
  state machine; `beginRollback` refactor to call `autorun.RollbackPairs`.
- **Docs** — the ch.07 example + tutorial + ROADMAP edits above.

## Testing

- **`autorun` (pure):** `Sequence` skips static + rollback-targets, keeps doc order;
  `NextRunnable` respects `needs=`, passes over `ok`/`skipped`, and treats a skipped-need
  block as unrunnable; `RollbackPairs` yields reverse `(origin,target)` for `ok` blocks
  with a `Rollback` and nothing otherwise; `Summarize` renders each status; `WriteRunLog`
  writes valid per-step JSON to the data dir.
- **`--auto` (headless):** all-green playbook → exit 0 + summary; a failing middle step →
  stops (later step never runs), rolls back the completed earlier step in reverse, exit =
  the failed exit code; `--no-auto-rollback` → same stop but no rollback; missing required
  env → non-zero before any step runs; the run log exists with the expected step records.
  (Drive `orch.Do` through the in-process fake used by existing orchestrator tests.)
- **`--assisted` (model):** entering assisted sets `readyID` to the first runnable block;
  selecting Run marks it running and emits; on ok, `readyID` advances and the pager offset
  places the next block at ~1/3 height; Skip sets `"skipped"` and skips a dependent;
  a failed step swaps to the failure footer; Roll back drives `beginRollback`; the footer
  row honors ←/→/Tab focus + Enter/Space select + click (reuse the existing confirm-row
  test patterns). Exit-code helper returns non-zero after an unrolled failure.

## Out of scope

- `ai-playbook validate` (tutorial ch.08 ⏳) — a separate follow-up.
- Any change to the **default** pager's free-form behavior (only the `--assisted` guided
  layer and the ch.07 doc wording change).
- Rich interactive env-var **editing** in headless `--auto` (vars come from the
  environment; the fullscreen pager remains the place to customize them).

## Build order

One spec, two implementation plans:

1. **Plan 1 — shared core + `--auto`.** `internal/autorun`, the headless construction
   seam, the flags + mutual-exclusion + `auto`→headless branch, the run log/summary, the
   `"skipped"` status, and the `beginRollback`→`RollbackPairs` refactor. Ships the CI /
   scripting story end-to-end and is testable without the TUI.
2. **Plan 2 — `--assisted`.** The `SetRunMode` seam, the `readyID` cursor + ready render,
   `scrollBlockToFraction`, the generalized focusable footer, and the assisted advance /
   failure / skip / quit state machine — all on top of Plan 1's shared core.
