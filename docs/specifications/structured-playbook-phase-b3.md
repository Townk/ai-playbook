# Structured Playbook — Phase B3: re-engagement → structured + follow-up-aware finalize

Status: agreed (2026-06-29). Final sub-phase of `structured-playbook-output.md` → Phase B
(B1 escalate, B2a/B2b portability+confirmation, B3 this). Builds on the Phase-A schema/
renderer, the B1 structured authoring core (`structuredStream`/`structuredBody`/
`RunStream` Structured mode), and the `submit_playbook` capture (`sess.lastPB`).

## Goal

Make the troubleshoot/re-engagement path produce its playbook the same structured way
`create` and `escalate` do, and make saving **follow-up-aware**: if the proposed
playbook ran clean, save it as-is; if the run diverged (a follow-up occurred), re-author
a fresh structured playbook that captures what actually worked. This closes the last
markdown-authoring path and removes the old always-regenerate-on-`w` (the 2-H1 bug).

## Background

The first authoring is already `create`-shaped: escalate (B1) diagnoses, calls
`submit_playbook`, and renders the proposed playbook as a `finalDraft` the user runs.
What remained on markdown is the **re-engagement** family (regenerate / refine-amend /
the verify-success wrap-up), which streams markdown into the open viewer via
`orchestrator.FinalPlaybook`/`Regenerate` (`buildReengageEvents` calls
`RunHarnessEvents` WITHOUT `Structured:true`), and the troubleshoot `w` which
**regenerates** a playbook even after one is already rendered.

## The pivot — `hadFollowup`

`hadFollowup` is true when **any** follow-up ran this session: the auto verify-fail retry
(a RUN of the verify block with a non-zero exit) or the manual "try another fix". It is
the signal that the run **diverged** from the originally-proposed playbook.

- It is set whenever a follow-up is launched (`beginFollowupInProc` / the auto path).
- It is **reset** after a re-author completes (the re-authored doc now reflects the
  resolution, so it is treated as final — no re-author loop).

The fix-didn't-work **Followup itself stays markdown** — it is troubleshoot
*continuation* (more diagnosis the user runs), not playbook authoring. It is the thing
that *sets* `hadFollowup`; it does not produce a `submit_playbook`.

## The save decision (core logic)

Saving is reached from two triggers (below); both run the same decision:

- **`hadFollowup == false`** → the rendered playbook is exactly what was authored and it
  ran clean → **persist the current doc as-is** (`commitPlaybookCmd(m.md)`). No
  re-authoring. This is the create/escalate persist-only path.
- **`hadFollowup == true`** → the resolution diverged → **re-author** a fresh structured
  playbook (the "create a new playbook with this information" flow): `FinalPlaybook`
  authored **structured** (`submit_playbook` → `sess.lastPB`), rendered as a new
  `finalDraft`; `hadFollowup` resets. The user reviews; a subsequent `w` persists it
  (now `hadFollowup == false`).

## Save triggers

1. **Verify-success offer.** When the user runs the final verify block and it exits 0
   (`msg.ID == verifyID && msg.Exit == 0 && !m.wrappedUp`), the viewer offers to save
   (the existing native confirm row). On accept → the save decision above.
2. **Manual `w`.**
   - `finalDraft && committed` → no-op ("already saved").
   - **fully run + verified** (the verify block has succeeded) → the save decision
     directly.
   - **not fully run/verified** → a **confirm dialog** first: "This playbook wasn't fully
     run, so we couldn't verify it works — save this state as a new playbook anyway?"
     On confirm → the save decision. (Reuses the existing confirm widget — the same
     `input.NewAsk` "confirm" overlay B2b uses.)

## Structured migration (Part 1)

The playbook-producing re-engagements author structured, reusing the B1 core:

- `buildReengageEvents` (`internal/launcher/session.go`) passes `Structured: true` to
  `RunHarnessEvents` for the **playbook-producing** kinds (`KindReengageFinalPlaybook`,
  `KindReengageRegenerate`) so the agent calls `submit_playbook` → `OnPlaybook` →
  `sess.lastPB`. `KindReengageFollowup` stays markdown.
