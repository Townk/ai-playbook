# Diff drift & source edit — focused spec

Status: agreed (2026-06-29). Third and last of the post-Phase-B follow-ups
(viewer-UX-polish ✅ → file-change representation ✅ → **this**). Originally framed as
"file-watching" (ROADMAP Phase 4 / phase-1 store spec); on scoping it resolved into two
concrete needs — **diff drift** and **source edit/reload** — and a literal background
file-watcher turned out NOT to be needed (both act at deterministic moments; the one
exception is the mux source-edit path, which polls a single file). Grounding: `internal/ui`
(blockRunState, the diff/file= Status toggle, the tick/`resultMsg` async patterns, the
FC1 in-process diff view), `internal/orchestrator` (`applyDiff`/`git apply`,
`projectRoot()`/`Drv.Cwd()`), `internal/diff` (FC1 `Parse`), the re-engage/Regenerate
channel.

## Goal

Two viewer affordances that keep a playbook usable as the world around it changes:
- **diff drift** — when a `diff` block's target file has changed since the playbook was
  authored so the patch no longer applies, detect it and hand the user a clear choice
  (resolve manually / ask the model to regenerate) instead of an opaque failed `git apply`;
- **source edit/reload** — edit the playbook's own `.md` source and have the viewer come
  back with the updated contents (mux-aware; works no-mux).

**No background file-watcher** (YAGNI): drift is checked at load + after a regenerate;
no-mux reload is deterministic on editor-exit. The ONLY watch is a single-file mtime poll
on the **mux** source-edit path (the editor runs in another tab, so there is no
editor-exit signal).

---

## W1 — Diff drift (first)

**Detection.** For each `diff` block, check the patch against the current target file via
the driver (the same surface `applyDiff` uses), at load and after a regenerate — run
asynchronously via the existing `orchCmd`→`resultMsg` Cmd pattern so it never blocks
render:
- `git apply --check <patch>` succeeds → **clean** (normal apply button);
- forward fails, `git apply --check --reverse <patch>` succeeds → **already applied**
  (undo button — today's behavior, `Status=="ok"`);
- both fail → **drifted**.

A new `blockRunState` field — `Drifted bool` — carries the verdict (the existing
`Status`/`Action` axis is untouched; drift is a separate axis). The target path for the
check comes from the patch itself (`git apply` reads it); no path threading needed for W1.

**Drifted UI.** When `Drifted`:
- the tab's **apply + view-diff buttons grey out** (rendered dimmed + inert — reuse the
  `shellDisabled`/dim mechanism the buttons already have);
- **below the diff body**, a drift message — *"⚠ the target file changed since this diff
  was written"* — followed by **two tag-buttons** (the tag-button style used elsewhere,
  e.g. re-engage):
  - **[resolve manually]** → opens the **FC1 in-process diff view** of the patch (mux
    float / no-mux overlay, per FC1) so the user sees the intended change and applies it
    themselves — the "propose, don't possess" path;
  - **[regenerate]** → a **model re-engagement scoped to this block**: send the current
    target-file content + the block's original intent (the stale patch) and request a
    fresh diff; on success, replace the block's patch and **re-run the drift check** (it
    should now be clean). Wires onto the existing re-engage/Regenerate channel.

**Deferred (W1 out of scope):** automatic 3-way merge (`git apply --3way` leaving
conflict markers) — "resolve manually" shows the diff and lets the user reconcile;
a real 3-way is a possible later enhancement.

**Decomposition of W1:** **W1a** (drift detection + grey-out + `[resolve manually]`) shipped
first — it reuses existing seams and is low-risk. **W1b** (the `[regenerate]` per-block
re-author) is the heavier chunk and follows, with the mechanism below.

---

## W1b — `[regenerate]` per-block re-author (mechanism, pinned 2026-06-29)

