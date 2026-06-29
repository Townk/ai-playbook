# Structured Playbook — Phase B2b: pre-run variable confirmation

Status: agreed (2026-06-29). Sub-spec of `structured-playbook-output.md` → Phase B → B2,
split into B2a (deterministic portability, shipped) + B2b (this). Builds on B2a
(`store.Meta.Env`, `PROJECT_ROOT` injection) and B1 (the viewer's dual-surface ask
dispatch).

## Goal

Before a stored playbook's blocks can run, confirm its declared variables with the
user — show each variable's current value, let the user accept them or customize, then
export the final values into the run driver so the blocks resolve them. This closes the
loop on B2a portability: B2a makes a relocated playbook *reference* `$PROJECT_ROOT`/env
vars; B2b lets the user *verify and override* those values for the current run.

## Trigger — a reusable gate before the first execution

The confirmation is a **gate that runs once before the first block executes**, NOT a step
tied to the `run` command. Today's trigger is the user's **first block-run action** in
the viewer (the first `enter`/`space`/`run ▶` on a runnable block): the gate fires once,
then every later block runs without re-asking. This keeps the viewer **read-first** — the
playbook renders in full, the user reviews and scrolls freely, and nothing is confirmed
or executed until they choose to run a block. (The current manual pager already lets the
user review freely; the gate simply sits in front of the first execution.)

The gate is built to be **reusable**. The assisted-run feature (Phase 2 run modes, per
`docs/ROADMAP.md` — scroll-to-next-step + confirm-each + log; not yet built) will trigger
the SAME gate at its "start assisted run" action. B2b builds the gate and wires only
today's trigger (the first block-run); assisted-run reuses it unchanged.

The variable set is exactly the front-matter `env`:
- a `project_bound` playbook → `PROJECT_ROOT` + any declared `meta.env` vars;
- a general playbook that declared `meta.env` vars → just those;
- a playbook with no declared vars → `env` empty → **no gate** (`N == 0`); the block runs
  directly (the path already fully resolved today).

Fires for ANY declared-var playbook, not only `project_bound`.

## UX flow

1. **Start the viewer** (the existing `run --file` path).
2. **Render the playbook** — drawn in full; the user reviews and scrolls **freely**.
   Nothing executes.
3. The user triggers the **first block run** (`enter`/`space`/`run ▶` on a runnable
   block) — the explicit kick-off.
4. If the front-matter `env` is non-empty and the gate has not yet run this session,
   **raise the confirm dialogs OVER the rendered playbook** (the user sees what they are
   about to run behind the dialog). Otherwise the block runs immediately.
5. On confirmation completion, **export the final values** into the run driver, **mark
   the gate satisfied**, then run the requested block. Every later block runs without
   re-confirming.

**ESC at any confirmation dialog cancels the gate and returns to reading** — the viewer
stays open, the requested block does NOT run, no values are exported; the user can review
more, retry a run, or quit normally (`q`). Consistent three-way: Confirm = proceed ·
Customize = edit · ESC = cancel back to reading. (`Ctrl+C` aborts the app as usual.)

## Surface — the in-viewer overlay (both mux and no-mux)

The confirmation renders through the viewer's **ask overlay** (`input.NewAsk`,
composited over the rendered playbook) for **both** mux and no-mux. The viewer's mux ask
path is a **text-only float** (the `AskFunc` used by `r`/refine) — it cannot render a
confirm/choose dialog — so B2b does NOT use the float; it drives the overlay directly via
the model's ask machinery (`m.ask`/`askMode`/`askCompletion`). The overlay composites
over the viewer's own content, so it works identically whether the viewer is fullscreen
(no-mux) or in a mux pane. One surface, both modes. (Consequence: only the overlay's
`NewAsk` needs the Confirm/Customize labels — no `floatinput` change.)

## Data flow

The variable list is built in the viewer from the parsed front-matter `env` (names +
`why`) + the project root + the live shell, but two of those are currently **discarded
before reaching the model** and must be threaded in:
- the `run --file` path parses the front matter in `loadPlaybookDocument` but keeps only
  the title/subtitle — `fm.Env` is dropped;
- B2a's `pendingProjectRoot` is consumed into `driver.Options.Env` at driver-open and
  then cleared — the model never retains it.

