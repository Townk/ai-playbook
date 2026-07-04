# Playbook-first layering: core schema + executor as `pkg/`, AI as a plug-in layer

- **Status:** Accepted
- **Date:** 2026-07-04

## Context and Problem Statement

ai-playbook's identity statement (2026-07-04, project owner): the tool exists to
let a user **reproduce a known-to-work workflow** — the *playbook* is the core,
not the *AI*. The AI layer is a guide that helps create playbooks from live
errors or descriptions; **removing it must still leave a complete, usable
tool**: a solid, rich playbook schema with a production-grade executor.

At **runtime** this already holds: `run --auto/--assisted`, `validate`, `env`,
the store verbs, and `depends_on` chains all work with no AI in the loop, and
the viewer's AI affordances degrade to inert no-ops when no re-engagement
context is wired. But the **code structure** does not reflect the layering.
Three boundary violations (found by the 2026-07-03 whole-codebase review):

1. **The schema has no single owner; its parser lives in the presentation
   layer.** The canonical fence/block parser (`{id=}`, `needs=`, `rollback=`,
   `file=`, `{static}`) is embedded in `ui.Render`'s AST walk. `internal/playbook`
   holds only the AI-submission DTO; `internal/validate` and the launcher's
   headless paths call the full styled renderer and discard the pixels just to
   extract blocks.
2. **Executor and AI layer are fused.** `internal/orchestrator` owns block
   execution, rollback, file create/undo, and diff apply (executor) *and* the
   re-engagement surface — Reengage streams, harness fan-out, cache re-store
   (AI). Replacing the AI means surgery on the executor.
3. **The UX seam is package globals.** The launcher configures the viewer
   through 14 `pending*` package variables plus `os.Args` reshaping — no
   interface, no isolation, no parallel tests.

## Decision Drivers

- Pieces must be **replaceable without touching their neighbors** (a new
  harness, a different UI, an embedded executor).
- The schema and executor deserve production-grade rigor independent of the AI
  feature set; schema-level features (Phase 6 output piping) must be designed
  against the schema's owner, not against the renderer.
- The harness seam inside `internal/author` (2026-07-04) proved the pattern one
  level down; the same cut is needed at the orchestrator and UX levels.

## Decision Outcome

Adopt a three-layer architecture, dependency direction strictly downward:

| Layer | Owns | Packages (target) |
|---|---|---|
| **Core** (public) | Playbook schema (parse/validate/render), executor (PTY driver, run engine, rollback, deps), store | **`pkg/`** — the schema + executor (+ store) are genuinely meant to be importable: an embeddable playbook runner |
| **AI** (private) | Capture, triage, authoring, harness adapters, streaming, knowledge base, response cache | `internal/` — plugs into Core through narrow interfaces (the Reengage seam, the submit-time DTO) |
| **UX** (private) | Viewer, input widgets, launcher/mux wiring, CLI | `internal/` — consumes both layers through explicit options/interfaces |

**`pkg/` promotion is the decision, staging is deliberate.** Pre-1.0 the API
may still move; promotion happens once the boundaries settle, in this order:

1. **Extract the canonical block parser** out of `ui.Render` into
   `internal/playbook` (`ParseBlocks`) — one schema owner; renderer, validate,
   launcher, and autorun all consume it. *(First, smallest, unblocks the rest.)*
2. **Split re-engagement out of the orchestrator** — the executor core becomes
   AI-free; the AI layer attaches via the narrowed Reengage interface.
3. **`ui.Run(Options)`** — replace the `pending*` globals with a real seam.
4. **Promote** the settled schema + executor (+ store) from `internal/` to
   `pkg/` (final import paths chosen then; mechanical rename).

## Consequences

- **Phase 6 (cross-block output piping) is re-classified as a Core
  schema/executor feature** and is sequenced after steps 1–2, so it is designed
  against the schema's owner. **Phase 5 (knowledge base) is an AI-layer
  feature** and may proceed independently at any time.
- Schema-adjacent backlog items (JUnit report, stored-playbook cwd rule,
  rollback semantics) graduate from polish to Core-contract work.
- Alternative front-ends and harnesses become additive work; the parked
  pi/cursor adapters plug into the existing harness seam untouched.
- Until step 4 lands, `internal/playbook` temporarily holds both the schema
  parser and the AI-submission DTO; the promotion separates them (the DTO is
  AI-layer and stays `internal/`).
