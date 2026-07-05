# Knowledge memory: two-set taxonomy, write-time curation, whole-file recall

- **Status:** Accepted
- **Date:** 2026-07-05

## Context and Problem Statement

The `remember` MCP tool persists model-distilled facts, but the result is not
yet a usable knowledge base: one flat per-project bullet file, append-only with
no dedup or size control (every authoring call pays the whole accumulated file
in tokens, forever), recalled verbatim into ONLY the initial authoring call
(follow-up, final-playbook, and drift-regen author blind), with no browse,
search, edit, or config surface at all.

Phase 5's question: what is the *memory architecture* — what gets remembered,
where it lives, how it stays small, and how it comes back?

## Decision Drivers

- Knowledge files must stay plain, human-editable markdown — the user owns them.
- Recall must not grow unboundedly expensive as facts accumulate.
- Zero new dependencies (no vector stores, no services) — terminal-native.
- Some knowledge transcends projects (the machine, the user); some is bound to
  one (its environment, its problem domains).

## Decision Outcome

1. **Two knowledge sets.** GLOBAL (`<data-root>/knowledge.md`): `## System`
   (the machine/tooling) and `## User` (profiling — preferences, patterns).
   PER-PROJECT (`<data-root>/projects/<key>/knowledge.md`): `## Environment`
   (this project's setup) and `## Topics` (lessons keyed by problem domain,
   `### <topic>` subsections). The `remember` tool gains a `kind`
   (`system|user|environment|topic`) that routes to the right file and section;
   the system prompt instructs classification by how closely the lesson is tied
   to the topic at hand.
2. **Write-time curation, not read-time filtering.** Exact/near-duplicate
   facts are skipped at write. At **solution completion** (the wrap-up path)
   the model is instructed to remember the session's durable lessons — filling
   happens through the existing tool inside the existing call. (Amended
   2026-07-05: per the reconciled spec, the fill instruction attaches to EVERY
   MCP-wired FinalPlaybook-kind generation — the wrap-up and the
   follow-up/regenerate refine/amend paths — not the wrap-up alone.) Then, ONLY if a
   file exceeds its size budget (`[kb] budget`, generous default), a dedicated
   **compaction pass** (one small AI call per oversized file) rewrites it:
   merge near-duplicates, generalize, drop stale entries, preserve structure.
   The prior version is kept as `knowledge.md.bak` (one level) — the model
   rewriting user-visible knowledge must be undoable.
3. **Whole-file recall, bounded by construction.** Both files fold verbatim
   (global first, then project) into ALL authoring-shaped calls (initial,
   follow-up, final-playbook, drift-regen); classify/metadata stay lean. A
   hard tail-cap guards hand-edited pathological files. Because compaction
   bounds the files at write time, recall needs no relevance machinery.
4. **User-visible surface.** A public `kb` verb (show/edit/search/list,
   project- and global-scoped) with man/completion via the standard pipeline;
   `[kb]` config (budget, dir override); the kb/cache `DefaultRoot`
   duplication dedupes into one shared resolver.

## Alternatives Considered

- **Read-time budgeted recall (lexical top-K over the request)** — keeps files
  unbounded and moves complexity to every read; write-time curation keeps the
  artifact itself clean and human-readable. Rejected as the primary mechanism
  (the tail-cap is its vestigial safety form).
- **Embeddings/semantic recall** — best relevance ceiling; a heavy dependency
  + index lifecycle against the zero-services ethos, unjustified at
  human-scale fact counts. Rejected for v1.
- **Append-only forever (status quo)** — unbounded token cost, noise
  accumulation. Rejected.
- **A model-driven `forget` tool** — model deletion outside the sanctioned
  compaction moment; needs fact identity in the format. Rejected for v1
  (compaction-at-completion is the one sanctioned rewrite).
- **Single per-project file including user profile** — puts global truths in
  project silos and duplicates them; rejected in design review (owner call:
  system+user are global, environment+topic are project).

## Consequences

- Wrap-up gains a memory-fill instruction; completion gains an occasional
  compaction call (only when a file is over budget). (Amended 2026-07-05: the
  fill instruction rides every MCP-wired FinalPlaybook-kind generation — wrap-up
  plus the follow-up/regenerate refine/amend paths — so durable lessons are
  captured wherever a final playbook is produced, not only at wrap-up.)
- Legacy flat project files migrate lazily: unsectioned bullets read as
  `## Environment` until the first sectioned write.
- Recall coverage widens to the three re-engagement prompt builders.
- Spec: `docs/specifications/knowledge-base.md`.
