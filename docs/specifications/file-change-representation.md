# File-change representation — focused spec

Status: agreed (2026-06-29). Second of three post-Phase-B follow-ups
(viewer-UX-polish ✅ → file-change representation → file-watching). Paired with
**ADR-0008** (in-process diff view). Designed as one feature; implemented in two chunks
(**FC1** diff view, then **FC2** `file=` create block). Grounding: `internal/ui`
(block.go/render.go/button.go/results.go), `internal/orchestrator`, `internal/playbook`,
`internal/author`.

## Goal

Give a playbook three first-class block roles for touching the filesystem, with a
consistent review-then-apply/undo surface:
- **run a command** (exists, unchanged);
- **edit an existing file** — a `diff` block (inline unified render + apply/undo + an
  in-process side-by-side "view diff");
- **create a new file** — a `file=<path>` block (body IS the content, deterministic
  write, a `create`→`undo` tab).

## Shared surface (the foundation)

The apply/undo toggle is driven entirely by `blockRunState.Status` (`results.go`):
`"ok"` ⇒ the button shows **undo**, else **apply/create**; the `resultMsg` state machine
(`model.go`) flips it on a clean Exit 0. Both the diff block's apply/undo and the `file=`
block's `create`/`undo` ride this same machinery — a new `code()` button branch + new
action kinds reuse it verbatim. Actions dispatch as `orchestrator.Action{Kind, ID,
Payload}` → `orch.Do`. Block writes follow the existing pure-Go precedent
(`os.WriteFile` in `CommitPlaybook`), anchored to the **driver's cwd / project root**
(not the process cwd).

---

## FC1 — In-process side-by-side diff view (ADR-0008)

**What exists:** the `diff` block already parses (`lang:"diff"`/`patch` → `classifyType`
"diff"), renders inline (unified, colored), and applies/undoes (`writePatch` pure-Go +
`git apply [--reverse]` via the driver). The gap is **"view diff"**: today
`orch.viewDiff` shells to `hunk`→`delta`→`less` in a mux float and **silently no-ops
off-mux** (the mux is off by default, ADR-0007).

**Build (per ADR-0008):** one pure-Go side-by-side diff renderer, modeled on
`charmbracelet/crush`'s `diffview` (our stack: bubbletea v2 + lipgloss + chroma; hunks
via `go-udiff`, already in deps). **Side-by-side is the requirement**, with a **unified
fallback for narrow terminals** (too few columns for two panes).

**Present mux-aware:**
- **mux ON** → a floating pane renders *our* Go diff (via an `ai-playbook diff`
  entrypoint spawned in the float), not an external tool.
- **mux OFF** → a **modal overlay** in the viewer (the existing `ask_overlay`/full-screen
  overlay mechanism).

Shared by the diff-block "view diff" button (`KindViewDiff` stays; only `viewDiff`'s body
is replaced).

**Drop:** the external chain — `viewDiff`'s spawn body, `diffViewerCmd`, `hunkBin`,
`lookViewer`, and the **`AI_PLAYBOOK_HUNK_BIN`** env var (record in CHANGELOG).
`writePatch` stays (apply/undo uses it).

**Amend ADR-0008:** its second shared surface — the adapt-on-run `d` overlay
(`unifiedDiffLines`/`buildDiffLines`/`diffView`) — was **deleted in B2a's dead-code
sweep**. Update the ADR to note the adapt surface is gone; the renderer is greenfield and
serves only the diff-block "view diff."

**Deferred (per ADR):** word-level intra-line highlighting — not required.

---

## FC2 — `file=<path>` create block

A code block tagged `file=<path>` whose body IS the file's content (deterministic write,
no heredoc / hunk math).

**Schema + render + parse.**
- `internal/playbook` `ContentItem` gains `File string` (`json:"file,omitempty"`); a
  `file=` block is `{kind:"code", lang, file:<relative path>, code:<content>}`.
- `playbook.Render` `fence()` emits `{id=… file=<path>}` (or with `static`); the viewer's
  `parseFenceInfo` ALREADY parses `file=` into `attrs["file"]`.
- Recognition: `code()` (`render.go`) reads `attrs["file"]` after `classifyType`; when set,
  the block is a create block — set `Block.File` (new field) + a create type. (Branch in
  `code()` rather than changing `classifyType`'s signature, to keep the existing
  diff/shell/run/static matrix + its tests intact.)

