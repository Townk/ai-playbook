# ai-playbook — Project Roadmap

Durable single source of truth for the feature roadmap. Each phase lists its
goal, status, settled decisions, and open questions. Per-phase, step-by-step
implementation plans are written just-in-time when a phase starts (they're
ephemeral; this doc is not). Last updated: 2026-06-26.

## Vision

ai-playbook is a **harness-agnostic, terminal-native AI assistant** that turns
your live shell context into **runnable, reusable playbooks**. Two entry verbs:

- **`assist`** — triage a request → a one-line command, a short answer, or a
  full playbook; reactive to terminal failures; cache-served.
- **`create`** — author a playbook directly (always fresh).

A **playbook store** then makes playbooks browsable/searchable (via an external picker fed by a machine-readable list), re-runnable with **adaptation to the current project**,
**composable** (dependencies), **safely executable** (assisted / unattended +
rollback), and **lint-able** (`validate`).

### Competitive positioning (vs. executable-markdown tools, e.g. runme)

We overlap on "runnable markdown with tagged blocks" but lead on: **generative
authoring** (the model writes the playbook from your live context),
**reactivity** to failures, **adapt-on-run**, **dependency composition**, and
**AI validation**. Tools like runme are author-it-yourself execution platforms
(strong on docs-as-code breadth, notebooks, maturity); we are AI-first and
terminal-native. We are at parity on multi-language execution
(python/node/ruby/perl via interpreter) and close the docs-as-code gap
with the **project-local store** below.

## Command surface (target)

```
assist [<prompt>]                      triage → command/answer/playbook;
                                       cache badge; interactive entry

create <prompt> [--template <t>]       author a playbook directly
                                       (always fresh; writes store+cache)

list   [--format human                 return all the playbook store in
       | fuzzy-data-source             different formats
       | json]

search <query> [--format ...]          filter the store

show   <slug>                          render a playbook (read-only)

run    [[--playbook] <slug>            execute a runbook
       | --file <path>]
       [--assisted
       | --auto [--no-auto-rollback]]

edit   <slug>                          open the playbook in $EDITOR

validate [<slug> | --file <path>]      AI + structural review of a playbook

   (internal/aux: session · answer · finalize · mcp · input · selftest)
```

- `run`: bare positional ⇒ `--playbook`. Exactly one source of {positional,
  `--playbook`, `--file`}.
- Run modes (mutually exclusive): default = interactive pager (free-form),
  `--assisted` = guided confirm-each-step, `--auto` = unattended.
  `--no-auto-rollback` is valid only with `--auto` (auto-rollback is ON by default under `--auto`; use `--no-auto-rollback` to opt out and leave failed state in place).

## Schema (evolves across phases)

````
Front matter (playbook .md):
  name, description, category, tags, env     [shipped]  — assembled by us + the model
  workdir                                    [Phase 1]  — target dir; adapt-on-run resolves/asks
  depends_on: [slug, …]                      [Phase 3]  — run fully, in topo order, before this playbook

