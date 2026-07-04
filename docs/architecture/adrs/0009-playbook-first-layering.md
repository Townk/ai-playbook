# Playbook-first layering: core schema + executor as `pkg/`, AI as a plug-in layer

- **Status:** Accepted
- **Date:** 2026-07-04 (revised 2026-07-04: the interaction toolkit added as a
  fourth surface with its own standalone binary — folded here rather than a
  separate ADR so the `pkg/` promotion is a single event)

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
| **Interaction toolkit** (public) | The user-interaction dialogs (confirm/line/text/choose/form) and their standalone `ask` binary — a product surface of its own, already consumed by external scripts (the user's chezmoi shims) | `internal/input` today; joins the `pkg/` promotion (step 4). Public CLI contract: subcommand-per-widget, exit codes as the answer, `ASK_*` env theming, JSON form spec. ai-playbook's private float plumbing (`--out`/FIFOs/`--thinking`/`--history`) stays on the hidden `ai-playbook input` |
| **UX** (private) | Viewer, launcher/mux wiring, CLI | `internal/` — consumes the Core, AI, and toolkit layers through explicit options/interfaces |

**`pkg/` promotion is the decision, staging is deliberate.** Pre-1.0 the API
may still move; promotion happens once the boundaries settle, in this order:

1. **Extract the canonical block parser** out of `ui.Render` into
   `internal/playbook` (`ParseBlocks`) — one schema owner; renderer, validate,
   launcher, and autorun all consume it. *(First, smallest, unblocks the rest.)*
2. **Split re-engagement out of the orchestrator** — the executor core becomes
   AI-free; the AI layer attaches via the narrowed Reengage interface.
3. **Build the `ask` binary** (`cmd/ask` + a thin subcommand layer over
   `internal/input`; man page + completion via the docgen pipeline) — the
   toolkit's public contract exists before promotion, so promotion changes
   import paths only, never the contract. *(Independent of steps 1–2; may run
   in parallel.)*
4. **`ui.Run(Options)`** — replace the `pending*` globals with a real seam.
5. **Promote** all public surfaces in ONE event — schema + executor (+ store)
   AND the interaction toolkit — from `internal/` to `pkg/` (final import
   paths chosen then; mechanical rename; `ask`'s imports flip once).

## Consequences

- **Phase 6 (cross-block output piping) is re-classified as a Core
  schema/executor feature** and is sequenced after steps 1–2, so it is designed
  against the schema's owner. **Phase 5 (knowledge base) is an AI-layer
  feature** and may proceed independently at any time.
- Schema-adjacent backlog items (JUnit report, stored-playbook cwd rule,
  rollback semantics) graduate from polish to Core-contract work.
- Alternative front-ends and harnesses become additive work; the parked
  pi/cursor adapters plug into the existing harness seam untouched.
- Until the promotion lands, `internal/playbook` temporarily holds both the
  schema parser and the AI-submission DTO; the promotion separates them (the
  DTO is AI-layer and stays `internal/`).
- The `ask` binary ships from this repo (the `apb` pattern — a third
  GoReleaser build), NOT a separate repo: the standalone `ai-assist-input`
  binary was deliberately retired into this module at v0.6.0; this decision
  re-offers the standalone *surface* without re-fragmenting the codebase.
- `internal/input` gains a second consumer and therefore an external
  compatibility bar: widget behavior changes must consider `ask`'s documented
  contract, not just ai-playbook's own dialogs.

## Promotion (2026-07-04): completed except `pkg/runner`

Step 5 was executed after steps 1–4 landed (`playbook.ParseBlocks` schema
owner, orchestrator reengagement split, the `ask` binary, `ui.Run(Options)`).
Purity verification (`go list` over each candidate's import graph; a `pkg/`
package may import only stdlib, third-party deps, and other `pkg/` packages —
never `internal/`) found three candidates coupled to private leaf packages.
Two of those couplings were shallow and were cut within the approved paths
(store: an explicit-dirs configuration surface; dialog: its theme leaf
relocated as a subpackage); the third — the executor's — genuinely needs
design work, so `pkg/runner` is the sole deferral.

**Promoted (each verified `pkg/`-pure):**

- `internal/playbook` (schema half) → `pkg/playbook` — `ParseBlocks`, `Block`,
  `ParseFenceInfo`, `NormalizeFences`, and the `{id=…}`/`{rollback=…}`/
  `{static}`/`file=`/`needs=` grammar (the schema owner).
- `internal/frontmatter` → `pkg/playbook/frontmatter`.
- `internal/validate` → `pkg/playbook/validate`.
- `internal/driver` → `pkg/driver`.
- `internal/store` → `pkg/store` — decoupled first: the package-level
  config/capture seams were replaced by an explicit configuration surface
  (`store.Dirs{Global, Project}`; the four operations are its methods), so the
  caller resolves configuration itself and the store performs no
  config/environment lookup of its own.
- `internal/input` → `pkg/dialog` (`package input` → `package dialog`) — the
  interaction toolkit; `internal/askcli` stays internal and imports it (its
  suite passes with mechanical import/selector updates only).
- `internal/theme` → `pkg/dialog/theme` — the shared palette was the dialog
  toolkit's only internal dependency; it relocates as a subpackage of the
  approved path (package name unchanged; `internal/ui` and `internal/diff`
  import the new path).
- The AI submit-time DTO + `Render` + submit-time `Validate` (the other half of
  `internal/playbook`) → `internal/draft` — AI layer, stays private.

**Remaining work — `pkg/runner` ← `internal/orchestrator` (deferred):** the
executor imports `internal/mux` (it holds a `mux.Mux` for edit/float pane
spawning, and mux drags `internal/config` → `internal/cache`) and
`internal/diff` (`diff.Parse` on the diff-apply path; diff also renders, which
is why it owns a theme dependency). The mux coupling genuinely needs design —
the executor must either take a narrowed pane-spawning interface it owns, or
the mux layer itself must become public; neither is a mechanical cut.
`internal/autorun` → `pkg/runner/auto` waits on the same decision (it imports
the orchestrator and `internal/cache`).
