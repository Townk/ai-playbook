# `depends_on` — playbook composition (Design)

**Status:** approved (2026-07-02) · Phase 3 (final feature)

## Problem

`ai-playbook run` executes exactly one playbook per invocation. There is no way
to declare that playbook B must run *before* playbook A (shared setup, a
prerequisite environment). The ROADMAP's Phase 3 reserves `depends_on` for this.

## Goal

A front-matter field `depends_on: [slug, …]` that, on `run <parent>`, runs the
parent's transitive dependencies (resolved from the store, in topological order)
before the parent — with cycle/dangling detection, and with `--with-env` / `env`
spanning the whole chain.

## Design

### Schema
- `frontmatter.FrontMatter` gains `DependsOn []string` (`yaml:"depends_on,omitempty"`).
- `store.Meta` gains `DependsOn []string`, copied in `metaFromFM` (so listing/search/load surface it).
- **`finalize` preservation:** `cmd/ai-playbook/finalize.go`'s `finalizeDoc`
  rebuilds front matter from scratch and would drop `depends_on`; it must parse
  the old front matter and carry `DependsOn` forward. (The authoring
  regenerate/commit path, `orchestrator.buildFrontMatter`, would likewise drop it
  on a re-author — logged as a follow-up, out of v1 scope.)

### Dependencies are store slugs
A dependency is a store slug (global, or `proj:`-prefixed for the project-local
store). A `--file` parent may declare `depends_on` slugs; a dependency is never a
file path. Transitive: a dependency's own `depends_on` is followed.

### The shared chain resolver
A single component resolves the dependency graph, consumed by `run`, `env`, and
`validate`:

- **Pure core** — `analyzeDeps(rootDeps []string, load func(slug string) (depNode, error)) (order []depNode, issues []DepIssue)`:
  DFS traversal (mirroring `validate.detectCycles`'s 3-color algorithm) over the
  slug graph starting from `rootDeps`; returns the dependencies in **run order**
  (post-order: a dependency appears before anything that needs it; the parent is
  NOT included), plus **all** issues found — `DepIssue{Kind: "dangling"|"cycle", …}`
  (a `load` error → dangling; a back-edge → cycle, with the slug path). Pure and
  unit-testable via an injected `load`.
- **Production loader** — resolves a slug via the store (`store.PathFor` for
  existence + path; `os.ReadFile` + `frontmatter.Parse` for the full front matter,
  body, and cwd), returning `depNode{Slug, FM, Body, Cwd}` or an error (dangling).

`run` and `env` treat any `issues` as a **hard error (exit 2)**; `validate`
renders them as `Error` findings (see below).

### Run behavior
On `run <parent>` in any mode, `RunMain` (after `resolveRunArgs`, before
dispatch) loads the parent's front matter and, when `depends_on` is non-empty:

1. Resolve the chain (`analyzeDeps` + the store loader). Any `issues` →
   print + **exit 2** before anything runs.
2. Run each dependency **headless** (`autorun.Run`, the `--auto` engine), in the
   resolved order, each behind a `→ dependency: <slug>` banner. A dependency's
   auto-rollback follows the parent's setting (`!ra.NoAutoRollback`; ON for
   non-auto parents).
3. On the **first dependency failure** (non-zero `Run`), abort — remaining
   dependencies and the parent do NOT run; exit with that code. Earlier
   fully-succeeded dependencies are left in place (idempotent by design).
4. Then the **parent** runs in its chosen mode (pager / `--assisted` / `--auto`)
   via the existing path.

Dependencies are always the unattended "setup"; the parent keeps its
interactivity. v1 **always runs** dependencies (leans on idempotency, which
`validate` enforces); a "skip if `{id=verify}` already passes" optimization is
deferred.

### `--with-env` spans the chain
The single `--with-env` JSON supplies values for the parent **and every
dependency**. Each playbook's `resolveEnv` gets the shared override map and picks
the keys it declares; per-playbook precedence is unchanged
(`--with-env[name]` → exported env → that playbook's default → missing).

- The undeclared-key warning is computed **once against the union** of every
  declared var across the chain — a key meant for a dependency is not falsely
  flagged while the parent runs. Mechanically: `RunConfig` gains
  `SuppressUndeclaredWarning bool`; every playbook in a chain run is invoked with
  it set, and the chain orchestration emits the single union warning.
- `--with-env` stays **`--auto`-only** (the whole chain runs unattended under one
  JSON). *(Deferred: allowing it for pager/`--assisted` parents to feed their
  headless deps.)*

For a parent with **no** dependencies, behavior is exactly as today (no chain,
per-playbook warning).

### `env` traverses `depends_on`
`ai-playbook env <parent>` emits the **union** of declared vars across the parent
+ all transitive dependencies in one JSON — the keys the chain's `--with-env`
consumes. Each value is resolved against the environment and redacted as before.

- Deduped by name; on a name declared by multiple playbooks with different
  defaults, the **parent's default wins**, then the nearest dependency (a
  deterministic parent-first, then run-order traversal). Since values resolve
  against the environment first, this only matters for an unexported var.
- `env` uses the same resolver: a **cycle or dangling** dependency is the same
  hard error (exit 2).

### `validate` checks
When `fm.DependsOn` is non-empty, `validate` resolves the chain (in the launcher
layer, keeping `internal/validate` a pure leaf) and renders each `DepIssue` as an
`Error` finding: `playbook depends_on "<slug>", which does not exist in the store`
and `depends_on cycle: a → b → a`. Mirrors the existing `needs=` dangling/cycle
findings.

## Non-goals / deferred
- Stepping through a dependency interactively (deps are always headless).
- `--with-env` for non-`--auto` parents.
- The pre-existing bug where `--auto` on a *stored* playbook drops its declared
  `env:` (the `"playbook"` branch never surfaces `fm.Env`) — dependencies avoid it
  (they read their file fresh); the stored-parent path is logged separately.
- The regenerate/commit authoring path dropping `depends_on` (logged).
- "Skip a dependency whose `{id=verify}` already passes."

## Testing
- `frontmatter`/`store`: `depends_on` round-trips through `Parse`/`Assemble` and
  `metaFromFM`; `finalize` preserves it.
- `analyzeDeps` (pure): linear chain order; diamond dedup; a cycle → one cycle
  issue; a dangling slug → a dangling issue; multiple issues collected; the parent
  excluded from `order`.
- Chain env: the union warning fires once for a key no playbook declares and not
  for a dependency-only key; each playbook resolves its own keys from the shared
  override.
- `env`: union across a chain; parent-first collision; cycle/dangling → exit 2.
- Run wiring (seams): dependencies run before the parent in order; a failing
  dependency aborts the chain with its exit code and the parent never runs.
- `validate`: dangling-dep and dep-cycle findings.

## Files
- `internal/frontmatter/frontmatter.go`, `internal/store/store.go`,
  `cmd/ai-playbook/finalize.go`
- `internal/autorun/run.go` (`RunConfig.SuppressUndeclaredWarning`)
- `internal/launcher/deps.go` (new: `depNode`, `DepIssue`, `analyzeDeps`, the
  store loader, `resolveChain`, `runDeps`), `internal/launcher/runcmd.go`
  (`blocksFor` extraction + `RunMain`/`autoRun` wiring), `internal/launcher/envcmd.go`,
  `internal/launcher/validatecmd.go`
- `examples/` (a composition tutorial section), `docs/ROADMAP.md`,
  `CHANGELOG.md`, `docs/BACKLOG.md`