Clicking `[regenerate]` on a drifted `diff` block sends the model the CURRENT target-file
content + the stale patch, gets ONE fresh diff back, splices it into that block in place,
and re-checks drift — WITHOUT the whole-playbook reset (`m.md` wipe + `clear(blockStates)`
+ full re-author) that the existing whole-playbook `Regenerate` does. Grounding resolved
three forks into one coherent mechanism; the whole-playbook regenerate is unchanged.

**Fork 1 — the fresh diff returns as TEXT (not structured capture).** The structured
`submit_playbook` route is whole-playbook AND shares `sess.lastPB` with the commit-metadata
seam — routing one diff through it would corrupt the committed playbook. Instead:
- a new `KindReengageDriftRegen` with `reengageStructured()==false` → the model gets the
  plain tool instruction (no `submit_playbook`) and returns the diff as TEXT;
- a scoped `case` in `buildReengageEvents` builds the prompt from the existing
  `(kind, base, change)` args — **`base` = current target-file content, `change` = stale
  patch** — no `req` mutation; the prompt asks for a fresh unified diff achieving the same
  intent against the current file;
- a new `Orchestrator.DriftRegen(target, patch string) (string, error)` runs `re.Events` →
  `agentstream.FanOut` → drains the reader (`fan.Body()`) → returns the one diff string.
  No `lastPB`, no structured mode, no shared-pipeline touch.

