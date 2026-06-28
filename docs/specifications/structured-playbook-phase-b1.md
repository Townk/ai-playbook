# Structured Playbook — Phase B1: escalate → structured + reusable ProgressWidget

Status: agreed (2026-06-28). Sub-spec of `structured-playbook-output.md` (Phase B,
decomposed into B1 → B2 → B3). This is B1.

## Goal

Migrate the **escalate** path (assist → triage → troubleshoot → author) from
free-markdown authoring to the **structured `submit_playbook`** flow Phase A built
for `create`, and extract the authoring-progress render into one **reusable
`ProgressWidget`** shared by the inline and in-viewer hosts.

## Context (current state)

- **Escalate** (`internal/launcher/session.go` `authorPlaybook`): calls
  `author.AuthorEvents(req, {Structured:false})` (→ the markdown `ToolInstruction`),
  fans the events into a reader + activity, and calls `ui.RunStream(reader, opts)`,
  which **streams the authored markdown live into the pager** while showing a
  "thinking" block (spinner + phrase + elapsed + activity). The body is cached from
  the stream text after the viewer returns.
- **Create** (Phase A): `Structured:true` (→ `StructuredToolInstruction`), the agent
  calls `submit_playbook`, captured via the session's `Deps.OnPlaybook` into
  `sess.lastPB` (atomic), rendered with `playbook.Render`, shown in the `finalDraft`
  viewer; progress is the **inline** host (`runCreateProgress` → `WaitingLine` on
  `/dev/tty`). Re-engagement stays markdown (deferred to B3).
- **Progress render** is already shared *helpers* but two *integrations*:
  `internal/ui/spinner.go` (`spinnerLine`, `workingLabel`, the 16 `workingPhrases`
  escalating one per 15s, `activityLineStr`, `collapseLine`) + `WaitingLine`. The
  inline host composes them via `WaitingLine`; the in-viewer "thinking" block
  composes `spinnerLine` + `activityLineStr` directly. Same parts, two call sites.
- **`OnPlaybook`/`sess.lastPB`** are wired on ALL paths (shared `openSession`), so
  escalate can capture a structured submit today — it just never triggers one
  (markdown instruction).

Progress hosts (clarified): **inline** when no viewer is open yet (no-mux `create`,
mux prompt-as-arg `create`); **in-viewer** when a viewer is already open — whether
fullscreen (no-mux escalate) or a docked pane (mux prompt-via-float `create`, mux
escalate). The docked placement is the existing `mux.SpawnDocked` viewer placement,
NOT a separate progress surface. Same widget, different host.

## Design

### Part 1 — Extract `ProgressWidget` (unify the two integrations)

A small, reusable component in `internal/ui` that owns the progress render so both
hosts produce an identical line and any tweak propagates:

```go
type ProgressWidget struct {
    frame    int    // spinner frame
    ticks    int    // 100ms ticks; elapsed seconds = ticks/10
    activity string // latest collapsed activity summary
}
func (w *ProgressWidget) Tick()                 // frame++, ticks++ (on the 100ms tick)
func (w *ProgressWidget) SetActivity(s string)  // on an activity update
func (w *ProgressWidget) Render(width int) string // spinner + workingLabel(elapsed) + elapsed, activity line below
```

- `Render` is the single composition (spinner + progression phrase + elapsed, then
  the activity line). `spinnerLine`/`workingLabel`/`activityLineStr`/`collapseLine`
  become its internals.
- **Inline host** (`progressAskModel`, `create_progress.go`): embed `ProgressWidget`;
  its tick/activity handlers call `Tick()`/`SetActivity()`; its view calls `Render()`.
  Replaces the direct `WaitingLine` call (`WaitingLine` becomes a thin wrapper over
  `ProgressWidget.Render`, or is removed if it has no other callers).
- **In-viewer host** (`model.go` thinking block): embed `ProgressWidget`; the
  existing 100ms/activity handlers drive it; the thinking render calls `Render()`.
  Replaces the ad-hoc `spinnerLine` + `activityLineStr` composition.
- Net: one render, two hosts. No behavior change for the inline path; the in-viewer
  thinking line is unified to the same layout (intended consistency).

