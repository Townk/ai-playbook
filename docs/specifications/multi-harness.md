# Multi-harness support (pi, cursor)

_Status: approved 2026-07-06 (design settled with the project owner; decision
record: ADR-0012). The v0.13 milestone — the last feature milestone before
1.0._

## Problem

Only a Claude harness exists. The seam is real but incomplete: the tool
transport (MCP-config writing + `--mcp-config`) is wired by the launcher
outside the `Harness` interface, four Claude-specific leaks sit in
harness-agnostic code, and nothing defines which capabilities a harness must
have versus which degrade.

## Decisions (ADR-0012)

Two capability tiers (FULL/BASIC) with degrade+note; the tool transport moves
behind the seam; pi + cursor adapters ship; flat config keys with per-harness
defaults; leak cleanup; one-shot invocation codified.

## The contract

A harness is an installed CLI the user has already authenticated. The
`Harness` interface (internal/author/harness.go) grows to:

- `Argv(systemPrompt, userMessage, inv) []string` — unchanged.
- `Env(inv) []string` — unchanged.
- `AdapterName() string` — unchanged (keys the agentstream registry).
- `DisplayName() string` — NEW: the human label ("Claude Code", "pi",
  "Cursor") used by the streaming UI header and error strings.
- `Capabilities() Capabilities` — NEW: `{Tools bool}` (schema-enforced
  tool loop + a transport to our socket backend). Streaming a final answer
  is required of every harness, so it is not a flag.
- `ToolTransport(inv, socketPath, dir) (files []WrittenFile, argv []string,
  err error)` — NEW: writes the harness's transport artifact(s) into `dir`
  (Claude: the mcp-config JSON; pi: the embedded extension file; cursor: its
  MCP config) and returns the argv additions that attach them
  (`--mcp-config <path>` / `--extension <path>` …). Called only when
  `Capabilities().Tools` and the caller wants tools; the launcher never
  writes transport artifacts itself again.

Required of EVERY harness (the BASIC floor):

- Non-interactive one-shot invocation: fresh process, prompt in argv,
  exit after the answer. No session state is ever used (pi: `--no-session`).
- A system-prompt override AND an append mode (or the adapter documents the
  closest equivalent) — the bare/quick path needs "replace", authoring
  needs "append".
- A parseable stdout protocol the harness's `agentstream` adapter converts
  into the four normalized events; at minimum a terminal event carrying the
  full final text. Adapters keep the strict-stream discipline (A5b):
  garbage lines are errors, truncated streams are errors.

FULL adds: a tool loop that presents our tools (from the transport), enforces
their JSON schemas (re-asking the model on validation failure), and returns
tool results to the model — which is what `submit_playbook` (structured
output), `run`, `ask`, and `remember` ride on.

## Tier behavior matrix

| Surface | FULL | BASIC |
|---|---|---|
| Authoring (create) | structured via `submit_playbook` | text path (free-text markdown), note once: "structured drafting unavailable on <harness> — using text mode" |
| Regenerate / final-playbook / wrap-up | structured | text path, same note class |
| Followup, drift-regen | unchanged (free text) | unchanged |
| `run` tool (agent probes), `ask` dialogs | available | absent — prompts must not mention them (the tool instruction fold already gates on MCP wiring; it gates on `Capabilities().Tools` now) |
| remember / KB fill | available | skipped, note once: "knowledge capture unavailable on <harness>" |
| KB recall (prompt fold) | unchanged | unchanged (read-side needs no tools) |
| classify / metadata / compaction / validate AI review | unchanged | unchanged (bare one-shots, no tools) |
| Refuse-solution constraints | unchanged | unchanged |

Notes are stderr/status-line one-liners, once per session, tested verbatim.

## The adapters

### pi (live-characterized against 0.80.3)