**The tab.** Mirror the diff-block tab, but: the block-type **icon** as normal, the type
**label replaced by the relative path** (`Block.File`), then the action separator and a
single **`create`** button. On a clean create the toggle flips to **`undo`** — reusing the
`Status=="ok"` ⇒ undo render the diff block uses. New glyph const for `create`.

**Action + write.**
- New kinds `KindCreateFile` / `KindUndoCreate` (orchestrator `Kind` enum + `String()` +
  `Do` dispatch); UI `kindOf` + `isShellActionKind` + BOTH click-dispatch blocks (mouse +
  keyboard hint) + the `orchCmd` result `case` updated (a missed site = a silently inert
  button).
- `Action` has no path field, so create actions carry `{path, body}` (and, for undo, the
  captured backup) as **JSON in `Payload`**, decoded by the orchestrator.
- `createFile` writes **pure-Go** (`os.WriteFile`, dir `MkdirAll` as needed) anchored to
  the driver cwd / project root; returns `driver.Result{Exit:0}` so the `resultMsg`
  toggle works unchanged. **Run-time drift:** if the path already exists, capture its
  prior content as a backup before overwriting; **undo** restores the backup, or deletes
  the file if it was newly created.

**Authoring-time validation (the create-vs-edit gate).** When the model calls
`submit_playbook`, the handler (which has the session's project root) checks each `file=`
block's path against the project filesystem: **if the path already exists, reject the
submission** with a message — "path `<x>` already exists; use a `diff` block to edit an
existing file (`file=` is for NEW files)." This pushes the model to pick the right role.
(Environmental check — lives in the `submit_playbook` handler, not the pure
`playbook.Validate`.) The run-time backup/restore above is the safety net for drift.

**Prompt vocabulary.** `StructuredToolInstruction` (and the markdown `ToolInstruction`)
gain: a `diff` block (use `lang:"diff"` with a unified patch to EDIT an existing file) and
a `file=` block (set `file:<relative path>`; the body is the new file's full content; use
ONLY for files that don't exist yet — edit existing files with a `diff` block).

---

## Integration risks (carry into the plans)

1. `Action`/`Block` have no path field → smuggle `{path, body}` via JSON `Payload`; add
   `Block.File`.
2. `classifyType` only sees `lang, static` — recognize `file=` by branching in `code()`
   after classification, not by changing `classifyType`.
3. The tab fill-width math is hand-computed (`render.go`) — a new `create`/`undo` button
   must add its `regionW += 2` reservation exactly, or the `▂` fill + all button hit-boxes
   shift. Mirror the diff button cluster precisely.
4. Create undo is NOT symmetric like `git apply --reverse` — needs the captured backup
   (restore vs delete).
5. Write path resolution: anchor to the driver cwd / project root, not the process cwd.
6. ADR-0008's referenced shared renderer (`adapt.go`) does not exist — FC1 is greenfield.
7. New action kinds must be added in ALL sites: orchestrator enum/`String`/`Do`, UI
   `kindOf`, `isShellActionKind`, both click-dispatch blocks, the `orchCmd` result case.

## Testing

- **FC1:** the side-by-side renderer (a known patch → two-pane output; narrow-terminal →
  unified fallback); "view diff" routes to the in-process renderer (float on / overlay
  off) — the external chain has no callers; the dropped env var is gone.
- **FC2:** `file=` round-trips (schema `File` → `fence` `{file=}` → `parseFenceInfo` →
  `Block.File`); `code()` recognizes it as a create block; the tab shows the path + a
  `create` button that toggles to `undo` on Exit 0; `createFile` writes the content to the
  cwd-anchored path; undo deletes a new file and restores a backed-up one; the
  `submit_playbook` handler rejects a `file=` on an existing path (with the diff
  suggestion); the prompt names both block roles.

## Decomposition & order

**FC1 (in-process diff view) first** — the architectural foundation; makes the existing
edit-file diff block fully work everywhere (incl. off-mux) and removes the external dep.
**FC2 (`file=` create block) second** — reuses FC1's shared apply/undo/toggle surface.
Each gets its own writing-plans + subagent-driven execution.

## Out of scope

File-watching (follow-up #3 — the viewer watching files for external changes); the
assisted-run feature (ROADMAP Phase 2); "review diff" (annotate+comment loop — rejected
in ADR-0008).
