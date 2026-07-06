# Cursor harness: BASIC→FULL promotion brief (run on a machine with `cursor-agent`)

_Status: actionable brief, 2026-07-06. Cursor shipped BASIC in v0.13
(ADR-0012, `docs/specifications/multi-harness.md`) because its adapter was
built fixture-first from documentation on a machine WITHOUT the CLI, and
because an isolation-unsafe MCP transport is worse than none. This brief is
for an agent on a machine that HAS `cursor-agent` installed and
authenticated. Work the two phases in order; Phase A is valuable and
committable on its own even if Phase B concludes "stay BASIC"._

## Ground rules (same as every batch in this repo — see CLAUDE.md)

- gpg-signed Conventional Commits; NO AI-attribution trailers; `git add` by
  explicit path; never commit `.superpowers/`. If gpg signing STALLS, STOP
  and report BLOCKED — never `--no-gpg-sign`.
- `make check` green before every commit (it now runs harness live tests
  wherever the CLI exists — on this machine that includes the cursor live
  tests below, and pi's if pi is also installed; each bills a few tiny
  calls).
- Follow the SDD loop: implement → adversarial self-review against named
  risks → fix → commit. Prefer small, reviewable commits (Phase A and
  Phase B are separate commits at least).
- The reference shapes are the two shipped FULL adapters: the claude harness
  (`internal/author/harness_claude.go`, `internal/agentstream/claude.go`,
  its MCP `ToolTransport` writing an `--mcp-config` JSON) and the pi harness
  (`internal/author/harness_pi.go`, `internal/agentstream/pi.go`, its
  embedded-extension `ToolTransport`). Cursor's current files:
  `internal/author/harness_cursor.go` (`cursorHarness`, `cursorBin`,
  `Capabilities{Tools:false}`, a stubbed `ToolTransport`, `cursorFoldPrompt`)
  and `internal/agentstream/cursor.go` (`cursorAdapter`; Final = last
  non-empty assistant segment; `result` = terminal marker only).

## THE SAFETY INVARIANT (non-negotiable — read before Phase B)

Promote to FULL **only if** our MCP tools can be attached to a cursor-agent
invocation in ISOLATION: the model must see OUR `run`/`ask`/`remember`/
`submit_playbook` tools and MUST NOT silently gain the user's globally
configured MCP servers (`~/.cursor/mcp.json`) in the same authoring session,
nor have them blanket-approved on our behalf. A FULL tier that leaks or
auto-approves the user's servers into our headless authoring run is a
security regression and is WORSE than the current BASIC tier. If isolation
cannot be established, the correct outcome is **stay BASIC** with the
findings recorded — that is a successful completion of this brief, not a
failure.

---

## Phase A — verify the BASIC adapter against the real CLI

The current cursor adapter is doc-derived (fixtures constructed from
cursor.com docs, not captured). Confirm reality matches, and replace the
doc-derived fixtures with real captures.

1. **Version + argv composition.** Record `cursor-agent --version`. Confirm
   the owned argv composes and runs non-interactively:
   `cursor-agent -p --output-format stream-json --stream-partial-output
   --mode ask "reply with exactly: ok"`. Capture the raw NDJSON.
2. **Run the gated live tests** — they SKIP today and should now RUN:
   `go test ./internal/author/ -run TestCursorLive -v`
   (`harness_cursor_live_test.go`: `TestCursorLive_BareFinalEvent`,
   `TestCursorLive_AppendFinalEvent`, `TestCursorLive_AuthoringShapedFinal`).
   The authoring-shaped test is the load-bearing one: it forces a real
   ask-mode read and asserts `Final` is the final answer with no narration
   glued on. If any fail, the doc-derived assumptions were wrong — diagnose
   against the captured stream.