### Part 2 — Escalate → structured authoring

`authorPlaybook` flips to the structured flow and hosts the progress **in the
viewer** (escalate's viewer is already the display surface):

1. `author.AuthorEvents(req, {Structured:true, MCPConfigPath: …})` — the agent
   diagnoses via `run`/`ask` then calls `submit_playbook`. The failure context
   (command/exit/scrollback) already flows through `SystemPrompt`/`BuildUserMessage`
   unchanged.
2. `ui.RunStream` gains a **structured mode** (`StreamOptions.Structured bool` +
   `StreamOptions.Body func() string`). In structured mode the viewer:
   - shows the `ProgressWidget` (driven by `opts.Activity`) while authoring;
   - **drains** the reader instead of rendering it as the playbook (under structured
     authoring the reader carries the agent's narration, not the playbook — the
     playbook arrives via `OnPlaybook`);
   - on stream EOF, sets `m.md = opts.Body()` — `opts.Body` is a launcher-supplied
     closure returning `playbook.Render(*sess.lastPB.Load())`, with a fallback to the
     accumulated stream text (`fo.Body()`) when no `submit_playbook` arrived (model
     misbehaved — authoring never dead-ends);
   - marks the result `finalDraft` (so `w` persists, `r` refines) and runs the
     existing finalDraft-EOF processing (preamble strip, title from H1, validity
     junk-guard → restore-and-error on a non-playbook).
3. The escalate **Reengage** uses the captured-meta seam (`capturedMetaSeam`) so the
   saved front matter comes from the structured `meta` (no separate metadata model
   pass) — same as create. `project_bound` is written.

The non-structured `RunStream` markdown-stream path remains as the structured
fallback render (when `Body()` falls back to `fo.Body()`), so nothing regresses if
the model skips the tool.

### Shared structured-authoring core (DRY)

The authoring + capture + body-closure logic is identical for create and escalate;
factor it so both call one helper (the `Structured:true` AuthorEvents + FanOut + the
`body()` closure preferring `sess.lastPB`). The **host** differs (create: inline
progress → open a fresh `run --file` finalDraft viewer; escalate: `RunStream`
in-viewer progress → render captured in place), and both hosts embed `ProgressWidget`.

## Retired / unchanged

- **Unchanged:** Phase A's schema, renderer, `submit_playbook` tool, validation,
  capture — reused verbatim. The viewer's run-block / `w` / `r` behavior. The mux
  docked-vs-fullscreen viewer placement.
- **Possibly retired:** `RunStream`'s pure markdown-stream-as-playbook mode if
  escalate was its only caller — confirm in the plan; keep it only as the fallback
  render path if still needed.
- **Deferred:** re-engagement → structured + collapse-finalize (B3); adapt-on-run +
  `project_bound` gating + `workdir` removal (B2).

## Testing

- **`ProgressWidget`:** `Render` output (spinner glyph + a known phrase at a given
  elapsed + the activity line); `Tick` advances frame/elapsed; phrase escalates per
  15s (reuse the existing `workingLabel` cases). Both hosts render via the widget
  (assert the inline + in-viewer views contain the widget line).
- **Escalate structured (launcher):** with a fake session whose backend captures a
  `submit_playbook`, `authorPlaybook`'s body resolves to `playbook.Render(lastPB)`;
  the no-submit fallback resolves to `fo.Body()`. Mirror Phase A's create end-to-end
  test (`TestCreate_StructuredRenderAndSeam`).
- **`RunStream` structured mode (ui):** in structured mode, on EOF the model's `m.md`
  becomes `opts.Body()` (not the streamed reader content), `finalDraft` is set, and
  the junk-guard restores on an invalid body. The activity feed drives the widget.
- Cross-shell / `-race` unaffected (no concurrency change beyond the existing
  `sess.lastPB` atomic).

## Out of scope

B2 (adapt-on-run structured + `project_bound` gating + `workdir` removal), B3
(re-engagement structured + collapse finalize), the viewer-UX-polish backlog, and
the file-change-representation (`file=`/diff) spec.
