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
  kinds live and maps them to the four events (reasoning arrives as
  `thinkingText` per the existing code note). Strict-stream rules apply.
- Tools: a pi EXTENSION (JS, embedded via go:embed, written to the session
  temp dir by `ToolTransport`) registering `run`/`ask`/`remember`/
  `submit_playbook`, each forwarding to the unix-socket backend
  (`tools.Dial` wire). Attached via `--extension <path>`; discovery stays
  disabled so ONLY our extension loads. Tier: FULL if pi's tool loop
  enforces input schemas (characterized in the adapter task; if it does
  not, pi ships BASIC and the extension work is deferred — the task
  records the finding either way).

### cursor (fixture-first; live tests gated on the CLI)

- Invocation: `cursor-agent -p --output-format stream-json` (documented
  shape); system-prompt handling characterized from docs/fixtures — if no
  replace/append flags exist, the adapter documents the fold-into-user-
  message fallback for the bare path.
- Tools: cursor speaks MCP; `ToolTransport` writes the same mcpServers
  document Claude uses (shared writer, per-harness attachment flags).
- Every live assertion wrapped in a skip-unless-installed guard; the
  fixture corpus is the review artifact. Tier target: FULL; BASIC until
  the tool loop is proven.

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
