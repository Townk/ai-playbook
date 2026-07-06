# Run journal and `run --retry`

_Status: approved 2026-07-05 (design settled with the project owner). The
v0.12.3 mini-milestone. Motivating case: a playbook fails mid-run on
something the user fixes out-of-band (an unrelated environment problem, a
missing login); holding the viewer open until the fix isn't always possible,
and today closing it discards all progress — the next run starts from
block one._

## Problem

Per-block run state is rich but ephemeral: the viewer's `blockRunState`
(ok/failed/stopped, exit, timed-out) and `--auto`'s status map die with the
process, and retained captures live in a temp session dir the driver removes
on Close. Nothing records that a run happened, how far it got, or how long
each step took — so there is nothing to resume from and no history to
consult.

## Decisions

1. **A per-playbook run journal, written incrementally.** Both run paths
   (viewer and `--auto`) persist the run's state to
   `<data-root>/projects/<key>/runs/<run-key>.json` — `<key>` is the
   existing sha1 project key (kb/cache convention, via the shared data-root
   resolver), `<run-key>` is the store slug for stored playbooks and a
   sha1 of the absolute path for `--file` runs. One file per playbook per
   project: each run overwrites it (latest run only). The journal is
   updated after EVERY block result via write-temp+rename (crash-safe: a
   kill mid-run loses at most the in-flight block).
2. **Journal contents.** Playbook identity (path + content sha256),
   started/finished timestamps, overall outcome, first-failure block id,
   and per-block records: outcome (`ok` | `failed` | `stopped` |
   `rolled-back`), exit code, **duration**, and `timed_out_after` when the
   ceiling killed it (the batch-8 JSON field precedent). Blocks undone by a
   rollback chain are re-recorded `rolled-back` (they are NOT ok — a retry
   re-runs them).