- Invocation: `pi -p --mode json` + `--append-system-prompt <sys>` (authoring)
  or `--system-prompt <sys>` (bare) + `--no-session`; bare additionally
  passes `--no-context-files --no-extensions --no-skills --no-prompt-templates`
  (the analog of Claude's `--exclude-dynamic-system-prompt-sections`).
- Thinking: native `--thinking off|minimal|low|medium|high|xhigh` — the
  config levels map directly; no env var.
- Model: `--model <pattern>` (supports `provider/id`); per-harness defaults
  chosen during characterization (T2 records them).
- Stream: `--mode json` NDJSON; the adapter task characterizes the envelope
  kinds live and maps them to the four events (reasoning arrives as real text
  in `thinking_delta` events — live-characterized). Strict-stream rules apply.
- Tools: a pi EXTENSION (TypeScript, embedded via go:embed, written by
  `ToolTransport` into the private per-invocation transport dir the shared
  launcher helper creates — the same system-temp dir claude's mcp-config
  uses, removed when the stream closes) registering `run`/`ask`/`remember`/
  `submit_playbook`, each forwarding to the unix-socket backend
  (`tools.Dial` wire). Attached via `--extension <path>`; discovery stays
  disabled so ONLY our extension loads. Tier: FULL if pi's tool loop
  enforces input schemas (characterized in the adapter task; if it does
  not, pi ships BASIC and the extension work is deferred — the task
  records the finding either way).

### cursor (live-verified against cursor-agent 2026.07.01-777f564; FULL via a HOME-redirect MCP isolation + a preToolUse builtin-containment hook)

- Invocation: `cursor-agent -p --output-format stream-json
  --stream-partial-output [--mode ask] --trust` (live-verified; rationale in
  harness_cursor.go). `--mode ask` is Cursor's read-only mode — print mode
  can otherwise use write/shell tools, an unsanctioned mutation channel for a
  text-producing invocation (the hazard pi closes with `--no-tools`) — so it
  rides the TOOL-LESS paths (text authoring, classify, followup, review). The
  FULL tool path DROPS `--mode ask`: cursor-agent REFUSES MCP tool calls in ask
  mode ("I'm in Ask mode, which restricts me to read-only actions" —
  live-verified), so the run/ask/remember/submit_playbook tools would never
  dispatch; default (agent) mode is required. Agent mode also exposes cursor's
  builtin write/shell tools, which the FULL transport neutralizes with a
  preToolUse allowlist hook (the builtin-containment gate below), so the
  read-only posture is ENFORCED, not merely prompted.
  `--trust` is REQUIRED: cursor-agent refuses to start in a not-yet-trusted
  directory, and the flag is ephemeral (writes no durable `~/.cursor` state)
  and narrow (unlike `--force`/`--yolo` it does NOT lift the per-command
  permission gates). The CLI installs as `agent`/`cursor-agent` (never
  `cursor`), so the defaults table grew a per-harness `Bin` column;
  `cursor-agent` (the every-vintage symlink) is the default.
- System prompt: cursor-agent has NEITHER replace nor append flags, and no
  context-suppression flags either (.cursor/rules, AGENTS.md, CLAUDE.md
  auto-load unconditionally) — so BOTH paths use the documented fallback:
  the system prompt is folded into the head of the single positional user
  message, and bare == append in shape.
- Tools: cursor is FULL (`Capabilities{Tools:true}`). Phase B found the naive
  attach unsafe; Phase C (cursor-agent 2026.07.01-777f564) found the
  STRUCTURAL fix — a per-invocation **HOME redirect** — and proved every gate
  of the safety invariant. The transport (`ToolTransport`, harness_cursor.go)
  writes `<dir>/.cursor/mcp.json` holding ONLY our server, plants a preToolUse
  builtin-containment hook (Step 6), and sets `HOME=<dir>` via `Env`, so
  cursor-agent resolves its global MCP config from the pristine root we control.
  Probe record:
  - **Mechanism (Step 0).** cursor-agent resolves the global MCP config from
    `os.homedir()/.cursor/mcp.json`. There is NO config-root env var —
    `CURSOR_CONFIG_DIR`, `CURSOR_HOME`, and `XDG_CONFIG_HOME` are all no-ops
    (verified); only a `HOME` override moves the resolution root.
  - **Isolation (Step 1) — PASSED.** With the user's four global servers in
    the real `~/.cursor/mcp.json` (context7, zellij, glean, atlassian) and a
    decoy server in `<dir>/.cursor/mcp.json`, `cursor-agent mcp list` under
    `HOME=<dir>` reports ONLY the decoy — the four global servers do not load.
    (Phase B's failure was the merge of a PROJECT `.cursor/mcp.json` with the
    global one; the HOME redirect moves the GLOBAL root itself, so there is
    nothing to merge with.)
  - **Auth (Step 2) — PASSED (darwin).** A bare HOME override loses auth (the
    macOS keychain is HOME-relative, `$HOME/Library/Keychains`). The transport
    symlinks the REAL keychain dir into `<dir>/Library/Keychains`, restoring
    login while the MCP config stays isolated; `cursor-agent status` under the
    redirect then reports "Logged in". The symlink exposes nothing cursor-agent
    lacks in BASIC (it already reads this keychain to authenticate).
  - **Approval (Step 3) — safe.** `--approve-mcps` under the redirect approves
    ONLY our server (nothing else is configured). The user's durable
    `~/.cursor` state and `~/.config/cursor/cli-config.json` approvals were
    byte-diffed before/after and did not change (only a benign `updatedAt`).
  - **Schema enforcement (Step 4) — PASSED.** cursor-agent passes each tool's
    `inputSchema` to the model AND relays a tool's `isError` result back,
    driving an automatic re-ask: an even-port-only probe server rejected an
    odd port and the model retried with an even one — the schema-enforced loop
    submit_playbook/run/ask/remember ride on.
  - **Runtime guard (Step 5) — mandatory.** `ToolTransport` never trusts the
    redirect held: when a bin is known it runs `cursor-agent mcp list` +
    `status` under `HOME=<dir>` and REFUSES to enable tools (→ BASIC, with the
    once-per-session note) if any foreign server is visible or auth was lost.
    The `status` check requires POSITIVE auth ("logged in as") and rejects a
    "not logged in" substring — the naive "logged in" contains-check fails OPEN
    ("not logged in" contains it); `parseCursorMCPList`/`cursorForeignServer`/
    `cursorStatusAuthenticated` are unit-tested against real `mcp list`/`status`
    captures (testdata/cursor/), including a foreign-server leak (guard FAILS),
    an only-ours isolation (PASSES), and multi-colon/colon-less lines (a foreign
    server on a `name: Error: ...` line is never missed).
  - **Builtin containment (Step 6) — the decisive safety gate.** Because FULL
    must run in agent mode (ask/plan refuse MCP tools), cursor's builtin
    write/shell tools are in scope, and they EXECUTE headlessly under `-p` with
    no per-command gate and no `cmd.Dir` — a would-be mutation of the user's real
    project. Probe evidence (cursor-agent 2026.07.01-777f564): a FULL-shape run
    (agent mode, `--trust`, `--approve-mcps`, HOME redirected, no `--force`/
    `--yolo`) asked to write a file AND run `touch` did BOTH (`editToolCall`/
    `shellToolCall` → success). cursor has NO builtin-off flag, and
    `permissions.deny` in `cli-config.json` is NOT honored headlessly (retested
    under `--force`, `--auto-review`, `--sandbox enabled` — builtins still ran).
    The working mechanism is a **preToolUse hook** (cursor CLI supports it; only
    `deny`/exit-2 is reliably enforced): the transport plants
    `<dir>/.cursor/hooks.json` + `cursor_pretool_hook.sh` (`failClosed:true`)
    that permits ONLY `MCP:<tool>` and DENIES every builtin — the cursor analog
    of pi's `--no-builtin-tools`. Re-probed with the hook: the same write/shell
    prompt is REJECTED (`result_keys=["rejected"]`) while the MCP call still
    succeeds. `tool_name` precedes `tool_input` on the wire, so the first-match
    extraction cannot be spoofed by a fake name in a tool argument.
    RequireHarness-gated live proof: `TestCursorLive_ToolHookBlocksBuiltins`
    asserts a builtin-mutation prompt creates no file through the production
    transport.
  The shared `mcpServers` writer (`mcpconfig.go`) is now used by BOTH FULL
  harnesses (claude's `--mcp-config`, cursor's redirected `.cursor/mcp.json`).
- Authoring-context asymmetry (LOW, benign — documented). On the FULL tool path
  the HOME redirect makes cursor-agent resolve config from the pristine `<dir>`,
  so the user's global `~/.cursor/rules` and `~/.gitconfig` are ABSENT — whereas
  the tool-less paths (text authoring, classify, followup, review) run under the
  user's real HOME and DO see them. Authoring guidance travels in our folded
  system prompt (not `~/.cursor/rules`), so this affects only ambient global
  rules/git identity the model would otherwise incidentally observe; it does not
  affect playbook correctness. Called out so the asymmetry is a known, accepted
  property rather than a surprise.
- Stream: `result` is the terminal envelope (REQUIRED — A5b); assistant
  deltas are deduped per the live-verified `--stream-partial-output`
  three-variant rule (delta = `timestamp_ms` without `model_call_id`;
  buffered pre-tool flush = both; end-of-turn flush = neither). Thinking
  events DO stream (subtype "delta" carries top-level reasoning text —
  contrary to the docs) and surface as Reasoning, like pi. `tool_call`
  `started` carries the tool-named wrapper (`readToolCall`) beside sibling
  metadata (`toolCallId`/`startedAtMs`/`hookAdditionalContexts`), from which
  the adapter picks the wrapper. The Final TEXT is the LAST assistant
  segment (accumulated from the deltas; segments are the text runs between
  tool calls), NOT the envelope's `result` field — confirmed live to be the
  no-separator concatenation of every segment in the turn, which would glue
  narration onto the stored body. The field is used only as the fallback
  when no delta streamed.
- Every live assertion wrapped in a skip-unless-installed guard; the
  fixture corpus is now raw live captures (cursor-*.ndjson). Tier: FULL —
  real-CLI-verified end to end (the `TestCursorLive_ToolLoopSubmitPlaybook`
  acceptance test drives the isolated redirect + guard + submit_playbook
  round-trip against the installed CLI).

### claude (refactor only)

- `ClaudeArgs`, the thinking env mapping, and `WriteMCPConfig` move into the
  claude harness/adapter files (they are claude-specific and currently sit
  in events.go/mcp.go); behavior byte-identical, goldens prove it.

## Config

- Keys unchanged: `[agent] harness / model / triage_model / bin / thinking`.
- Defaults resolve through the harness: a new per-harness defaults table
  (claude: triage "haiku"; pi/cursor: recorded during characterization).
  `TriageModel: "haiku"` leaves config.go. Explicit values always win.
- `harness` accepts `claude | pi | cursor`; unknown names keep failing fast.

## Leak cleanup (rides the milestone)

- `session.go:454/678` `Harness: "Claude Code"` → `h.DisplayName()`.
- `results.go` RegenNote / `validatecmd.go` aiSkipNote → name the configured
  harness's binary ("install and authenticate <bin>").
- `debug.go` LookPath("claude") → the shared bin-resolution.
- Docs: configuration.md `[agent]` section documents the three harnesses +
  the per-harness defaults; README/architecture overview stop implying
  Claude is the only backend.

## Out of scope (recorded, not built)

- Session resume / conversation reuse (one-shot is the contract).
- Adapter plugins (external adapter binaries).
- Per-harness config sections (`[agent.pi]` …).
- Additional harnesses (codex, gemini) — the contract is designed for them;
  adapters are follow-ups.
- Model-name translation between harnesses (model strings pass through).

## Testing

- Contract tests run against EVERY registered harness: argv shape sanity
  (system prompt present in the right mode, no session flags), env
  hygiene, `ToolTransport` writes only into the given dir + returns
  attachable argv, DisplayName/AdapterName non-empty, Capabilities
  consistency (Tools ⇒ ToolTransport succeeds).
- Adapter stream tests: fixture corpora per harness (happy stream, tool
  activity, truncated stream → error, garbage line → error, empty
  reasoning dropped) — fixtures are the always-run baseline for all three
  adapters. LIVE tests are conditional on the harness CLI being installed:
  any system that has the required binary runs them, any system without it
  skips them (one shared `requireHarness(t, bin)` helper; `t.Skip` with the
  binary name when absent). This applies uniformly — claude, pi, and cursor
  live tests all run wherever their CLI exists (pi fixtures are captured
  from the live CLI on this machine; cursor fixtures from documentation
  until the CLI is available somewhere).
- Tier degradation: a fake BASIC harness drives the launcher — structured
  paths fall back to text with the note (once), KB fill skipped with the
  note, recall/classify/metadata unchanged; prompt folds never mention
  unavailable tools.
- Leak regression: no "claude"/"Claude" literals outside the claude
  adapter files, the skill-install default, and docs (a lint-style test
  with an allowlist).
- Refactor safety: claude argv goldens byte-identical across the seam move;
  full `make check`.
