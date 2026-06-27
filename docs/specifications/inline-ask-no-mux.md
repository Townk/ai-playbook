# Inline UX without a multiplexer (completing ADR-0006 Stage 1)

Status: agreed (2026-06-27). Completes the "mux-optional" goal of
[ADR-0006](../architecture/adrs/0006-progressive-enhancement-portable-core.md).

## Problem

ADR-0006 Stage 1 made the multiplexer structurally optional, but the off-mux UX
is a stopgap: the request is read from plain stdin and the agent `ask` tool is
"unavailable" inline. That guts the assist flow. This spec defines the real
no-mux UX. **The mux-present path is unchanged** (zellij float + docked panes as
today). We re-host the *same* components — we do not redesign them.

## Phase 1 — Input (`assist`, no mux)

Render the input UI **inline, directly below the shell prompt** (not a float):
the three existing float elements — **description line**, **bordered input box**,
**hint text** — in the same layout as today. Identical whether the user typed
`ai-playbook assist` at the CLI or invoked it from the ZLE widget.

- On ENTER: the same in-box **sine-wave animation** + classification run inline.
- **COMMAND** → return the command string to the ZLE widget (fills the command
  line), or print it to stdout otherwise.
- **ANSWER / ESCALATE** → clear the inline region and hand off to Phase 2.

**Explicit request** (a CLI arg, or text the ZLE widget passes): **skip the input
box** entirely and go straight to classify — but show progress using the
**viewer-style indicator** ("Waiting…" with the tiered phrases) plus the
**model-activity line** right below it. (Do NOT use the sine-wave here — too heavy
for this path.)

After a COMMAND result the inline UI clears so the terminal is left tidy.

## Phase 2 — Viewer (`answer`/`escalate`, no mux)

The playbook viewer/runner takes over **fullscreen**. Any dialog the agent raises
(the `ask` tool) renders as an **overlay modal on the viewer, using the same
mechanism as the help modal**. The dialog UI itself is byte-for-byte today's
dialog — only its host changes (overlay instead of zellij float).

## Reuse map

| Component | Mux present (today) | No mux (this spec) |
| --- | --- | --- |
| Input (desc/box/hint) | zellij float | inline, below the prompt |
| Classify progress (explicit req) | — | viewer "Waiting…" + model-activity line |
| Agent `ask` dialog | zellij float | overlay modal (help-modal mechanism) |
| Viewer / runner | fullscreen | fullscreen (unchanged) |

## Supersedes

- The plain-stdin request read and the "ask unavailable" stopgap from the Stage-1
  inline-fallback task.

## Out of scope (backlog)

- Flipping the default to no-mux; the 2-tier integration config (named preset +
  per-command overrides) for mux/shell/AI.
