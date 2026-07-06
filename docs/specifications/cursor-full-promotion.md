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

---

## Phase C — reopener: does a config-DIR REDIRECT isolate cleanly? (run on the laptop)

_Added 2026-07-06. Phase B tested a project `.cursor/mcp.json` in a controlled
WORKSPACE and found it MERGES with the global config. It did NOT test
redirecting cursor-agent's config-resolution ROOT (an env var like
`CURSOR_CONFIG_DIR` / `CURSOR_HOME`, or a `HOME` override) so cursor reads a
PRISTINE config dir we control and never sees `~/.cursor/mcp.json` at all.
If a redirect isolates cleanly AND preserves auth, the isolation blocker is
solved structurally and FULL is back on the table. This phase is mostly
DETERMINISTIC (`cursor-agent mcp list` is the oracle — no LLM calls) except
the final schema probe. NOTE from Phase A: the system-prompt fold trips the
model's prompt-injection refusal on canned "reply exactly X" prompts — keep
any LLM probe benign and natural._

### The safety invariant still governs

Same as Phase B: FULL ships only if the authoring-session model sees OUR tools
and NOT the user's global MCP servers. A redirect that isolates config but is
fragile (silently falls back to global on any misconfig) is only acceptable
if ai-playbook ENFORCES the isolation at runtime (Step 5). "Stay BASIC" remains
a valid, successful outcome.

### Step 0 — discover whether a redirect mechanism exists (free)

- Re-scan the CLI surface for a config-root override: `cursor-agent --help`,
  `cursor-agent mcp --help`, `cursor-agent --version`; grep for `config`,
  `home`, `dir`, `CURSOR_`. Check the docs (cursor.com/docs/cli) for any
  `CURSOR_CONFIG_DIR`/`CURSOR_HOME`/`XDG_CONFIG_HOME` mention.
- Determine WHERE cursor-agent actually reads `mcp.json` from, empirically. A
  non-privileged way: create a temp dir, seed a DECOY config, and point each
  candidate at it (Step 1). A deeper way if needed: file-access trace while it
  starts — `sudo fs_usage -w -f pathname cursor-agent 2>&1 | grep -i mcp`
  (macOS) or `dtruss -f -t open_nocancel cursor-agent ... 2>&1 | grep -i
  cursor` — to see the exact path it opens for mcp.json and whether an env var
  moves it.

### Step 1 — the DETERMINISTIC isolation oracle (free — no LLM)

`cursor-agent mcp list` reports the loaded servers + approval state (Phase B
used it). Use it as the isolation test:

1. Build a pristine redirect root `$ISO` (fresh temp dir) with a config that
   declares exactly ONE recognizable decoy server, e.g.
   `apb_iso_probe` → a trivial stdio command (even `sh -c 'cat'` is enough to
   be listed; it only needs to be enumerable). Try BOTH layouts the discovery
   suggests: `$ISO/.cursor/mcp.json` and `$ISO/mcp.json`.
2. Leave the REAL global `~/.cursor/mcp.json` populated with its normal servers
   (atlassian/zellij/context7) — those are the decoys for the leak test.
3. For each candidate redirect, run `mcp list` UNDER it and record the servers:
   - `CURSOR_CONFIG_DIR="$ISO" cursor-agent mcp list`
   - `CURSOR_HOME="$ISO" cursor-agent mcp list`
   - `XDG_CONFIG_HOME="$ISO" cursor-agent mcp list`
   - `HOME="$ISO" cursor-agent mcp list`  (blunt override — see Step 2 caveat)
4. **Read the result:**
   - `apb_iso_probe` ONLY, no atlassian/zellij/context7 → **ISOLATION WORKS**
     for that mechanism. Record which env var + which config layout.
   - both the decoy AND the global servers → merge (like the workspace route);
     that mechanism does not isolate.
   - only the global servers (decoy absent) → the redirect is not honored.

### Step 2 — auth survival under the redirect (the critical gotcha, free)

A `HOME` override isolates config but may ALSO relocate cursor-agent's auth
token → the run loses authentication and can't reach the model. A TARGETED
config-only env var (if one exists) isolates mcp.json while auth stays put.

