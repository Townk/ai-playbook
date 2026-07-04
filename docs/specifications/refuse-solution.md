# Refuse Solution — specification

_Status: approved 2026-07-04._

## Problem

Sometimes the assistant proposes a solution the user knows cannot be used
(wrong tool, unavailable dependency, forbidden approach). Today there is no way
to say "this is not acceptable, and here is why":

- `r` (refine) exists only when **settled** and always re-authors in **amend**
  mode — the model is instructed to *preserve* the existing steps, so it
  anchors on the very approach being rejected.
- **Mid-stream there is no cancel at all** — `q`/ESC quit the whole program;
  the only stream-supersede mechanics are internal (`reArmStreamMsg`).
- Nothing accumulates across re-engagements: each regenerate/followup prompt is
  built fresh, so a refused approach can resurface two steps later — including
  from the cache, which still holds the refused solution.

## Decisions (made with the user, 2026-07-03; revised 2026-07-04)

1. **Refusal regenerates from scratch** — the rejected content is discarded as
   a basis; the reason becomes an explicit constraint (realized via the amend
   prompt's discard instruction — see §2).
2. **Settled-only** _(revised 2026-07-04)_ — mid-stream refusal was considered
   and dropped: the in-flight progress display shows only a single line of
   model activity, so there is nothing visible to judge and refuse before the
   solution lands. `r` remains unavailable while streaming, exactly as today.
3. **One `r` binding** — no second key.
4. **Refusal reasons persist as session constraints** — injected into every
   subsequent re-engagement.

## Design

### 1. Session constraints

A `refusals []string` list on the ui model (session-lifetime, in-memory).

- Every refusal reason is appended verbatim (trimmed, non-empty only).
- Injected into **every** re-engagement prompt — all four kinds
  (`KindReengageRegenerate`, `KindReengageFollowup`,
  `KindReengageFinalPlaybook`, `KindReengageDriftRegen`) — as a new section:

  ```
  ## Constraints (user-rejected approaches)
  The user explicitly rejected the following. Do NOT propose them again,
  in this or any alternative form:
  - <reason 1>
  - <reason 2>
  ```

- Plumbing (as built): `orchestrator.Reengage.Events` (`EventsFunc`) and all
  four orchestrator re-engagement methods (`Regenerate`, `FinalPlaybook`,
  `Followup`, `DriftRegen`) carry a constraints parameter; the launcher
  (`internal/launcher/session.go reengagePrompts` and
  `drift_reengage.go`) appends the section to the built system prompt via
  `author.WithConstraints` — the four prompt builders themselves are
  unchanged. Empty list ⇒ prompts are byte-identical to today
  (characterization-tested). The degraded TEXT fallbacks (fired only when the
  events producer fails to start) do not carry constraints — recorded as a
  backlog item, practically unreachable in production.
- Cache-served solutions: a refused solution that was cached stops mattering
  in-session (the constraint steers every later re-engagement); the re-authored
  document replaces the cached one through the existing commit/wrap-up
  persistence paths, same as any refine today. No new cache plumbing.

### 2. `r` — refine, upgraded to carry refusals

Same single prompt ("What should I change?"), two additions:

- **(a)** every submitted note is ALSO recorded into `m.refusals` — refine
  steering persists exactly like refusals, so anything you have steered away
  from cannot resurface in later followups/regenerations either;
- **(b)** the AMEND branch of `author.FinalPlaybookPrompt` gains one
  instruction: *if the change note rejects the current approach outright,
  discard the base playbook and re-author from scratch honoring the note —
  do not patch the rejected approach.* Ordinary refinements amend exactly as
  today; outright rejections stop anchoring.

One prompt, one binding: the *text* carries the intent, the model decides
amend-vs-discard, and either way the note persists as a constraint.

### 3. Feedback affordances

- Status flash when a note is recorded (`noted — will avoid that from now on`
  — worded for the unified flow, where a note may amend or discard).
- A small persistent indicator while any constraints are active, in the status
  line: `N constraint(s)`. No management UI (view/edit/remove) — YAGNI;
  constraints die with the session.
- Help overlay (`?`) updated for the new `r` semantics. climeta/man document
  no pager keys, so no generated-docs change (docs-check stays green).

### 4. Out of scope (recorded, not built)

- **Answers**: the ANSWER pager has no re-engagement wiring at all (`m.orch`,
  `m.asker`, `m.askBridge` are nil there; `r` already says "refine unavailable
  in this mode"). Wiring re-engagement into answers is its own backlog item.
- Persisting constraints across sessions (a future Phase-5/knowledge-base
  concern).
- Editing/removing individual constraints mid-session.
- `create` inline-progress phase (before the viewer opens): refusal applies in
  the viewer; the inline classify/authoring wait already has ESC-cancel.

## Error handling

- Ask overlay dismissed/empty ⇒ no state change; nothing recorded.
- Re-authoring start failure after a refusal ⇒ the reason is still recorded as
  a constraint; the pane shows the standard re-engagement error path (same as a
  failed refine today).
- Constraints list is plain text passed to prompts — no escaping hazards beyond
  what `change` already has (verbatim interpolation, same as today's refine).

## Testing

- Model tests: `r` while streaming remains a no-op (unchanged, pinned); settled
  `r` submit both amends AND records the note into `m.refusals`; empty/cancel
  records nothing.
- Prompt tests: constraints section renders in all four re-engagement kinds;
  the discard-if-rejected instruction present in the AMEND branch; constraints
  **absent** ⇒ prompts byte-identical to today (characterization).
- Orchestrator: constraints parameter threading (signature-level; the fake
  EventsFunc asserts receipt).
- Help/climeta: docs-check stays green; help text updated.

## Alternatives considered

- **`R` (shift) as a dedicated refuse key** — clearer intent, but the user
  preferred a single muscle-memory binding.
- **Settled `r` asking refine-vs-refuse each time** — most explicit, most
  friction; rejected.
- **Mid-stream refusal (cancel the in-flight stream + refuse)** — designed,
  then dropped 2026-07-04: the progress display shows only one line of model
  activity, so the user cannot see enough of an in-flight solution to judge
  it. Removing it also removed the stream-supersede plumbing and the
  refine/refuse message discriminant — the whole feature now rides the
  existing `fChangeMsg` flow.
