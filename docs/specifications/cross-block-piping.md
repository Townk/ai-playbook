# Cross-block output piping (`from=`)

_Status: approved 2026-07-04 (design settled with the project owner; decision
record: ADR-0010). Phase 6 of the roadmap — a CORE schema/executor feature._

## Problem

A playbook step often needs the data a prior step produced — build output fed
to an analyzer, a query result fed to a filter script. Today the only channel
is the `APB_OUT_<id>` session env vars: full output but shell-quoted (awkward
raw consumption), env-bound (size/binary fragility), undeclared in the schema
(no ordering, no validation), and unusable as stdin (blocks run with
`</dev/null`; interpreter blocks' own program text occupies stdin via heredoc).

## Decisions (ADR-0010)

Checkpointed whole-capture dataflow: `from=<id>` wires the producer's retained
stdout to the consumer's stdin; producers stay independently runnable; `from=`
implies `needs=`; auto-materialization for `from=` chains only; stdout-only v1;
driver-level session-scoped retention with raw-path env vars.

## Schema

- New fence attribute **`from=<id>`** on `shell` and `run` blocks. **`run` =
  the script blocks (python/node/ruby/perl)** — explicitly included; a python
  consumer reading `sys.stdin` is the flagship case.
- `pkg/playbook.Block` gains `From string` (empty = no producer).
- Validation (both `pkg/playbook/validate` on parsed files and the submit-time
  `internal/draft` rules):
  - the `from` target must exist and must not be the block itself;
  - the target must be a runnable block (`shell`/`run`) — `static` has no
    output; `diff`/`create` targets are rejected in v1;
  - only `shell`/`run` blocks may declare `from=` (`static`/`diff`/`create`
    consumers rejected);
  - one producer per block (v1); a comma list is a validation error;
  - ordering/cycle checks run over the **combined** `needs= ∪ from=` graph;
  - `from=<id>` implies membership in the block's effective needs set (gating,
    `--auto` ordering, dependent invalidation) without duplicating the id into
    the `needs=` attribute textually.
- The playbook schema spec (`docs/specifications/playbook-schema.md`) gains a
  **Value-passing** section documenting `from=`, `APB_OUT_FILE_<id>`/
  `APB_ERR_FILE_<id>`, the pre-existing quoted `APB_OUT_<id>`/`APB_ERR_<id>`/
  `APB_EXIT_<id>` vars (including their `printf %q` quoting), and `needs=`
  (previously undocumented in the tags table).

## Capture retention (driver)

- The driver retains each identified run's stdout/stderr as files under the
  session directory: `out_<key>` / `err_<key>` (`key` = the existing
  `sanitizeKey(id)`), overwritten when the same id re-runs, removed on
  `Close()`. Unidentified runs (`id == ""`) retain nothing.
- After each identified run the job script exports `APB_OUT_FILE_<key>` and
  `APB_ERR_FILE_<key>` with the raw retained paths (no quoting) across all
  three shell adapters (zsh/bash/sh).
- `driver.Result` gains `OutPath`/`ErrPath` (empty for unidentified runs) so
  the executor can wire stdin without re-deriving paths.

## Stdin wiring

- `orchestrator.Action` gains `StdinPath string`; `driver.RunID` gains the
  parameter. Empty → `</dev/null` exactly as today; non-empty → the job's
  subshell redirects `< <path>`.
- The executor resolves `StdinPath` from the consumer's `From`: the retained
  `out_<key>` of the producer. Missing file ⇒ the producer counts as unrun
  (see Execution).

## Payload assembly (moves to the schema owner)

- `pkg/playbook` gains the canonical block→command function (today's
  `runPayload`/`langInterp` in `internal/ui/render.go`, relocated):
  - `shell` blocks: payload as-is;
  - `run` (script) blocks: the payload is written to a session temp script
    file and the command becomes `<interpreter> <script-path>` — **no stdin
    heredoc**, so stdin is free for `from=` data. Interpreter mapping table
    unchanged (python/python3/py→python3, node/js/javascript→node, ruby,
    perl, default: the lang verbatim).
- Both the viewer and `internal/autorun` consume this function. This **fixes
  the pre-existing `--auto` interpreter bug** (headless runs previously
  executed raw script payloads through the shell — recorded in BACKLOG; its
  fix lands with this feature and gets its own CHANGELOG Fixed line and a
  regression test that is RED against the old path).

## Execution semantics

- **Gating/ordering:** `from=` participates exactly like a `needs=` edge —
  run-button gating in the viewer, `NextRunnable` ordering in `--auto`,
  `resetDependents` invalidation on undo/rollback.
- **Auto-materialization (viewer + assisted, `from=` chains only):** running a
  consumer whose transitive `from=` producers have not run this session runs
  the chain in order. Each chain step is an ordinary block run: own status
  pill, own log, own assisted confirmation. A producer with `Status == "ok"`
  this session is NOT re-run — its capture serves. A chain step failing stops
  the chain; downstream steps never start. `needs=`-only unmet dependencies
  still gate (no auto-run) — materialization follows data edges only.
- **`--auto`:** unchanged code path; topological order over the combined graph
  materializes producers before consumers. A consumer whose producer failed or
  was skipped is itself not runnable (existing `NextRunnable` semantics).
- **Producers run standalone** exactly as today; doing so pre-materializes the
  capture.

## Environment variables (consolidated contract)

| Var | Content | Notes |
|---|---|---|
| `APB_OUT_<id>` / `APB_ERR_<id>` | full output, **shell-quoted** (`printf %q` form) | pre-existing; unchanged; documented now |
| `APB_EXIT_<id>` | exit code | pre-existing |
| `APB_OUT_FILE_<id>` / `APB_ERR_FILE_<id>` | **raw path** to the retained capture file | new; args-passing idiom: `--input "$(cat "$APB_OUT_FILE_x")"` |

## UX

- Consumer blocks render a `⇐ from: <id>` annotation beside the existing
  `⊘ needs:` indicator space; a satisfied producer shows no blocker (the chain
  auto-materializes on run).
- Chain runs appear as consecutive ordinary block runs — no new modes or keys.
- `validate` reports the new rules with its existing Check/severity shapes.

## Out of scope (recorded, not built)

- Streaming/concurrent pipes (future extension over the same schema).
- Multiple producers per block (`from=a,b`).
- Stream selectors (`from=id.err`).
- First-class `args-from=` (command substitution over `APB_OUT_FILE_<id>`
  covers it; additive later if the idiom proves noisy).
- Cross-session capture persistence.

## Testing

- **Schema:** parse tables for `from=`; validate tables (missing target, self,
  static/diff/create producer or consumer, comma list, combined-graph cycles).
- **Driver (zsh/bash/sh matrix):** retention files exist with exact bytes;
  re-run overwrites; Close removes; `APB_*_FILE_*` exported raw; `StdinPath`
  wiring delivers exact bytes (incl. multi-line, no trailing-newline, binary).
- **Payload assembly:** script-file command equivalence with the old heredoc
  output for each interpreter; the `--auto` interpreter regression test (RED
  pre-fix).
- **Execution:** chain materializes unrun producers once (never re-runs an ok
  producer); failure stops the chain; assisted confirm cadence per chain step;
  `needs=`-only blocks still gate without auto-run; `--auto` ordering
  unchanged (existing suite) plus a piped `--auto` end-to-end.
- **End-to-end:** a real `produce → filter (python, reads sys.stdin) → consume`
  playbook through the driver suite, viewer path and `--auto` path.