3. **Resolve the H3-review open questions with real evidence:**
   - Does `--mode ask` CLAMP generation? (The concern: ask mode is
     read-only; does it decline or truncate long-form markdown authoring
     output?) The authoring-shaped test answers this — confirm a
     multi-paragraph playbook body comes back intact.
   - Is the `result` envelope really the no-separator concatenation of all
     assistant segments? Confirm the adapter's "trust segments, not
     `result.text`" policy (`cursor.go`) is correct against a real
     multi-segment turn (one with at least one tool_call between text runs).
   - Terminal envelope, `tool_call` subtype names (`started`/`completed`),
     and the assistant-delta dedup rule (`timestamp_ms`/`model_call_id`
     field presence) — verify each against the capture; fix `cursorAdapter`
     if the real wire differs.
4. **Replace fixtures.** Swap the doc-derived `testdata` NDJSON for real
   captures (label them as captured, note the CLI version). Keep the strict
   discipline (garbage line → error, missing terminal → error).
5. **Commit Phase A**: `test(agentstream): verify the cursor adapter against
   the real CLI` (or `fix(...)` if the parser needed correcting). Record
   every divergence found in the commit body.

## Phase B — attempt the FULL promotion

Only after Phase A confirms the BASIC adapter is faithful. Three
investigations gate the promotion; ALL must resolve safely.

