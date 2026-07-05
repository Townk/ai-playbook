# Knowledge base (remember / recall)

_Status: approved 2026-07-05 (design settled with the project owner; decision
record: ADR-0011). Phase 5 of the roadmap — an AI-layer feature._

## Problem

`remember` persists facts but the KB is not usable: flat per-project file,
append-only (no dedup, no size control — every authoring call pays the whole
file in tokens), recalled only by the initial authoring call, invisible (no
browse/search/edit), unconfigurable.

## Decisions (ADR-0011)

Two knowledge sets (global: System+User; project: Environment+Topics);
write-time curation (write-dedup + wrap-up fill + over-budget compaction with
`.bak`); whole-file recall into all authoring-shaped calls; a public `kb` verb.

## Storage

- **Global KB**: `<data-root>/knowledge.md` — sections `## System`, `## User`.
- **Project KB**: `<data-root>/projects/<key>/knowledge.md` (existing path) —
  sections `## Environment`, `## Topics` (topic entries as `### <topic>`
  subsections, bullets under each).
- Plain markdown, human-editable; facts are `- ` bullets. A `<!-- meta:
  project-root: <path> -->` comment line is written once per project file so
  `kb list`/`search` can show real names instead of sha1 keys.
- **Migration (lazy)**: a legacy unsectioned project file is READ as if its
  bullets were `## Environment`; the first sectioned write rewrites it into
  sectioned form (preserving all bullets under Environment).
- The kb/cache `DefaultRoot` byte-duplication dedupes: one shared resolver
  (kb calls it; placement: the smallest honest home — implementer picks and
  states it). `[kb]` config section: `budget` (bytes, default 4096, per file),
  `dir` (root override; default the shared data root).

## The `remember` tool (v2)

- Input gains `kind`: `"system" | "user" | "environment" | "topic"` (required)
  and `topic` (string, required when kind=topic, rejected otherwise).
  `projectRoot` override remains, valid only for project kinds (environment/
  topic); an override with a global kind is a tool error.
- Routing: system/user → global file section; environment → project
  `## Environment`; topic → project `## Topics` under `### <topic>` (created
  as needed, case-insensitive topic matching, stored in the submitted casing).
- **Write-dedup**: an exact-duplicate bullet (case-insensitive,
  whitespace-normalized) within the target section (or topic subsection) is
  skipped silently (tool returns ok — idempotent).
- The tool description + system-prompt guidance teach the classification:
  lessons are classified by how closely tied they are to the topic at hand —
  machine/tooling truths → system; who the user is / preferences → user;
  this project's setup → environment; domain-specific lessons → topic.
  The existing "never secrets/env dumps" rule stays verbatim.

## Fill and compact (solution completion)

- **Fill**: the wrap-up flow's prompt gains one instruction: before finishing,
  `remember` the session's durable lessons (classified per the taxonomy).
  This uses the existing tool inside the existing call — no extra round trip.
  The instruction attaches to every FinalPlaybook-kind generation (the `w`
  wrap-up and the `f`/`r` refine/amend — the code does not distinguish them at
  the prompt level), MCP-gated (omitted when the tools backend isn't wired, so
  the model is never pointed at an unavailable tool); it never attaches to
  initial authoring, follow-up, or drift-regen. Repeated refines cost only
  deduped, idempotent `remember` calls; compaction stays commit-only (ADR-0011
  — the one sanctioned rewrite fires at the solution-artifact save).
- **Compact**: after the wrap-up completes, each KB file whose size exceeds
  `[kb] budget` gets ONE compaction call (a quick structured call, same
  invocation class as classify/metadata: bounded timeout, no MCP): the model
  receives the file and returns the rewritten content — merge near-duplicates,
  generalize overlapping facts, drop stale topic entries, PRESERVE the section
  structure and the meta comment. The result replaces the file; the prior
  content is written to `knowledge.md.bak` first (one level, overwritten each
  compaction). A compaction result that is empty, not smaller than the input
  (>= — an identical rewrite is pointless to persist), missing a required
  section, or dropping the input's meta line is REJECTED (file untouched, no
  .bak, stderr note) — the model cannot destroy knowledge through a bad
  compaction.
- Under budget ⇒ no call, no cost. Failures (timeout, harness error) leave the
  file untouched (stderr note; wrap-up itself is unaffected). A file changed by
  another session during the compaction window is detected by a pre-replace
  re-read and skipped (untouched, no .bak, stderr note) — a concurrent
  `remember` is never clobbered.

## Recall

- Both files fold verbatim — global first, then project — under the existing
  `## What we already know about this project` heading (renamed section
  intro: global content under `### About this machine and user`, project
  content under `### About this project` — one heading change, characterized).
- Coverage extends from the initial authoring call to ALL authoring-shaped
  calls: `FollowupPrompt`, `FinalPlaybookPrompt`, `DriftRegenPrompt` gain the
  same fold (empty KB ⇒ byte-identical prompts — characterization-tested).
  Classify and metadata stay lean (no KB).
- A hard tail-cap (8× budget) truncates a pathological hand-edited file at
  read time with a stderr note — the vestigial safety, not the mechanism.

## The `kb` CLI verb (public)

- `kb show [--project <path>] [--global]` — default: both sets, exactly what
  recall sees (global then project for the cwd's project root); flags narrow.
- `kb edit [--project <path>] [--global]` — opens the file in `$EDITOR`
  (the store `edit` pattern); default: the project file.
- `kb search <query> [--all]` — case-insensitive substring over facts; default
  global + current project; `--all` spans every project file; results grouped
  by set/project (real names via the meta line, key as fallback).
- `kb list` — the global file (size, fact count) + every project with a KB
  (name, path, size, facts).
- climeta registration (public, not Internal), man + completion via the
  existing docgen pipeline; the dispatch↔registry sync test covers it
  automatically.

## Out of scope (recorded, not built)

- Embeddings/semantic recall; read-time relevance ranking.
- A model-driven `forget`/revise tool (compaction is the one sanctioned
  rewrite).
- Cross-machine sync; fact provenance/timestamps; multi-level `.bak` history.
- Recall in classify/metadata.

## Testing

- **kb package**: section-aware append/routing tables (all four kinds, topic
  subsection creation, case-insensitive topic match); write-dedup (exact +
  whitespace/case-normalized); legacy-file lazy migration (read-as-Environment,
  rewrite-on-first-sectioned-write preserves bullets); meta-line write-once;
  two-file separation (global kinds never touch project files and vice versa).
- **remember tool**: kind validation (missing/unknown kind, topic without
  kind=topic, projectRoot with global kind → tool errors); routing end-to-end
  through the MCP socket seam.
- **Compaction**: trigger gating (under budget = no call — fake harness
  asserts zero invocations); rejection guards (empty/not-smaller incl. the
  equal-size boundary/section-missing/meta-dropped results leave the file +
  write no .bak); `.bak` written before replace; failure tolerance (timeout
  leaves file untouched, wrap-up unaffected); the cross-session race abort
  (a write landing in the compaction window survives — no replace, no .bak).
- **Recall**: characterization — no KB ⇒ byte-identical prompts for all four
  call shapes; with both files ⇒ global-then-project order under the heading;
  tail-cap on an oversized file.
- **Fill**: the MCP-wired FinalPlaybook (wrap-up/refine) prompt contains the
  memory-fill instruction (golden); the unwired FinalPlaybook prompt and every
  other prompt shape do NOT.
- **CLI**: table tests per subcommand incl. name resolution from the meta
  line and the sha1 fallback; docs-check green (new man/completion).
