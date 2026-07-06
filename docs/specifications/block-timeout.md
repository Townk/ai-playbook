# Per-block execution timeout (`timeout=`)

_Status: approved 2026-07-05 (design settled with the project owner). The
v0.12.2 mini-milestone. Motivating case: an onboarding playbook's first
backup capture (`system-backup now`, legitimately minutes-long) was killed
at the runner's flat 2-minute ceiling._

## Problem

Every block run shares one hardcoded timeout —
`internal/orchestrator/orchestrator.go` `defaultTimeout = 120s`, applied
unconditionally by `Do(KindRun)` on both execution paths (the viewer /
assisted path and `--auto`). A playbook cannot declare that a step is
expected to run long, and when the ceiling kills a block the viewer shows a
plain failure (`resultMsg` drops `Result.TimedOut`), so the user cannot tell
a hang-kill from a real error.

## Decisions

1. **New fence attribute `timeout=<duration>`** on runnable blocks, Go
   duration syntax (`timeout=90s`, `timeout=15m`, `timeout=1h`):
   `` ```bash {id=first-capture timeout=15m} ``. Parsed onto
   `playbook.Block.Timeout` (zero when absent). The parser stays
   non-erroring (an unparseable value parses as zero); `validate` owns the
   errors.
2. **The default rises 120s → 10m.** A timeout exists to catch hung blocks,
   not slow ones; two minutes kills legitimate work (installs, first
   captures). `defaultTimeout` becomes `10 * time.Minute`.
3. **No unbounded escape hatch.** `timeout=0`, negative, or unparseable
   values are validate **Errors** (contract tier — same as a malformed
   `from=`); every block always has a ceiling, because unattended `--auto`
   runs must terminate. A generous explicit duration covers real cases.
4. **Timed-out failures say so.** A block killed by the timeout renders as
   `timed out after <effective duration>` (its declared `timeout=` or the
   default) instead of a plain failure — in the viewer status line and the
   `--auto` step output. Closes the existing `resultMsg drops
   Result.TimedOut` backlog item for the run paths.

## Surfaces

- **Schema** (`pkg/playbook`): `Block.Timeout time.Duration`; `buildBlock`
  parses `attrs["timeout"]` via `time.ParseDuration`, keeping zero on error.
  Schema-spec table row (shipped availability).
- **Validate** (`pkg/playbook/validate`): Error when `timeout=` is present
  but unparseable or non-positive; Warning when declared on a non-runnable
  block (`static`/`diff`/`create` — the attr is inert there; diff apply has
  its own fixed `applyTimeout`).
- **Execution** (`internal/orchestrator`): `Action.Timeout time.Duration`;
  `Do(KindRun)` passes `a.Timeout` when positive, else the (new 10m)
  default. Threaded from the parsed block at both call sites: the viewer's
  block→action conversion (`internal/ui/inprocess.go`) and autorun
  (`Step.Timeout` + its builders in `internal/autorun`).
- **Display**: the viewer's result handling and `--auto`'s step reporting
  keep `Result.TimedOut` and render `timed out after <duration>`.
- **Authoring**: the structured draft schema gains an optional per-code-item
  `timeout` (jsonschema doc: declare only for steps known to run long) and
  the fence renderer emits it; the SKILL's tag table and the schema spec
  gain the row. The rubric is untouched — this is schema, not a quality
  rule.
- **Docs/CHANGELOG**: schema spec + SKILL rows; consolidated CHANGELOG
  entries (Added: the attribute; Changed: the default; Fixed: timed-out
  runs surfaced as such).

## Out of scope (recorded, not built)

- An unbounded/`none` value.
- A playbook-wide front-matter `timeout:` default and a `[run] timeout`
  config key — revisit if per-block declarations prove repetitive.
- Retrofitting a countdown/elapsed indicator into the running spinner.

## Testing

- Parse table: absent (zero), `90s`/`15m`/`1h`, unparseable → zero.
- Validate tables: unparseable/zero/negative → Error; valid on runnable →
  no finding; valid on static/diff/create → Warning; absent → no finding.
- Orchestrator: `Do` passes the declared timeout to the driver when set,
  the default otherwise (fake driver captures the value).
- Threading: viewer conversion and autorun builders carry `Block.Timeout`
  end-to-end (both paths, fake driver assertion).
- Display: a `TimedOut` result renders the `timed out after <d>` form in
  the viewer status and `--auto` output; a plain failure is unchanged.
- Draft: renderer golden with a `timeout` item; schema JSON contains the
  field with its doc string.
- Docs-check green; SKILL/schema-spec rows present.