1. **Isolated transport.** Test whether cursor-agent honors a project-local
   `.cursor/mcp.json` in a working directory WE control (the candidate:
   run with `cwd` = a fresh temp dir, or `--workspace <temp dir>` if that
   flag exists, holding our own `.cursor/mcp.json` that points at
   `ai-playbook mcp --socket <path>` the way claude's mcp-config does).
   CRITICAL probe: with a temp-dir project config in place AND a decoy
   server in `~/.cursor/mcp.json`, does the model see ONLY our server, or
   both? If both load, look for a strict/isolation flag (there was no
   `--strict-mcp-config` analog in the docs — verify on the real CLI:
   `cursor-agent --help`, `cursor-agent mcp --help` if it exists). No
   isolation ⇒ STOP, stay BASIC, record it.
2. **Schema-enforcing tool loop.** FULL requires the harness to validate a
   tool call's arguments against the tool's input schema and RE-ASK the
   model on a mismatch (this is what makes `submit_playbook` structured
   output reliable — see how pi/claude do it). Probe: register a tool with
   a strict input schema via the transport, prompt the model to call it
   with deliberately wrong arguments, and observe whether cursor-agent
   rejects+re-asks or passes the bad args through. No enforcement ⇒ the
   `submit_playbook` structured path is unsafe ⇒ stay BASIC (or ship FULL
   for `run`/`ask`/`remember` only IF you can prove those degrade safely —
   but the simplest correct outcome is BASIC).
3. **Headless MCP approval.** cursor-agent may gate MCP tool use behind an
   approval prompt. Find how it behaves headless (`-p`): is there a scoped
   per-server approval, or only `--approve-mcps` which blanket-approves
   EVERY configured server (including the user's)? Blanket approval of the
   user's servers violates the safety invariant. A scoped approval limited
   to our server is acceptable.

**If all three resolve safely:**
- Factor the shared `mcpServers`-document writer out of the claude harness
  (the spec names this: claude and cursor emit the same JSON shape, differ
  only in the attach argv) — keep claude's argv goldens byte-identical
  across the factoring.
- Implement `cursorHarness.ToolTransport` (write the isolated
  `.cursor/mcp.json` + return the attach argv/flags), flip
  `Capabilities()` to `{Tools:true}`, and wire the four tools
  (`run`/`ask`/`remember`/`submit_playbook`) — the tools backend
  (`internal/tools`) and the surface (`internal/mcpserver`) already exist;
  cursor reuses the same socket wire.
- Add a live schema-enforcement test (gated by `RequireHarness`) mirroring
  pi's `TestPiLive_ToolLoopSubmitPlaybook`: real cursor + real
  `tools.Server` + a `submit_playbook` round-trip landing a
  `draft.Validate`-clean playbook.
- Update `docs/specifications/multi-harness.md` (cursor section: BASIC→FULL,
  the transport that worked), the CHANGELOG, and remove the promotion
  BACKLOG line. Commit: `feat(author): promote the cursor harness to FULL`.

**If any blocker remains:** update the cursor section of the spec + this
brief with the SPECIFIC evidence (which probe failed, the CLI version,
verbatim output), refine the BACKLOG line to the narrowed remaining gap,
and commit the Phase-A verification alone. That is a complete, valuable
outcome — the adapter is now real-CLI-verified even if it stays BASIC.

## Deliverables checklist

- [x] Phase A: live tests run (not skip) and pass; fixtures are real
      captures; H3 open questions answered with evidence; committed.
- [x] Phase B: the gate probes documented with real output (below).
- [x] BASIC retained with the precise blocker recorded (spec + brief +
      narrowed backlog line). FULL not shipped — isolation is unattainable.
- [x] `make check` green for the cursor work (build/vet/lint/fmt + full
      suite; only the pi live tests fail, on missing pi provider auth —
      environmental, unrelated to cursor); commits gpg-signed, no trailers.

---

## OUTCOME (2026-07-06, run on the work-laptop; cursor-agent 2026.07.01-777f564)

**Phase A — DONE (committed `fix(agentstream): verify the cursor adapter
against the real CLI`).** The doc-derived adapter was wrong in several
load-bearing ways; all corrected and re-verified live:

- `--trust` is REQUIRED (cursor-agent refuses to start in a not-yet-trusted
  dir; the flag is ephemeral — `~/.cursor` state byte-identical before/after —
  and does not lift command gates, unlike `--force`/`--yolo`). The old "NO
  --trust" rationale was wrong.
- `tool_call` `started` carries the tool-named wrapper BESIDE sibling metadata
  (`toolCallId`/`startedAtMs`/`hookAdditionalContexts`); the old
  `map[string]cursorToolCallBody` aborted the line on the first real tool use.
- `thinking` events DO stream (top-level `text`); now surfaced as Reasoning.
- `result` is confirmed the no-separator concat of all assistant segments →
  the last-segment Final policy is correct.
- `--mode ask` does NOT clamp authoring (multi-paragraph markdown returns
  intact; headless reads work).
- The system-prompt fold intermittently trips the model's prompt-injection
  refusal on canned "always reply exactly X" probes → live probe made benign.
- Fixtures replaced with raw live captures.

**Phase B — STAY BASIC (isolation is unattainable).** Decisive probe:

- **Isolation FAILED.** Temp workspace with our own project `.cursor/mcp.json`
  + the user's four global servers in `~/.cursor/mcp.json`. A headless
  `cursor-agent -p --output-format stream-json --stream-partial-output --mode
  ask --trust` run from that workspace, asked to enumerate its MCP tools,
  returned the user's global servers in full: 31 `atlassian` tools (Jira/
  Confluence create/edit/delete), 85+ `zellij` tools
  (`kill_all_sessions`/`exec_in_pane`/`run_command`), `context7`. Project
  config MERGES with global; there is NO `--strict-mcp-config` analog
  (`cursor-agent --help`, `cursor-agent mcp --help`). `--workspace` does not
  change it.
- **Approval is blanket-only.** Our own server showed `not loaded (needs
  approval)` in `cursor-agent mcp list`; the sole headless approval flag is
  `--approve-mcps` ("Automatically approve all MCP servers"), which
  blanket-approves the user's servers too. `mcp enable/disable` mutates the
  user's durable approved list, not a per-invocation scope.
- **Schema enforcement not probed** — the isolation failure alone disqualifies
  promotion (a leaky FULL is worse than BASIC), and no attach path is safe to
  build on.

Conclusion: there is no way to attach OUR `run`/`ask`/`remember`/
`submit_playbook` tools to a cursor-agent authoring session WITHOUT the model
also gaining the user's globally-configured MCP servers — an authoring-session
privilege escalation. Per the safety invariant, cursor STAYS BASIC. The
narrowed remaining gap: cursor-agent needs a `--strict-mcp-config`-equivalent
(load only a specified MCP config, ignore global) AND a scoped headless
approval. Revisit if a future cursor-agent adds either.