**Fork 2 — the `m.md` single-block splice by `{id=X}` tag.** A new helper finds the
opening fence carrying `{id=X`, finds its closing ```` ``` ````, replaces the body lines
between (keeping BOTH fence lines so tag attributes survive), leaves the rest of `m.md`
intact, then `reflow()`. Low-risk: ids are unique per block, diff bodies contain no fences,
and `normalizeFences` already canonicalizes. (~15-line string op; no existing helper.)

**Fork 3 — per-block UX: an isolated async Cmd→msg, no pane reset.** A second drift
tag-button `[regenerate]` (`Kind:"drift-regen"`, alongside `drift-resolve`); on click set a
new `blockRunState.Status == "regenerating"` (spinner on JUST that block — new `case` in
`runRegion` + the `spinTick` predicate), and fire a new async Cmd (mirroring W1a's
`driftCheckCmds`) → `driftRegenMsg{ID, newPatch string, err error}`. The handler splices
`m.md` (Fork 2), `reflow()`s, clears the regenerating status, and re-fires `CheckDrift` for
that one block. It MUST NOT touch the shared streaming pipeline
(`m.reader`/`structured`/`bodyProvider`/`streaming`/`thinking`) — it behaves like the drift
check, NOT like `beginRegenerate`.

**Behavioral decisions.**
- **Success** (fresh diff applies clean): splice → re-check → drift clears → the normal
  apply button returns; the user applies it as usual.
- **Failure or still-drifted** (model errored, or the fresh diff also doesn't apply): the
  block STAYS drifted, the drift region remains, with a brief *"regenerate didn't resolve
  it — resolve manually"* note. No destructive fallback.
- **Persistence:** the regenerated diff updates the viewer's in-memory `m.md` (session-local);
  it persists to the stored playbook only if the user saves (the existing save flow / W2).
  W1b does NOT change save behavior.

**W1b risks:** the `m.md` splice string op (mitigated by unique ids + fence-free diff
bodies + `normalizeFences`); strict isolation from the shared streaming pipeline (the new
Cmd must drain its OWN private reader, never `m.reader`); the scoped prompt must reliably
yield a unified diff (text path, `Structured:false`).

---

## W2 — Source edit + reload (second)

**Prerequisite.** Thread the playbook's **on-disk source path** into the viewer model
(the ROADMAP-noted missing piece). Only **file-backed** playbooks (store / committed)
have one; the edit affordance is gated to those.

**The [edit] tag-button** — mux-aware, mirroring FC1's view-diff presentation:
- **no-mux (default):** `tea.ExecProcess(exec.Command($EDITOR, <source>), cb)` — suspends
  the TUI, runs the editor on the real terminal, resumes; `cb` **reloads** the source
  (re-parse the `.md`, rebuild the blocks) and re-renders. Deterministic; no watcher.
- **mux enabled:** spawn `$EDITOR <source>` in a **mux tab** (via the mux integration).
  The viewer keeps running, so it **polls the source file's mtime** on a slow tick (a
  dedicated `tea.Tick` loop reusing the existing tick mechanism — one `stat`/tick, no new
  dependency) and **reloads on change**. This single-file, mux-only poll is the sole
  surviving "watch."

**$EDITOR resolution:** `$VISUAL` → `$EDITOR` → a sensible fallback (`vi`).

**Reload semantics:** re-parse the source into a fresh playbook/blocks, preserving the
viewer's transient per-block state where the block still exists (best-effort by block ID);
a changed block's drift/Status is re-derived.

---

## Mechanism notes

- **No new dependency.** `git apply --check` is the existing git surface; `tea.ExecProcess`
  is built into bubbletea; the mux poll reuses `tea.Tick`. `fsnotify` is intentionally
  NOT added (the poll is sufficient and platform-portable; the ROADMAP left fsnotify-vs-poll
  open — we choose poll).
- **Async drift check** reuses the `orchCmd` goroutine→`resultMsg` idiom (there is no
  retained `tea.Program.Send` handle; background work returns a `tea.Msg` from a `tea.Cmd`).
- **Project root / target resolution** for the regenerate path anchors to
  `orchestrator.projectRoot()` / `Drv.Cwd()` (where `applyDiff` writes), not the heuristic
  `model.projectRoot`.

## Integration risks (carry into the plans)

1. Drift detection adds N driver calls at load (N = diff blocks) — run async (don't block
   render); playbooks have few diff blocks, but keep it off the render path.
2. `blockRunState.Drifted` is a NEW axis orthogonal to `Status`/`Action` — the render must
   combine them correctly (drifted overrides the apply/view-diff buttons; an already-applied
   block, reverse-check-ok, is NOT drifted).
3. The drift tag-buttons render in a NEW location (below the diff body, not in the tab) —
   needs button hit-testing for that region (mirror how re-engage tag-buttons are placed).
4. W1 **[regenerate]** scoped to one block is the heaviest piece — the existing Regenerate
   is whole-playbook; a per-block regenerate against the current file is new wiring on the
   re-engage channel.
5. W2 source-path threading: the path exists only for file-backed playbooks — the [edit]
   button must be absent (not just disabled) for ephemeral ones.
6. The mux source poll must stop / not leak when the viewer exits; mtime granularity (1s
   on some FSes) may delay reload slightly — acceptable.
7. Reload must not clobber in-flight block actions (best-effort state carry by block ID).

## Testing

- **W1:** the drift classifier (clean / already-applied / drifted) from `git apply --check`
  forward+reverse results; `blockRunState.Drifted` set by the async check; the drifted
  render greys apply+view-diff and emits the message + two tag-buttons; [resolve manually]
  opens the FC1 diff view; [regenerate] triggers the scoped re-engagement and the
  post-regen re-check clears drift on a now-applying patch.
- **W2:** the source path threads through for a file-backed playbook (absent for ephemeral);
  no-mux [edit] runs `tea.ExecProcess` and reloads on return; mux [edit] spawns the editor
  in a tab + the mtime poll reloads on change; `$EDITOR` resolution order; reload re-parses
  and re-renders, carrying transient state by block ID.

## Decomposition & order

**W1 (diff drift) first** — the user's primary concern and a real correctness gap (opaque
failed applies today). **W2 (source edit/reload) second**. Each gets its own writing-plans
+ subagent-driven execution.

## Out of scope

A general background file-watcher / project-tree watch; reflecting arbitrary external file
changes onto blocks beyond the diff-drift check; automatic 3-way merge resolution;
the assisted-run feature (ROADMAP Phase 2).
