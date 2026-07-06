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

### cursor (fixture-first; live tests gated on the CLI)

- Invocation: `cursor-agent -p --output-format stream-json
  --stream-partial-output --mode ask` (documentation-derived; sources cited
  in harness_cursor.go). `--mode ask` is Cursor's documented read-only mode
  — print mode can otherwise use write/shell tools, an unsanctioned mutation
  channel for a text-producing invocation (the hazard pi closes with
  `--no-tools`). The CLI installs as `agent`/`cursor-agent` (never `cursor`),
  so the defaults table grew a per-harness `Bin` column; `cursor-agent` (the
  every-vintage symlink) is the default.
- System prompt: cursor-agent has NEITHER replace nor append flags, and no
  context-suppression flags either (.cursor/rules, AGENTS.md, CLAUDE.md
  auto-load unconditionally) — so BOTH paths use the documented fallback:
  the system prompt is folded into the head of the single positional user
  message, and bare == append in shape.
- Tools: cursor speaks MCP but attaches servers ONLY by file discovery
  (project `.cursor/mcp.json` / global `~/.cursor/mcp.json`; no
  per-invocation config flag). Writing into the user's project config is
  isolation-unsafe (mutates/overwrites a user-owned file, races concurrent
  sessions, and the global servers still load — no `--strict-mcp-config`
  analog), and no documentation establishes a schema-enforcing re-ask tool
  loop. Cursor therefore SHIPPED BASIC (`Capabilities{Tools:false}`); the
  shared-mcpServers-writer factoring is deferred to the promotion, whose
  gate is an ISOLATED per-invocation MCP attachment plus a live
  schema-enforcement proof (the RequireHarness-gated live tests). The
  strongest attachment candidate is `--workspace <temp dir>` holding our
  own `.cursor/mcp.json` — that neutralizes the user-file-mutation and
  concurrent-session objections, but the global servers still load and
  headless MCP approval is undocumented (`--approve-mcps` would
  blanket-approve the user's servers too), so live probes decide.
- Stream: `result` is the terminal envelope (REQUIRED — A5b); assistant
  deltas are deduped per the documented `--stream-partial-output`
  three-variant rule; thinking events are suppressed in print mode, so
  cursor never emits Reasoning. The Final TEXT is the LAST assistant
  segment (accumulated from the deltas; segments are the text runs between
  tool calls), NOT the envelope's `result` field — the documented example
  shows that field is the no-separator concatenation of every segment in
  the turn, which would glue narration onto the stored body. The field is
  used only as the fallback when no delta streamed.
- Every live assertion wrapped in a skip-unless-installed guard; the
  fixture corpus (doc-derived from the published stream-json examples) is
  the review artifact. Tier: BASIC shipped; FULL is the promotion target.

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
