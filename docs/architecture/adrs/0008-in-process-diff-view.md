# In-process side-by-side diff view; drop the external diff tool

- **Status:** Accepted
- **Date:** 2026-06-27

## Context and Problem Statement

ai-playbook has two diff surfaces:

1. **Adapt-on-run** original→adapted overlay (the `d` keybind) — already pure-Go,
   in-process (`internal/ui/adapt.go`: `unifiedDiffLines`/`buildDiffLines`/`diffView`).
2. **The playbook `diff`-type block "view diff"** — shells out to an external tool
   (`hunk` → `delta` → `less`) spawned in a multiplexer floating pane
   (`internal/orchestrator/viewDiff`/`diffViewerCmd`/`hunkBin`/`lookViewer`).

Two problems with surface 2:

- **Dead by default.** `viewDiff` only fires when a mux is present, and the mux is off
  by default (ADR-0007). Off-mux, "view diff" silently no-ops. It also requires the
  user to install `hunk`/`delta`.
- **The only thing the external tool gave us that an inline render couldn't was a
  SIDE-BY-SIDE view** — that is the entire reason to pop a diff into its own pane.

A richer **"review diff"** was also considered: the model annotates each change with a
rationale, and the user comments back to drive a revision — a model↔user loop over one
diff. We judged this **not worth its complexity**; read-only "view diff" covers the real
need (see the change clearly before applying) with none of the annotation-schema +
interactive-comment + revision-round-trip cost.

## Decision Drivers

- The diff view must work in the **default (no-mux)** config — not be dead by default.
- **No external dependency** to install; no fragile external-pager terminal handoff.
- **Side-by-side is the point** of a rich diff view, so it is a requirement.
- Consistency with the progressive-enhancement pattern (ADR-0006): the mux is an
  *enhancement* to presentation, never a requirement — mirroring the input float vs
  inline input and the ask float vs ask overlay.

## Decision Outcome

Build **one in-process, pure-Go diff renderer** and present it **mux-aware**.

- **Renderer (shared):** side-by-side + syntax-highlighted, modeled on
  `charmbracelet/crush`'s `internal/ui/diffview` (our exact stack: bubbletea v2 +
  lipgloss + chroma). Hunks via `go-udiff` (already transitively in the dep tree),
  highlighting via chroma. **Side-by-side is a REQUIREMENT**, with a **unified fallback
  for narrow terminals** (too few columns for two panes).
- **Mux-aware presentation:**
  - **mux ON → a floating pane** shows the side-by-side diff — rendering *our* Go
    diff (e.g. via an `ai-playbook diff` entrypoint spawned in the float), **not** an
    external tool.
  - **mux OFF → a modal overlay** in the viewer (the existing full-screen overlay
    mechanism the adapt `d` overlay uses).
- **Shared by both dedicated-view surfaces:** the diff-block "view diff" button AND the
  adapt-on-run `d` (original→adapted) overlay use this renderer.
- **Unified diff** is used **only** for the inline diff-block rendering in the viewer
  body (compact, in-flow) — never as the dedicated view.
- **Drop:** the external tool chain (`hunk`/`delta`/`less`), `diffViewerCmd`/`hunkBin`/
  `lookViewer`, the `AI_PLAYBOOK_HUNK_BIN` env var, and **"review diff"** (annotate +
  comment loop).
- **Unchanged:** apply/undo (pure-Go `git apply [--reverse]`).
- **Deferred (optional polish):** word-level intra-line highlighting (delta's
  syntect+Levenshtein) — the one capability we don't get for free; not required.

### Positive Consequences

- Diff view works everywhere, including the default no-mux config.
- One renderer, two presentations; the mux is a presentation enhancement, not a gate.
- Removes an external dependency and the dead-by-default external-pager handoff.
- In-process is faster for the no-mux path (no subprocess/pane spawn); diff sizes here
  (a block's patch, or an original→adapted of a few hundred lines) render in ms.

### Negative Consequences

- We **build** (copying the crush pattern), not import, the side-by-side renderer —
  moderate effort.
- We lose word-level intra-line highlighting vs delta until/unless added (deferred).
- **Behavior change:** `AI_PLAYBOOK_HUNK_BIN` and the external-viewer fallback are
  removed → recorded in the CHANGELOG.

## Pros and Cons of the Options

### Option 1 — in-process, mux-aware presentation (chosen)
- **Good:** works by default; no external dep; side-by-side proven in-stack; consistent
  with the float/overlay progressive-enhancement pattern.
- **Bad:** build the renderer; intra-line highlight deferred.

### Option 2 — keep the external tool, make it pluggable (the "diff driver" path)
- **Good:** reuses users' existing `delta`/`hunk` setups; intra-line for free.
- **Bad:** keeps the terminal-handoff fragility that made view-diff dead by default;
  an external dep; a diff-driver + terminal-handoff + 2-tier-config worth of machinery.
  Rejected.

### Option 3 — drop diff view entirely
- **Good:** smallest change.
- **Bad:** loses the review-before-apply surface (the inline colored block body is not
  a substitute for a side-by-side view). Rejected.
