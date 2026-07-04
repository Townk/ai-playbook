# ai-playbook — Project Roadmap

Durable single source of truth for the feature roadmap. Each phase lists its
goal, status, settled decisions, and open questions. Per-phase, step-by-step
implementation plans are written just-in-time when a phase starts (they're
ephemeral; this doc is not). Last updated: 2026-07-04 (v0.9.0).

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

## Path to 1.0 (proposed 2026-07-04)

1.0 means: **stable public contracts** (the `pkg/` API, the `ask` CLI, the
playbook schema), a **production-grade executor** (cancellation everywhere,
CI-consumable output, loud errors), and **CI we trust** (fast, no separate
race lane, darwin coverage). Proposed milestones:

- **v0.10 — the code matches the architecture.** ADR-0009 steps 4–5
  (`ui.Run(Options)`, the single `pkg/` promotion) plus the deep
  maintainability items that get harder with every feature: model.go
  decomposition, launcher consolidation, input wrapper folding.
- **v0.11 — Phase 6, cross-block output piping.** The largest remaining core
  feature, designed against the now-settled schema owner and AI-free executor.
  Rides with executor-grade polish: JUnit/XML `run --auto` report, the
  ESC-audit sweep, status-line truncation.
- **v0.12 — Phase 5, knowledge base** (AI layer, independent) plus A5a-full
  (cancellation/timeout for streaming AI calls, truncation surfaced on
  authoring paths).
- **v1.0 — hardening + trust.** Shared-test-driver speedup (retires the race
  lane), the CI hardening batch (macOS job, cache keys, dependabot,
  tidy-diff, release-notes guard), coverage ~90%, width-engine unification,
  Homebrew tap. Explicitly post-1.0: Windows/no-PTY portability, additional
  harness adapters (pi/cursor — the seam is ready), GNU info pages.


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
       | --auto [--no-auto-rollback]
                [--with-env <json|file>]]

edit   <slug>                          open the playbook in $EDITOR

validate [<slug> | --file <path>]      AI + structural review of a playbook

env    [<slug> | --file <path>]        print declared env as --with-env JSON
                                       (resolved from the environment; secrets
                                       redacted)

   (internal/aux: session · answer · finalize · mcp · input · selftest)
```

- `run`: bare positional ⇒ `--playbook`. Exactly one source of {positional,
  `--playbook`, `--file`}.
- Run modes (mutually exclusive): default = interactive pager (free-form),
  `--assisted` = guided confirm-each-step, `--auto` = unattended.
  `--no-auto-rollback` is valid only with `--auto` (auto-rollback is ON by default under `--auto`; use `--no-auto-rollback` to opt out and leave failed state in place).
- `--with-env <json|file>` (valid only with `--auto`) supplies declared `env:`
  values on the CLI (precedence over the environment; undeclared keys warned).
  The `env` command scaffolds that JSON from a playbook's declaration.

## Schema (evolves across phases)

````
Front matter (playbook .md):
  name, description, category, tags, env     [shipped]  — assembled by us + the model
  workdir                                    [Phase 1]  — target dir; adapt-on-run resolves/asks
  depends_on: [slug, …]                      [Phase 3]  — run fully, in topo order, before this playbook

Block tags (on the fenced ``` language line):
  {id=<id>}            a runnable step (auto-id when absent)
  {id=verify}          the final whole-setup verification (success detection keys on this)
  {rollback=<id>}      [shipped]  the rollback for step <id>; run completed steps' rollbacks in REVERSE on failure
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
  config), `pkg/` only for anything genuinely meant to be importable. Largely
  adopted (`cmd/` + `internal/`). **DECIDED (ADR-0009, 2026-07-04): the playbook
  schema + executor (+ store) AND the interaction toolkit ARE meant to be
  importable and will be promoted to `pkg/`** in ONE event. Staging progress:
  steps 1–3 DONE at v0.9.0 (`playbook.ParseBlocks` single schema owner; the
  AI-free executor with `internal/reengage` extracted; the standalone `ask`
  binary shipped). Remaining: `ui.Run(Options)` (step 4), then the single
  `pkg/` promotion (step 5).
- **README.md** — overview, install, quick start, the command surface, with
  badges: CI status, **test coverage**, Go Report Card, latest release,
  license. — DONE: also now covers shell completion, man pages, and the
  `apb` short binary (2026-07-03).
