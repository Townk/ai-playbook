# UX review — triage & fix plan

Source: hands-on review of `examples/` ch.01–06 (2026-06-30). Full findings in the session
punch-list. 25 findings + 1 architectural root cause, grouped into 5 execution waves.

Severity: **P0** blocks review/correctness · **P1** broken behavior · **P2** polish.

---

## Wave 1 — ARCH keystone (P0): front-matter round-trip

Root cause of **F4** (run cwd = /tmp) and **F25** (env gate never fires). `runFile`
round-trips the front-matter-stripped body to /tmp, so cwd context and the `env:` map are
lost before `ui.Main` sees them.

- **Fix:** for `run --file`, hand the ORIGINAL file to `ui.Main` (its `loadPlaybookSource`
  already strips FM for display AND extracts the env map) + pass `--cwd` (project_bound→root,
  else dir-of-file) + `setProjectRootFn` for project_bound. Replaces the F4 band-aid.
- **Also:** verify `project_root` resolves from `fm.project_root`, NOT the git-toplevel
  heuristic (`capture.ProjectRoot()`).
- **Unblocks:** ch.06 env Confirm/Customize dialog review (the one unseen surface),
  PROJECT_ROOT value, seed.txt location.
- Note: `run --playbook` (store slug) env gate is a separate path (env lives in store Meta,
  not body) — track but out of scope for ch.06 unblock.
- **Folded in (surfaced during Wave 1 verify):**
  - **F27** confirm gate ignored the declared env `value:` (showed empty). Fixed:
    `buildConfirmVars` defaults to `ev.Value`, live shell env overrides, PROJECT_ROOT→root.
  - **F28** gate exported derived values literally (`DATA_DIR='$PROJECT_ROOT/data'` → a dir
    literally named `$PROJECT_ROOT`). Fixed: `buildExportCmd` → `expandConfirmedVars` expands
    `$NAME`/`${NAME}` confirmed-var refs (fixed-point), leaves literal `$` alone.
  - **F26** Confirm/Customize dialog bg leak → deferred to Wave 5 (with F12).

## Wave 2 — run-state model (P1): F7 · F11 · F18 (+ F10)

One coherent model for block run-state:
- **F7** a block whose action ran shows a "done" state; its action greys/disables.
- **F18** undoing a diff/file (or rollback) RESETS dependent blocks' run-state — clears stale
  `✓ ran` results/expanded output, re-enables actions.
- **F11** `## Verify` reflects upstream failure (blocked/warned, not blithely runnable).
- **F10** remove the authoring-only "try a different solution" button from the run context.

## Wave 3 — rollback & failure UX (P1): F6 · F8 · F9

- **F6** rollback default = MANUAL. Failed step with a rollback shows a "Rollback" button;
  auto-rollback only behind an explicit flag. Remove auto-fire.
- **F8** rollback execution is visible (which rollback ran + output/status).
- **F9** the "view full log" path points at a real, existing log file.

## Wave 4 — drift resolve/regenerate (P1): F19–F24

Reads as unfinished today.
- **F21** build real manual-resolve (open target in `$EDITOR`, no-mux default per W2); keep
  "view diff" as the separate read-only affordance. Rewrite ch.05 prose to match.
- **F22** drift buttons (drift-resolve/drift-regen) mouse-hittable.
- **F23** regenerate surfaces a spinner + a result/error (no silent no-op).
- **F24** AI-dependent actions surface a clear error when no backend is reachable.
- **F19** no hints on disabled buttons.
- **F20** hint-label placement prefers surrounding empty space over overlapping adjacent text.

## Wave 5 — diff dialog + chrome/docs (P2): F12–F17 · F1–F3 · F5

- Diff dialog (FC1 `internal/diff` render + overlay): **F12** paint full dialog bg · **F13**
  diff row tint over syntax highlight · **F14** split header across panes + full-height
  divider · **F15** width = viewer − 4 cols · **F16** height near-full, 1-line pad · **F17**
  post-apply "Diff applied successfully" (not "log unavailable").
- Chrome/docs: **F1** prose references buttons by NAME, not embedded glyphs (font-robust) ·
  **F2** callouts framed/greyed in hint mode like code blocks · **F3** Play button only when
  mux + docked side panel · **F5** reconcile §1 mux/Play prose with F3.

## Decisions needed (my recommendations — confirm or override)

1. **Manual-resolve (F21):** open the file in `$EDITOR` and re-check drift on save.
   Conflict-markers is a stretch — recommend plain open + the intended change shown nearby.
2. **Rollback flag (F6):** `--auto-rollback` opt-in; button label "Rollback"; whole-playbook
   scope (reverse-order undo of applied steps).
3. **Backend UX (F24):** clear error notice now; DEFER the interactive model picker (YAGNI).
4. **Glyphs (F1):** rewrite prose to name buttons, drop embedded glyphs.

## Clean

ch.03 (`file=` create/undo) — flawless, no changes.