Block tags (on the fenced ``` language line):
  {id=<id>}            a runnable step (auto-id when absent)
  {id=verify}          the final whole-setup verification (success detection keys on this)
  {rollback=<id>}      [Phase 2]  the rollback for step <id>; run completed steps' rollbacks in REVERSE on failure
  {static}             non-runnable (no run button)
````

---

## Foundations (shipped)

- Go binary unifying + replacing a retired shell-script stack; harness-agnostic
  design (Claude harness today); invoked directly or bound to a shell key.
- `assist` triage (command / answer / escalate) + routing; **cache-by-kind**
  (repeat command/answer/playbook served without re-classify); **cached-answer
  in-place invalidate** (reload re-runs the cheap classify).
- In-process re-engagement (regenerate / follow-up / wrap-up);
  **auto-follow-up** on a failed verify; native verify-success confirm (green
  ask-style buttons, `c` to generate); the wave thinking animation.
- **Replace-protection** (never persist a non-playbook over the resolved
  troubleshoot).
- **Perf:** classify runs thinking-OFF (~2.6s vs ~7-9s); async session open
  (cached playbooks render instantly, shell buttons enable when ready); answer
  skips the driver.
- Front matter (name/description/category/tags/env) + `finalize` backfill;
  multi-language run blocks (shell + python/node/ruby/perl via interpreter
  heredocs).
- Cleanup/rebrand: `AI_PLAYBOOK_*` env vars, `ai-playbook` labels + cache
  schema, corrected system-prompt tool refs (MCP run/ask/remember), dead-FIFO +
  `--results-fifo` removal.

---

## Project infrastructure & distribution

A cross-cutting track, independent of the feature phases (some near-term, some
ongoing). Keeps ai-playbook a standalone, installable, well-documented Go tool;
any wiring into a particular shell/dotfiles setup is separate and secondary.

- **Repo layout** — adopt
  [golang-standards/project-layout](https://github.com/golang-standards/project-layout):
  `cmd/ai-playbook/` (the binary `main`), `internal/` (the private packages: ui,
  author, driver, orchestrator, triage, cache, capture, mux, tools, input,
  config), `pkg/` only for anything genuinely meant to be importable (candidate:
  `store`). Foundational — do this FIRST (before Phase 1 adds the `store`
  package), since it rewrites every import path. **[near-term]**
- **README.md** — overview, install, quick start, the command surface, with
  badges: CI status, **test coverage**, Go Report Card, latest release, license.
- **CHANGELOG.md** — [Keep a Changelog](https://keepachangelog.com) format; one
  entry per release, tied to tags.
- **CI (GitHub Actions)** — `go test` (+race) + `vet` + `golangci-lint` +
  coverage upload, on push and PR.
- **Releases** — multi-platform binaries (linux/darwin × amd64/arm64; Unix-only tool)
  via [GoReleaser](https://goreleaser.com) on a version tag; checksums and an
  optional Homebrew tap. CHANGELOG drives the release notes.
- **zsh completion** — ship a full `_ai-playbook` completion: subcommands, all
  flags, and **dynamic slug completion from the store** for `run` / `show` /
  `edit` / `validate`. (This is the project's shell deliverable; a keybind/picker
  on top is user config.)
- **man + info pages** — generate man pages (per command) and GNU
  texinfo/`info` files; include them in releases (and any Homebrew formula).

---

## Phase 1 — Store & entry verbs

**Goal:** make the accumulating playbooks a browsable/searchable/editable
library (via an external picker), and split the entry verbs. **Status:** SHIPPED
(2026-06-27) — `internal/store`, `list`/`search`/`show`/`edit`, `assist`/`create`
split, `workdir` front matter, configurable `[store]` dirs. Dotfiles FZF-pick
pairing in progress (separate repo).

**Features**

- `store` package: scan **both** `${XDG_DATA_HOME}/ai-playbook/playbooks/*` and
  `${PROJECT_ROOT}/.ai-playbook/playbooks/*`; parse front matter → `Meta`.
  Project-local entries get a **`proj:`**-prefixed slug. On-demand scan, no DB.
- `list` / `search` with `--format human|fuzzy-data-source|json`.
  - `fuzzy-data-source`: `<display>\x1f<slug>\x1f<path>` per line (for a picker
    like fzf: show field 1, ENTER → `run {2}`, ALT+ENTER → `edit {2}`).
- `show <slug>` (read-only), `edit <slug>` (`$EDITOR`).
- **`assist`** (rename of `troubleshoot`) — the **only** triage entry; keeps the
  cache badge. **`create <prompt>`** — direct author, always fresh, writes
  **store + cache**, no cache badge, "similar playbooks exist: …" banner from a
  store search.
- Add the **`workdir`** front-matter field (+ `finalize` backfill from
  provenance).
- Shell integration (project deliverable): the `--format fuzzy-data-source`
  output is the documented contract for any external picker; ship/extend the
  `zsh` completion accordingly (see Infrastructure). Wiring a keybind + picker
  into a particular shell config is user-side ergonomics (secondary).

**Settled decisions:** `proj:`-prefixed = project, unprefixed = global. `create`
writes store+cache but never _serves_ a cache hit. Cache badge gated to `assist`
only. Detailed spec:
`docs/specifications/phase-1-live-playbook-store.md`.

**Open:** `create` runs in the invoking pane vs a docked pane.

---

## Phase 2 — Run engine

**Goal:** `run` a store playbook (or file) with adaptation and three execution
modes + rollback. **Status:** PARTIALLY SHIPPED — the run args + adapt-on-run
(below) landed with Phase 1 (2026-06-27); the run modes, rollback, and execution
log are NOT started (the remaining Phase 2 work).

**Features**

- [DONE] `run --playbook <slug>` / `--file <path>` (positional ⇒ `--playbook`).
  Internal callers (`serveCachedPlaybook`, `answer`) move to `run --file`.
- [DONE] **Adapt-on-run:** resolve `workdir` (default to it; `ask` the user when
  absent/stale) → authoring-model rewrite for the target (paths/versions) →
  pager with an "adapted from `<slug>`" banner + `d` to view the
  original→adapted diff → drive. Junk→original fallback (reuse
  `isValidPlaybook`). Raw `--file` w/o front matter runs as-is.
- **Run modes:** default pager (free-form); `--assisted` (scroll to next un-run
  step, "Proceed?", run, log); `--auto` (unattended).
- **Rollback** via `{rollback=<id>}` blocks. assisted → confirm "Step X failed.
  Roll back?"; `--auto` → roll back completed steps in reverse on first error;
  `--auto --no-auto-rollback` → stop, leave state as-is. `--auto` with **no**
  rollback blocks → behaves like `--no-auto-rollback` (nothing to undo → stop +
  log). Never continue past a failure.
- **Execution log:** per-step `{command, exit, output}` → a run summary surfaced
  to the user + written to a log file under the data dir.

**Settled decisions:** adapt uses the authoring model (default thinking).
Per-mode rollback behavior as above.

**Open:** the "stale workdir override" confirm (nicety). Execution-log file
location/ format.

---

## Phase 3 — Composition & validation

**Goal:** compose playbooks via dependencies; lint playbooks with the model.
**Status:** not started.

**Features**

- **`depends_on: [slug, …]`** front-matter field. On `run`, resolve + run
  dependencies **fully, in topological order, before** the parent (in auto
  mode). A dependency failure aborts the chain (rollback per the active mode).
  **v1: always run** dependencies (lean on idempotency, which `validate`
  enforces); "skip if `{id=verify}` already passes" is a later optimization.
- **Cycle detection:** hard error in the runner; advisory in `validate`.
- **`validate [<slug>|--file]`** — combine **deterministic** checks (circular
  deps, dangling dependency slugs, missing `{id=verify}`, a mutating block with
  no `{rollback}`) + **model** checks on the authoring model (idempotency,
  destructive/ non-reversible commands, unclear steps). Reports findings.

**Settled decisions:** dependencies always run for v1. validate =
deterministic + model (authoring model, no new knob).

**Open:** dependency run mode when the parent is interactive (likely: deps run
`--auto` regardless, then the parent in its chosen mode). validate output format
(pager vs plain).

---

## Phase 4 — Viewer affordances

**Goal:** richer pager interaction for file-backed playbooks + in-process diff
review. **Status:** not started.

**Features**

- **"edit" tag-button** (like the cached badge): opens `$EDITOR <file>` in a new
  mux tab; the viewer **watches the file** (fsnotify, poll fallback) → re-render
  on save. Shows only for file-backed (committed/store) playbooks. Requires
  threading the on-disk source path into the model first.
- **In-process diff view ([ADR-0008](architecture/adrs/0008-in-process-diff-view.md)):**
  one pure-Go **side-by-side** (syntax-highlighted) renderer for both the
  `diff`-block "view diff" button AND the adapt-on-run `d` overlay, presented
  **mux-aware** — a floating pane when a mux is on, a viewer modal overlay when
  off. Unified diff stays only for the inline block body. **Drops** the external
  `hunk`/`delta`/`less` chain, `AI_PLAYBOOK_HUNK_BIN`, and the never-built "review
  diff" (model-annotate + user-comment loop). Word-level intra-line highlight is
  deferred polish.
- (The **assisted-run** flow + execution log + `{rollback}` schema are
  implemented in Phase 2; this phase is the standalone editing UX + diff view.)

**Open:** file-watch mechanism (fsnotify vs poll) per platform.

---

## Viewer/runner — path to feature-complete (excluding run-assisted)

The viewer/runner is feature-complete when Phase 2 (run engine) and Phase 4
(viewer affordances) land **minus the run-assisted feature** (the `--assisted`
mode, its per-step "Proceed?" confirm, the assisted "Roll back?" confirm, and the
assisted-run button). Crucially, **none of the items below are entangled with
run-assisted** — they are the shared substrate run-assisted later sits on top of.

**Already shipped (baseline):** default-pager run + drive, value-passing,
verify + native confirm + auto-follow-up, copy/play, apply/undo-diff,
adapt-on-run + "adapted from" banner + `d` overlay, regenerate/followup/wrap-up/
commit re-engagement, the no-mux inline input + ask overlay.

**Remaining (recommended sequence):**

1. **Execution log** (Phase 2) — structured per-step `{command, exit, output}` →
   a run summary + a log file under the data dir (today: ad-hoc `/tmp` per-block
   logs). *Shared substrate — build first.*
2. **`{rollback=<id>}` schema parse** (Phase 2) — add the field to the block parser.
   *Unblocks rollback.*
3. **`--auto` mode + `--no-auto-rollback`** (Phase 2) — headless run loop,
   stop-on-first-failure.
4. **Auto rollback flow** (Phase 2) — reverse-order rollback of completed steps
   on failure; no rollback blocks → stop + log.
5. **Source-path threading → "edit" button → file-watching** (Phase 4).
6. **In-process side-by-side diff view** (Phase 4, [ADR-0008](architecture/adrs/0008-in-process-diff-view.md))
   — independent; can run in parallel from the start.

---

## Phase 5 — Knowledge base (remember / recall)

**Goal:** turn the agent's `remember` facts into a usable, recalled KB so
authoring gets smarter per project over time. **Status:** to be designed (own
brainstorm → spec). The `remember` MCP tool already persists facts; this phase
adds storage/browse/search + recall of relevant facts during
`assist`/`create`/adapt.

---

## Parked / deferred (intentionally, until the phases above land)

- **Harness adapters (pi / cursor)** — explicitly deferred until the whole
  project (all phases incl. KB) is complete. The harness layer is already
  pluggable in design.
- **`create --template`** — manual playbook templates (the arg is reserved).
- **Headless/CI niceties** beyond `run --auto` (e.g. a JUnit-style report) —
  revisit if CI usage materializes.
- ~~Wave-pause~~ — RESOLVED: the loop is mathematically seamless (measured); the
  perceived pause was the SSH transport, not the animation. No action.