3. **`run --retry` resumes from the journal.** Gates, in order:
   - No journal, or last outcome was success → clear message ("nothing to
     resume"), exit 0 (success case) / exit 1 (no journal).
   - **Content-hash gate**: the playbook changed since the journaled run →
     refuse with a message naming the mismatch ("playbook changed since the
     failed run — run fresh"); no partial resume of a drifted document.
   - Otherwise: prior `ok` blocks are pre-seeded as satisfied — the viewer
     renders them distinctly ("done — previous run"), their `needs=` edges
     count as met, and the user lands at the first failed/unrun block
     (which is the natural pickup point after an out-of-band fix). A
     pre-seeded block stays manually re-runnable. `--auto --retry` skips
     pre-seeded blocks and resumes execution order from the first non-ok
     block.
4. **Previous-session producers re-run on demand (no capture
   persistence).** A pre-seeded block's outputs (`APB_OUT_<id>` env,
   retained captures) do not exist in the new session. At retry start,
   any pre-seeded block that a REMAINING block consumes — via `from=` or
   via an `APB_OUT_<id>`/`APB_ERR_<id>`/`APB_EXIT_<id>`/`APB_OUT_FILE_<id>`/
   `APB_ERR_FILE_<id>` reference in its payload (the env-scan machinery) —
   is DEMOTED to unrun, so the existing `from=`-chain auto-materialization
   and `needs=` gating make it re-run before its consumer. Blocks that were
   ok and feed nothing remaining stay skipped. Capture persistence across
   sessions is explicitly out of scope (recorded below).
5. **Discoverability.** Plain `run` (no `--retry`) on a playbook whose
   journal records a failed last run prints one hint line before starting
   fresh: `last run failed at <id> (<age> ago) — 'run --retry' resumes
   there`. Starting fresh remains the default; a fresh run overwrites the
   journal.
6. **`list` shows the last outcome.** The stored-playbook listing gains a
   last-run column sourced from the journals: `✓`/`✗` (or `–` when never
   run) plus the run's total elapsed. Journals are advisory metadata —
   a missing/corrupt journal file never breaks `run` or `list` (treated
   as "never run", stderr note on corruption).

## Semantics details

- The `verify` block is never pre-seeded: if verify was ok the run
  succeeded (nothing to resume); on any resume the goal must be re-proven.
- `stopped` and timed-out blocks resume exactly like `failed` (the retry
  point is the first non-ok block in document order).
- Pre-seeded state is session-local dressing: the journal is only read at
  startup; the retry session then journals normally (pre-seeded blocks are
  re-recorded `ok` with their PREVIOUS duration and a `previous_run: true`
  marker so history stays honest).
- A retry whose pre-seeded producer set demotes EVERY prior ok block
  degrades gracefully to a fresh run (message, not an error).
- A pre-seeded block caught by a retry-session rollback chain executes its
  undo payload in the CURRENT session, where the rolled-back run's
  value-passing vars (`$APB_OUT_/ERR_/EXIT_*`) expand empty — write
  rollbacks self-contained (against durable state, not captures).
- `--retry` composes with existing `run` flags (`--auto`, `--assisted`,
  `--file`); `run --retry <slug>` and `run --retry --file <path>` both
  resolve the same journal the non-retry form would write.

## Surfaces

- **New package** `internal/runlog`: journal model, load/save
  (write-temp+rename), the consumer-scan (`from=` + APB refs) and the
  demotion computation — pure functions over `[]playbook.Block` + journal
  state, unit-testable without a driver.
- **Viewer** (`internal/ui`): journal writes on every result (ok/failed/
  stopped/rolled-back + duration); startup pre-seed from `ui.Options`
  (loaded by the launcher, not the ui package); the "done — previous run"
  rendering; re-record-on-rerun.
- **Autorun** (`internal/autorun`): same journal writes (it already owns a
  JSON run log — the journal is separate and durable); `--retry` skip +
  resume ordering.
- **Launcher** (`internal/launcher`): `--retry` flag plumbing, the gates
  (no-journal / success / hash mismatch), the plain-`run` hint, journal
  path resolution.
- **`list`** (`internal/cli` or wherever list renders): the outcome column.
- **climeta**: the `run --retry` flag registered (docs/man/completion via
  the pipeline); `list` column mentioned in its help text if the help
  enumerates columns.
- **Docs/CHANGELOG**: configuration.md (nothing — no new config);
  README run section; consolidated CHANGELOG entry (Added: journal +
  `--retry` + hint + list column with durations).

## Out of scope (recorded, not built)

- Capture persistence across sessions (re-run-producers covers v1).
- `run --from <id>` (start anywhere) — the journal is its natural base.
- Multi-run history / journal rotation (latest run only).
- Retry for `assist`-session solution flows (scope is `run` of saved/file
  playbooks).
- A `runs`/`history` verb.

## Testing

- **runlog package**: save/load round-trip; incremental update +
  write-temp+rename atomicity (partial-write simulation); corrupt file →
  zero value + error the callers treat as "never run"; consumer-scan
  tables (from=, each APB ref form, quoted/braced, no false positive on
  unrelated `$APB_...` in static blocks); demotion tables (producer of a
  remaining block demoted; producer feeding only prior-ok blocks kept;
  transitive gating via existing needs= machinery — not re-implemented).
- **Gates**: no journal; success journal; hash mismatch; all-demoted →
  fresh-run degradation. Exit codes + messages table-tested.
- **Viewer**: pre-seed rendering (distinct done-previous form); needs=
  satisfied by pre-seeded blocks; manual re-run of a pre-seeded block
  re-records; journal written after each result (fake clock for
  durations).
- **--auto**: resume order (skips pre-seeded, starts at first non-ok);
  from=-consumer forces producer re-run end-to-end (live driver);
  journal outcomes for ok/failed/timed-out/rolled-back runs.
- **Hint**: plain run with failed journal prints the one-liner (age
  formatting); success/absent journal prints nothing.
- **list**: column for ✓/✗/– + elapsed; corrupt journal → "–" + stderr
  note.
- docs-check green (flag in man/completion).