So B2b adds a small seam: capture `fm.Env` and the project root into the `model` at load
time. With those in hand the viewer builds the list itself:
- **name** + **why** ← front-matter `env`.
- **value**: for `PROJECT_ROOT`, the heuristic root (`pendingProjectRoot`); for every
  other var, its **live shell value** (`os.Getenv(name)`, empty string if unset). The
  stored front-matter `value` is NOT used for display — the user's live environment is
  the source of truth, which is the whole point of confirming.

## Grouping

Balanced dialogs of at most 5 variables:
- number of dialogs = `ceil(N / 5)`;
- per-dialog size = `ceil(N / ceil(N / 5))` (balanced, always ≤ 5 — e.g. 6 → [3,3],
  12 → [4,4,4], 13 → [5,5,3]; never a lonely last group);
- guard `N == 0` (no dialogs; the trigger already excludes this).

## Per-group flow

For each group, a **confirm dialog** (the existing `confirmField` 2-button widget,
labelled **Confirm** / **Customize**) whose prompt lists the group's `name = value`
lines:
- **Confirm** → accept the shown values for this group, advance to the next group.
- **Customize** → a pre-filled **line** input for each variable in the group (prompt
  `NAME — why`, value pre-filled with the shown value); the edited values replace the
  shown ones; then advance to the next group. No re-confirm loop after editing.
- **ESC** → cancel the gate and return to reading (viewer stays open; the requested
  block does NOT run; nothing is exported; the gate stays unsatisfied so a later run
  re-prompts).

The confirm widget already supports custom button labels at the field level
(`newConfirmField(affirmative, negative, …)`); B2b surfaces "Confirm"/"Customize" by
threading those labels through the `NewAsk` confirm constructor (a small pass-through —
no new widget).

## Export

After the last group, the final values (live-or-customized, for every variable) are
exported into the run driver's MAIN shell context as a single
`export NAME=value; …` before any block can execute (the same mechanism B2a uses to set
`PROJECT_ROOT`). Exporting all final values — not only the customized ones — keeps the
driver authoritative regardless of inherited-env edge cases. Values are shell-quoted.

## Components (decomposition)

- **Grouping helper** — pure function, balanced ≤ 5 (+ `N == 0` guard). Unit-testable.
- **Variable-list builder** — front-matter `env` + `pendingProjectRoot` + `os.Getenv`
  → `[]{name, value, why}`. Unit-testable.
- **Reusable confirmation gate** — a viewer-level capability invoked before the first
  block executes: builds the var list, groups it, runs the dialog sequence through the
  existing ask dispatch, and on completion exports the values + marks the gate satisfied
  + proceeds with the requested block; on ESC returns to reading (unsatisfied). It tracks
  a once-per-session "satisfied" flag so subsequent block runs skip it. The trigger
  (first block-run today; the assisted-run start later) is the caller's concern, kept
  separate from the gate so Phase-2 assisted-run can invoke the same gate. The bulk of
  the work.
- **First-block-run trigger** — intercept the first block-run action; if the gate is
  unsatisfied and `env` is non-empty, invoke the gate (deferring the actual block run
  until it completes); else run the block directly.
- **Driver export** — shell-quoted `export …` of the final values before the block runs.
- **`NewAsk` confirm-label pass-through** — surface "Confirm"/"Customize" labels.

## Testing

- **Grouping helper:** N = 1, 5, 6, 12, 13 (balanced sizes, all ≤ 5) + the `N == 0`
  guard.
- **Variable-list builder:** `PROJECT_ROOT` → the injected root; other vars → live shell
  value (set + unset cases); `why` carried through.
- **Confirmation gate (viewer):** the first block-run on an `env`-bearing playbook raises
  the dialogs (the block does not run yet); Confirm leaves values unchanged; Customize
  applies the edited values; multi-group sequencing advances correctly; on completion the
  requested block runs and the gate is marked satisfied; a **second** block-run does NOT
  re-prompt; **ESC** returns to reading with no export, the viewer still open, the block
  not run, and the gate still unsatisfied (a later run re-prompts); an `env`-empty
  playbook never prompts.
- **Export:** the driver receives the final (confirmed + customized) values, shell-quoted,
  before the gated block runs.

## Out of scope

B3 (re-engagement → structured + collapse finalize); the viewer-UX-polish backlog; the
file-change-representation (`file=`/diff) spec. Re-engagement variable confirmation, if
ever wanted, is not part of B2b.
