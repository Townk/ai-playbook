# Cross-block data flow: checkpointed whole-capture piping via `from=`

- **Status:** Accepted
- **Date:** 2026-07-04

## Context and Problem Statement

Phase 6's goal: a runnable block consumes a prior block's output, chaining data
between steps the way a shell pipeline does. What exists today:

- `APB_OUT_<id>`/`APB_ERR_<id>`/`APB_EXIT_<id>` are exported into the live
  session shell after each identified run — full output, but **shell-quoted**
  (`printf %q` form, awkward to consume raw) and env-var-bound (fragile for
  large/binary data).
- A block's **stdin is `/dev/null`** — no true piping.
- Interpreter (script) blocks receive their own program text **via a stdin
  heredoc**, so stdin cannot also carry data — and payload assembly lives in
  the renderer, which is why `--auto` never applies the interpreter wrapping
  at all (a real bug this decision's implementation fixes).
- The schema is silent: no attribute declares a data dependency; the spec never
  documented the env vars (or even `needs=`).

The fork decided here: is `from=` a **fused pipeline** (the consumer executes
`a | b`; the producer is not independently runnable) or **checkpointed
dataflow** (the producer runs alone; its output is retained; the consumer
reads the capture)?

## Decision Drivers

- Playbook steps are **checkpoints**: individually runnable, verifiable,
  rollback-able, individually confirmed in `--assisted`. A block without its
  own status breaks rollback graphs, per-step verify, and the assisted cadence.
- The executor is deliberately serial (one block at a time); concurrency is a
  different product.
- Iterating on a downstream step without re-paying an expensive upstream step
  ("replay without re-paying") is a high-value property for troubleshooting
  workflows.
- `needs=` today is gate-only; nothing auto-runs. Any auto-run behavior must be
  principled, not ambient.

## Decision Outcome

**Checkpointed whole-capture dataflow.**

1. **`from=<id>`** (new fence attribute, one producer in v1): the consumer's
   stdin is the producer's **complete captured stdout** — raw bytes from a
   session-retained file. Applies to `shell` AND `run` blocks (`run` = the
   script blocks: python/node/ruby/perl — explicitly included; the design
   exists largely for them). `static`/`diff`/`create` blocks cannot declare it.
2. **The producer stays independently runnable** — running it simply
   pre-materializes its capture.
3. **`from=` implies `needs=`** for ordering, validation (existence + cycles
   over the combined graph), and invalidation (undo of the producer invalidates
   the consumer through the existing dependents graph).
4. **Auto-materialization, `from=` chains only**: in the viewer/assisted,
   running a consumer whose producer has not run this session runs the chain in
   order — each step with its own status and its own assisted confirmation; an
   already-ran producer is NOT re-run (the capture serves). A producer failing
   stops the chain. `--auto` is unchanged (topological order already
   materializes producers). **`needs=` remains gate-only** — it asserts order;
   `from=` demands data, and missing data is materialized on demand
   (make-style).
5. **Retention is driver-level and session-scoped**: per-block capture files
   (`out_<id>`/`err_<id>` under the session dir, removed on Close), exported as
   **`APB_OUT_FILE_<id>` / `APB_ERR_FILE_<id>`** — raw paths, no quoting, no
   size limit — serving stdin wiring, args-passing (`$(cat "$APB_OUT_FILE_x")`),
   and random access. The existing quoted `APB_OUT_<id>` vars remain unchanged
   for compatibility.
6. **Payload assembly moves to the schema owner** (`pkg/playbook`): the
   canonical block→command function; interpreter blocks switch from stdin
   heredocs to temp script files, freeing stdin for data and fixing the
   `--auto` interpreter bug (viewer and autorun share the one function).
7. **stdout only in v1**; stderr stays reachable via `APB_ERR_<id>`/
   `APB_ERR_FILE_<id>`.

## Alternatives Considered

- **Fused pipeline (`a | b` as the executable unit; producer not runnable
  alone)** — shell-native intuition, but destroys the checkpoint model
  (statusless producers, muddy rollback/verify, broken assisted cadence) and
  its real form needs concurrent execution. Rejected.
- **Streaming pipes (concurrent producer/consumer)** — a fundamental executor
  redesign (serial `runMu`, per-block status, reverse rollback all assume
  serial). Recorded as a possible future extension over the same schema;
  rejected for v1.
- **Template interpolation (`{{out.id}}` in payloads)** — invents a template
  syntax inside shell code, quoting/escaping hazards, breaks copy-paste-ability.
  Rejected.
- **Env-file only (no schema change)** — no declared data dependency, no
  ordering/validation, boilerplate consumption. Rejected as the whole answer;
  its useful part (`APB_OUT_FILE_<id>`) ships as part of the decision.
- **First-class `args-from=`** — command substitution over `APB_OUT_FILE_<id>`
  already expresses args idiomatically; deferred (additive later, not
  breaking).

## Consequences

- The `--auto` interpreter-heredoc bug is fixed as a by-product; the renderer
  sheds another piece of core semantics (per ADR-0009's direction).
- The schema spec gains a **Value-passing** section documenting `from=`, the
  new file vars, the pre-existing quoted env vars, and (finally) `needs=`.
- `pkg/playbook.Block` gains `From` (public API growth; pre-1.0 caveat holds).
- Captures are session-scoped like block statuses; a fresh session starts
  clean and chains re-materialize on demand.
- Spec: `docs/specifications/cross-block-piping.md`.