- **CHANGELOG.md** — [Keep a Changelog](https://keepachangelog.com) format; one
  entry per release, tied to tags. — DONE: in use since v0.5.0 (2026-07-03).
- **CI (GitHub Actions)** — `go test` (+race) + `vet` + `golangci-lint` +
  coverage upload, on push and PR. — DONE: build/vet/test + golangci-lint +
  coverage on a fast per-push lane, plus a nightly `-race` lane
  (2026-07-03).
- **Releases** — multi-platform binaries (linux/darwin × amd64/arm64; Unix-only tool)
  via [GoReleaser](https://goreleaser.com) on a version tag; checksums and an
  optional Homebrew tap. CHANGELOG drives the release notes. — DONE:
  **v0.5.0 shipped 2026-07-03** via GoReleaser (multi-platform binaries +
  checksums, curated CHANGELOG release notes); archives now ship both
  `ai-playbook` and `apb`. Homebrew tap still deferred.
- **zsh completion** — ship a full `_ai-playbook` completion: subcommands, all
  flags, and **dynamic slug completion from the store** for `run` / `show` /
  `edit` / `validate`. (This is the project's shell deliverable; a keybind/picker
  on top is user config.) — DONE: `_ai-playbook` with dynamic store-slug
  completion, `#compdef ai-playbook apb` so it also completes `apb`
  (2026-07-03).
- **man + info pages** — generate man pages (per command) and GNU
  texinfo/`info` files; include them in releases (and any Homebrew formula).
  — man pages DONE: generated `docs/man/*.1` (per command), release-packaged
  (2026-07-03). GNU info pages still open.

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
modes + rollback. **Status:** SHIPPED — the run args + adapt-on-run (below)
landed with Phase 1 (2026-06-27); the run modes (`--auto`, `--assisted`),
rollback, and execution log shipped 2026-07-01 (spec
`docs/specifications/run-modes-assisted-auto.md`; plans
`.../plans/2026-07-01-run-modes-p1-auto.md` + `...-p2-assisted.md`).

**Features**

- [DONE] `run --playbook <slug>` / `--file <path>` (positional ⇒ `--playbook`).
  Internal callers (`serveCachedPlaybook`, `answer`) move to `run --file`.
- [DONE] **Adapt-on-run:** resolve `workdir` (default to it; `ask` the user when
  absent/stale) → authoring-model rewrite for the target (paths/versions) →
  pager with an "adapted from `<slug>`" banner + `d` to view the
  original→adapted diff → drive. Junk→original fallback (reuse
  `isValidPlaybook`). Raw `--file` w/o front matter runs as-is.
- [DONE] **Run modes:** default pager (free-form); `--assisted` (guided — a
  "ready" cursor auto-scrolls each next step into view, a focusable
  `[ Run ][ Skip ][ Quit ]` footer confirms each step, a failure switches it to
  `[ Roll back ][ Leave as-is ][ Quit ]`); `--auto` (headless, `needs=` order,
  stop on first failure, non-zero exit; inline TTY / CI-friendly).
- [DONE] **Rollback** via `{rollback=<id>}` blocks. assisted → the "Roll back?"
  failure footer; `--auto` → roll back completed steps in reverse on first error;
  `--auto --no-auto-rollback` → stop, leave state as-is; `--auto` with **no**
  rollback blocks → stop + log. Never continue past a failure. (The undone
  forward step reads "↺ rolled back"; its undo command reads as a success.)
- [DONE] **Execution log:** per-step `{command, exit, output}` → a run summary +
  a JSON log under `${XDG_DATA_HOME}/ai-playbook/runs/` (`internal/autorun`).
- [DONE] **CLI env values for `--auto`:** `--with-env <inline-JSON | file>`
  supplies declared `env:` values without exporting them (precedence: `--with-env`
  → exported env → front-matter default → missing; undeclared keys warned; valid
  only with `--auto`). The companion **`env [<slug>|--file]`** command prints a
  playbook's declared env as a `--with-env`-compatible JSON template, each value
  resolved against the current environment with secrets redacted to `""`
  (round-trip: `env > env.json` → edit → `run --auto --with-env env.json`).
  (Specs `docs/specifications/2026-07-02-with-env-auto.md`,
  `.../2026-07-02-env-command.md`.)

**Settled decisions:** adapt uses the authoring model (default thinking).
Per-mode rollback behavior as above.

**Open:** the "stale workdir override" confirm (nicety). Execution-log file
location/ format.

---

## Phase 3 — Composition & validation

**Goal:** compose playbooks via dependencies; lint playbooks with the model.
**Status:** SHIPPED — `validate` shipped 2026-07-01
(`docs/specifications/validate-command.md`); `depends_on` composition shipped
2026-07-02.

**Features**

- [DONE] **`depends_on: [slug, …]`** front-matter field. On `run`, resolve +
  run dependencies **fully, in topological order, before** the parent —
  headless, regardless of the parent's own run mode. A dependency failure
  aborts the chain (nothing further runs; non-zero exit). **v1: always run**
  dependencies (lean on idempotency, which `validate` enforces); "skip if
  `{id=verify}` already passes" is a later optimization.
- [DONE] **Cycle detection:** hard error in the runner (dep cycles and
  dangling dep slugs both fail the run, exit 2); advisory in `validate` for
  `needs=` cycles plus structural `depends_on` checks (dep cycles, dangling
  slugs).
- [DONE] **`validate [<slug>|--file]`** — **deterministic** checks (front-matter
  required keys, `needs=` existence, `needs=` cycles, duplicate ids, fence
  balance; + no-runnable / missing-lang warnings) + a **model** prose review on
  the authoring model (inconsistencies, missing callouts, non-idempotent /
  destructive / non-reversible steps), with live progress (TTY spinner / CI
  stderr heartbeat) and `--no-ai` / `--plain` / `--quiet`. Exit non-zero on
  structural errors only; the AI review is advisory. (Per the shipped scope,
  "missing `{id=verify}`" and "mutating block without `{rollback}`" are routed to
  the advisory AI pass, not treated as deterministic errors; the `depends_on`
  checks — dangling dep slugs, dep cycles — arrive with `depends_on`.)

**Settled decisions:** dependencies always run for v1. validate =
deterministic + model (authoring model, no new knob). Dependency run mode when
the parent is interactive: deps always run headless (`--auto`-equivalent)
regardless of the parent's chosen mode, then the parent runs in its own mode.

**Open:** validate output format (pager vs plain).

---

## Phase 4 — Viewer affordances

**Goal:** richer pager interaction for file-backed playbooks + in-process diff
review. **Status:** SHIPPED — the `[edit]` button + on-save mtime file-watch and
the in-process side-by-side diff view all landed (source-edit W2 + the FC1 diff
view / drift work).

**Features**

- [DONE] **"edit" tag-button** (like the cached badge): opens `$EDITOR <file>`
  (no-mux: in-place suspend/resume; mux: a docked editor pane); the viewer
  **watches the file** (1s mtime poll) → reload on save. Shows only for
  file-backed (committed/store) playbooks; threads the on-disk source path into
  the model.
- [DONE] **In-process diff view ([ADR-0008](architecture/adrs/0008-in-process-diff-view.md)):**
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

## Viewer/runner — FEATURE-COMPLETE (2026-07-01)

The viewer/runner (Phase 2 run engine + Phase 4 viewer affordances) is
**feature-complete, including run-assisted** — the `--assisted` mode (its
focusable per-step footer, the failure "Roll back?" footer, the ready cursor)
shipped alongside the rest.

**Baseline (shipped earlier):** default-pager run + drive, value-passing,
verify + native confirm + auto-follow-up, copy/play, apply/undo-diff,
adapt-on-run + "adapted from" banner + `d` overlay, regenerate/followup/wrap-up/
commit re-engagement, the no-mux inline input + ask overlay.

**Landed 2026-07-01 (the recommended sequence, all done):**

1. [DONE] **Execution log** (Phase 2) — structured per-step `{command, exit,
   output}` → a run summary + a JSON log under `${data}/ai-playbook/runs/`.
2. [DONE] **`{rollback=<id>}` schema parse** (Phase 2) — in the block parser.
3. [DONE] **`--auto` mode + `--no-auto-rollback`** (Phase 2) — headless run loop,
   stop-on-first-failure.
4. [DONE] **Auto rollback flow** (Phase 2) — reverse-order rollback of completed
   steps on failure; no rollback blocks → stop + log.
5. [DONE] **Source-path threading → "edit" button → file-watching** (Phase 4).
6. [DONE] **In-process side-by-side diff view** (Phase 4, [ADR-0008](architecture/adrs/0008-in-process-diff-view.md)).
7. [DONE] **`--assisted` guided mode** (Phase 2) — the previously-carved-out
   run-assisted feature.

**Refinements (2026-07-02):** `--assisted` confirms declared variables at load
(before the first step); the variable-confirmation dialog is fully painted on its
background (prompt, buttons, hint) with an aligned + wrapping two-column var list
and a `[ Confirm ][ Customize ][ Quit ]` footer where ESC / Quit end the run;
`--with-env` + the `env` command (above) complete the non-interactive env story.
_Remaining polish (backlog):_ `choose.go` and the text-input box interior share
the same frame-background bleed and want the same treatment.

---

## Phase 5 — Knowledge base (remember / recall)

**Layer note (ADR-0009): a pure AI-layer feature — independent of the
playbook-first extractions; may proceed in parallel at any time.**

**Goal:** turn the agent's `remember` facts into a usable, recalled KB so
authoring gets smarter per project over time. **Status:** to be designed (own
brainstorm → spec). The `remember` MCP tool already persists facts; this phase
adds storage/browse/search + recall of relevant facts during
`assist`/`create`/adapt.

---

## Phase 6 — Cross-block output piping

**Re-classified (ADR-0009, 2026-07-04): this is a CORE schema/executor feature,
not an AI feature — sequence it AFTER the playbook-first layering extractions
(canonical `ParseBlocks` owner + AI-free executor) so it is designed against
the schema's owner, not the renderer.**

**Goal:** let a runnable block consume a prior block's output — pipe the
`{command, exit, output}` a step produced into a downstream step (beyond the
existing `APB_OUT_<id>`/`APB_ERR_<id>`/`APB_EXIT_<id>` value-passing env vars),
so a playbook can chain data between steps the way a shell pipeline does. This is
the single largest remaining feature on the table (runme-parity and then some);
**Status:** to be designed (own brainstorm → spec). Promoted from the backlog
2026-07-03. Consider: named block outputs, an explicit `from=<id>` reference on
the consuming block, streaming vs whole-capture semantics, and how it interacts
with `needs=`/`depends_on` ordering and `--auto` execution.

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
