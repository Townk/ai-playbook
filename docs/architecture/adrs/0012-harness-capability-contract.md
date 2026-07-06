# Multi-harness: the capability contract, the transport seam, and tiered degradation

- **Status:** Accepted
- **Date:** 2026-07-06

## Context and Problem Statement

The model harness has been pluggable in name since ADR-0009 — a 3-method
`Harness` seam (argv / adapter-name / env), an `agentstream` adapter registry
normalizing the wire into four event kinds, and a deliberately
harness-neutral tools backend (`internal/tools`, unix-socket JSON-RPC). But
only a Claude implementation exists, and the 2026-07-06 coupling survey found
the seam incomplete in one structural way and leaky in four small ones:

- **The transport hole**: MCP-config assembly (`WriteMCPConfig`) and the
  `--mcp-config` flag are wired by the *launcher*, outside the `Harness`
  seam. A harness whose tool transport is not Claude-shaped MCP (pi's is an
  extension system) has no hook.
- **Leaks**: the `TriageModel: "haiku"` config default (a Claude model alias
  in harness-agnostic code); hardcoded `"Claude Code"` UI labels
  (session.go); "install the Claude CLI" no-backend strings (results.go,
  validatecmd.go); `exec.LookPath("claude")` in debug.go.
- Every invocation is a **one-shot process** (11 shapes, all `claude -p`,
  no session state) — an assumption that is actually the easiest contract
  for other CLIs to meet.
- Structured output rides **schema-enforced tool-use** (`submit_playbook`):
  the harness's tool loop must validate JSON schemas and re-ask.

v0.13 must define what "a harness" IS — which capabilities are required,
which degrade, and where the seam boundary sits — and ship pi and cursor
adapters against that contract.

## Decision Drivers

- 1.0 = phases 5 + 6 + multi-harness (owner's definition).
- The playbook-first layering (ADR-0009): the AI layer is replaceable; the
  harness is a detail of that layer.
- No silent fallbacks: a missing capability must be visible, never guessed
  around (the A5c doctrine).
- pi 0.80.3 is installed and live-characterizable; cursor-agent is not
  (fixture-driven until it is).

## Decision Outcome

1. **Two capability tiers, degrade + note.** The `Harness` interface gains a
   `Capabilities()` descriptor. **FULL** = streaming events + a
   schema-enforced tool loop + a tool transport reaching our socket backend
   (claude today; pi/cursor targeted). **BASIC** = streaming text only.
   Under BASIC: authoring uses the existing text path (free-text markdown),
   structured create / `submit_playbook` / `remember` / KB fill degrade —
   each with ONE visible note naming the missing capability — and recall,
   classify, metadata, compaction, drift-regen, followup, and the validate
   AI review (none of which need tools) work unchanged. Nothing refuses
   outright except a harness name the registry does not know.
2. **The tool transport moves behind the seam.** A new `Harness` method
   owns transport wiring (writing the transport artifact — Claude's
   mcp-config JSON, pi's extension file — and contributing the argv/env that
   attaches it to the invocation). The launcher stops calling
   `WriteMCPConfig` directly; it asks the harness. `internal/mcpserver`
   is recognized for what it is — the *Claude transport adapter* — and pi's
   sibling (an embedded pi extension forwarding to `tools.Dial`) lives next
   to it. The socket wire protocol is the stable contract; transports are
   per-harness.
3. **Adapters shipped: pi and cursor.** pi: characterized live against
   0.80.3 (`-p --mode json`, `--system-prompt`/`--append-system-prompt`,
   native `--thinking`, `--no-context-files --no-extensions --no-skills` as
   bare mode, tools via an embedded extension). cursor: built fixture-first
   from its documented `--print`/stream output and MCP config; live
   verification gated on the CLI being installed (`t.Skip` otherwise).
   Claude's adapter is untouched except where the seam refactor moves code
   it already owns.
4. **Flat config keys, per-harness defaults.** `[agent]` keys stay as they
   are; the DEFAULTS for `model`/`triage_model`/`thinking` resolve through
   the selected harness (claude → triage "haiku"; pi/cursor → their own
   cheap-model aliases, chosen during adapter characterization). Explicit
   user values always win. The "haiku" literal leaves harness-agnostic code.
5. **Leak cleanup rides the milestone.** UI labels come from a new
   `Harness.DisplayName()`; the no-backend messages name the *configured*
   harness's binary; debug.go resolves through the same bin-resolution the
   real invocation uses.
6. **The one-shot contract is codified.** A harness must support
   fresh-process, non-interactive invocation with a final answer; session
   resume is explicitly NOT part of the contract (pi's session flags are
   suppressed with `--no-session`).

## Alternatives Considered

- **Single tier, MCP required** — simpler contract, fewer degraded paths;
  rejected by the owner: excludes tool-transportless CLIs entirely, and the
  text-authoring fallback already exists and works.
- **Per-harness config sections** (`[agent.claude]` / `[agent.pi]`) —
  more explicit, pre-configures several harnesses; rejected for now as a
  bigger schema change than the need (flat keys + per-harness defaults
  cover the real use case; revisit if users demonstrably switch often).
- **Direct LLM-API integration (no CLI harness)** — bypasses the harness
  concept; rejected: the terminal-native philosophy delegates auth, model
  routing, and tool loops to the user's existing agent CLI.
- **Adapter plugins (external binaries/scripts)** — maximal openness;
  rejected for v1: in-tree adapters keep the strict-stream discipline
  (A5b) testable and reviewed.

## Consequences

- `Harness` grows from 3 methods to ~6 (`Capabilities`, `DisplayName`,
  the transport hook) — still one seam, still config-blind.
- The launcher's MCP wiring paths (session, create, drift-reengage) become
  harness-calls; their tests move behind fakes of the seam.
- Degraded-mode notes become part of the UX contract (tested strings).
- The docs stop naming Claude as *the* backend (configuration.md, README,
  architecture overview) — it becomes the default of three.
- Spec: `docs/specifications/multi-harness.md`.