- For each mechanism that ISOLATED in Step 1, run a trivial benign real prompt
  under it: `<REDIRECT> cursor-agent -p --output-format stream-json --mode ask
  --trust "In one sentence, what is a playbook?"` and confirm a real answer
  streams back (auth survived) vs an auth/login error (auth broke).
- If a `HOME` override isolates but breaks auth: find where cursor stores auth
  (keychain vs `~/.cursor/<file>`). If it's a file, test whether
  symlinking/copying just that auth artifact into `$ISO/.cursor/` restores auth
  while keeping mcp.json isolated — viable but fragile; prefer a config-only
  env var if one exists. If auth is in the macOS keychain, a HOME override
  keeps auth automatically → best case.

### Step 3 — approval is safe once isolated (free)

Phase B's blanket-approval objection only bites when foreign servers are
present. Under a mechanism that isolates (Step 1) with NO global servers
visible:

- `<REDIRECT> cursor-agent mcp list` → confirm only `apb_iso_probe`, shown as
  needs-approval.
- Run once with `--approve-mcps` under the redirect, then `mcp list` again →
  confirm ONLY `apb_iso_probe` is now approved and NO global server appears.
  This proves `--approve-mcps` is safe when the config is genuinely isolated
  (there is nothing else to approve). Confirm it did NOT mutate the real
  `~/.cursor/` approved list (diff it before/after).

### Step 4 — schema enforcement (the second FULL gate — ONE LLM call)

Only if Steps 1-3 pass. Register a real probe tool (reuse ai-playbook's
`internal/tools` socket server, or a tiny standalone MCP stdio server) exposing
ONE tool with a STRICT input schema (e.g. required integer `port`). Via the
isolated config, prompt the model naturally to call it in a way that would
produce a WRONG-typed/missing argument, and observe:

- cursor-agent REJECTS the malformed call and RE-ASKS the model → schema
  enforced → **structured `submit_playbook` drafting is viable** → full FULL.
- cursor-agent passes the bad args to the server unvalidated → no enforcement
  → ship FULL for `run`/`ask`/`remember`, but structured drafting stays the
  text path → **partial FULL**.

### Step 5 — the runtime guard (build requirement, whichever way it lands)

Whatever mechanism works, `cursorHarness.ToolTransport` MUST verify isolation
at RUN TIME before enabling tools — never assume the redirect held. Concretely:
write the isolated config, then confirm (e.g. via a `cursor-agent mcp list`
check under the same redirect, or by construction of a guaranteed-pristine
`$ISO`) that ONLY our server is present; if ANY foreign server appears, DEGRADE
to BASIC + the once-per-session note ("cursor tools disabled — could not
isolate MCP config"). Defense in depth on a machine with destructive global
tools in scope.

### Decision matrix

- Redirect isolates (Step 1) + auth survives (Step 2) + approval safe (Step 3):
  - schema enforced (Step 4) → **promote to full FULL** (transport via the
    redirect + the Step-5 guard; wire all four tools; live schema test).
  - schema NOT enforced → **partial FULL** (run/ask/remember; structured
    drafting stays text).
- Redirect isolates but breaks auth with no clean fix → not viable; fall back
  to the enforced-empty-global-config guard (check `~/.cursor/mcp.json` is
  empty/only-ours at run time, else BASIC) — acceptable ONLY with the Step-5
  guard, since it depends on the user's config staying clean.
- No redirect honored + merge everywhere → cursor STAYS BASIC (Phase B stands);
  the gap is upstream (cursor needs `--strict-mcp-config`).

### Deliverable

A `docs`/`feat` commit recording the Step-1..4 evidence (which env var, which
layout, auth outcome, approval outcome, schema outcome) and EITHER the FULL/
partial-FULL implementation (transport + Step-5 guard + caps flip + live test +
spec/CHANGELOG + backlog line removed) OR the narrowed "stay BASIC" record with
the specific mechanism failures. Keep LLM probes to Step 4 only; Steps 0-3 are
free/deterministic.