- Re-engagement runs **inside the open viewer** (via `reArmStreamMsg`, not a fresh
  `RunStream`), so the in-viewer re-arm path becomes **structured-aware**: when it arms a
  structured re-engagement stream it sets `m.structured = true` + a `bodyProvider` that
  renders the captured `sess.lastPB` (`playbook.Render`, with `Portabilize` when
  `project_bound`), so the stream's EOF renders the captured playbook (REPLACE) — exactly
  as `RunStream` does at startup for escalate. The body closure (which reaches
  `sess.lastPB`) is threaded from the launcher where the session lives, analogous to how
  escalate supplies `RunStream.Body`; the plan resolves the exact seam.
- The amend/fresh inputs stay in the prompt: `FinalPlaybookPrompt(req, base, change)`
  (base = the served playbook for amend, change = the troubleshoot resolution / the
  `r`/`f` typed adjustment). Only the *delivery* moves to `submit_playbook`.
- The orchestrator's text fallbacks (`author.FinalPlaybookText` etc.) remain as the
  EventsFunc-failure fallback.

## The finalize collapse (Part 2)

- Remove the troubleshoot `w`'s **always-regenerate** branch
  (`beginFinalPlaybookInProc`/`beginFinalPlaybookGenerate` invoked unconditionally) and
  the `persistOnFinish` **auto-baseline** EOF commit. `w` becomes the save decision:
  persist-as-is when `!hadFollowup`, re-author when `hadFollowup`. The verify-success
  wrap-up no longer auto-persists — it renders a `finalDraft` the user reviews, then `w`
  persists (unifying with create/escalate).
- Net: no double-author (the 2-H1 bug is gone for the troubleshoot path), and `w` on a
  clean run is a pure commit.

## Cleanup (Part 3)

The non-structured `RunStream` markdown path is **kept** — it remains the
`authorPlaybookText` harness-missing fallback for initial authoring (create/escalate),
the only remaining non-structured `RunStream` caller. B3 does not retire it.

## Components (decomposition)

- **`hadFollowup` tracking** — set on any follow-up launch; reset after a re-author.
  Model field + the set/reset points.
- **Structured re-engagement producer** — `buildReengageEvents` `Structured:true` for the
  playbook-producing kinds; the orchestrator re-engagement methods deliver via
  `submit_playbook`/`sess.lastPB` (the body source) instead of streaming markdown.
- **In-viewer structured re-arm** — the re-arm path sets `m.structured` + `bodyProvider`
  (render `sess.lastPB`) so a structured re-engagement renders the captured playbook at
  EOF; the body closure threaded from the launcher.
- **The save decision** — the `hadFollowup` branch (persist-as-is vs re-author), shared by
  the verify-success offer and manual `w`.
- **The not-verified confirm** — the Path-2 confirm dialog before saving an unverified
  run (reuse the confirm overlay).
- **The collapse** — remove the always-regenerate `w` branch + the `persistOnFinish`
  auto-baseline.

## Testing

- **`hadFollowup`:** set by an auto verify-fail and by a manual follow-up; reset after a
  re-author.
- **Save decision:** `!hadFollowup` → `commitPlaybookCmd` with the current doc, NO
  re-author; `hadFollowup` → triggers the structured re-author (a `submit_playbook` →
  `sess.lastPB` render), then a subsequent save persists.
- **Structured re-engagement:** a re-engagement with `Structured:true` captures a
  `submit_playbook` into `sess.lastPB`; the in-viewer re-arm renders
  `playbook.Render(sess.lastPB)` at EOF (not the streamed markdown).
- **Verify-success offer:** exit-0 verify offers to save; accept → the save decision.
- **`w` not-verified:** before the verify succeeds, `w` raises the confirm dialog;
  confirm → the save decision; cancel → no save.
- **Followup stays markdown:** a fix-didn't-work follow-up does NOT call `submit_playbook`
  / does not go structured.

## Out of scope

The assisted-run feature (Phase-2 run modes — the B2b gate is ready for it to reuse); the
viewer-UX-polish backlog; the `file=`/diff spec. After B3, Phase B (the structured
playbook migration) is complete.
