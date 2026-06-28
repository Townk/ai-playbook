# `create` — inline progress, then viewer (no live-stream takeover)

Status: agreed (2026-06-28). Revises the Phase-1 `create` behavior.

## Problem

Today `create <prompt>` reuses the assist/escalate authoring path
(`realCreateAuthor` → `authorPlaybook` → `ui.RunStream`), which **streams the
authoring live into the fullscreen viewer**. The user does not want `create` to
take over the screen while the model works. This applies **universally** —
regardless of whether a multiplexer is configured.

## Desired flow (mux OR no-mux — identical)

`create "<prompt>"`:

1. Capture context + emit the existing "similar playbooks already exist…" banner.
2. **Inline progress while authoring** — render the viewer-style indicator
   **below the shell prompt** (non-alt-screen, on `/dev/tty`): the spinner +
   `Waiting…` (tiered phrases) + elapsed seconds, with the **model-activity line**
   beneath it. Do NOT render the building playbook; do NOT take over the screen.
   Drain the authoring stream to collect the complete playbook body.
3. **Agent `ask` during authoring is fully supported** (the authoring agent has
   the `ask` tool, `internal/author/mcp.go`):
   - **mux present** → the input **float** (the existing `floatinput.Asker`).
   - **no mux** → **pause the progress line and show the inline ask box** (the
     embeddable `input.Ask`, same UI as assist's no-mux ask), get the answer,
     then **resume** the progress line. Routed via the existing `askbridge`.
4. On stream completion: **persist** the playbook (store + cache, unchanged from
   today's `authorPlaybook` commit path).
5. **Open the fullscreen viewer/runner with the COMPLETE playbook** (no
   token-by-token streaming) so the user can review and drive it. Asks during the
   *driving* phase are handled by the viewer as today. Reuse the authoring
   session's driver so the run blocks execute in the same shell the agent used.

## Components

- **Progress+ask host (new):** extend the launcher's `waitingModel`
  (`internal/launcher/inline_input.go`) — which already renders `ui.WaitingLine`
  + activity inline — to also subscribe to the `askbridge` request channel and,
  on a request, embed `input.Ask` (pausing the wait), respond via the bridge, and
  resume. mux-present asks use the float and never reach the bridge.
- **`create` author path (changed):** a new `createAuthorWithProgress` replaces
  the `ui.RunStream` call in `realCreateAuthor`. It opens the session (driver +
  tools backend), runs `author.AuthorEvents` → `agentstream.FanOut`
  (reader/activity/fo), drives the progress+ask host while draining the reader,
  and returns `fo.Body()` on completion.
- **Persist + view (reuse):** `CommitPlaybook(body)` (store + cache), then write
  the body to a temp file and open `ui.Main` with the authoring session's driver
  (via `ui.SetDriver`, like `serveCachedPlaybook`) — the viewer renders the
  complete playbook and drives its blocks.

## Invariants (unchanged)

- `create` still does NO triage, NO cache-hit serve, NO cached/regenerate badge.
- The cache keys / store dir / disable-guard from `createDecision` are unchanged.
- `assist` / escalate keep their current live-stream-into-viewer behavior — this
  spec changes `create` only.

## Testing

- Progress host: activity updates the line; a done signal quits; an ask request
  embeds `input.Ask`, the answer is sent to the bridge, then the wait resumes.
- create core: authoring is drained to a body, `CommitPlaybook` is called, the
  viewer is invoked with the complete body (seams for the author stream + viewer,
  no live harness/TTY) — and triage/classify are never consulted.
- No-mux ask round-trip during authoring (bridge → inline ask → bridge response).
