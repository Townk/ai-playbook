// harness_cursor.go — the Cursor harness (Cursor's headless CLI agent,
// installed as `agent` with the legacy `cursor-agent` symlink): the owned
// cursor-agent argv and the system-prompt fold. Everything cursor-specific in
// package author lives HERE (the ADR-0012 seam).
//
// LIVE-VERIFIED against cursor-agent 2026.07.01-777f564: the owned argv and the
// stream mapping below were checked against the real CLI (Phase A of the cursor
// promotion brief), correcting several doc-derived assumptions — see the
// --trust rationale on Argv and the thinking/tool_call notes on the adapter
// (agentstream/cursor.go). The RequireHarness-gated live tests
// (harness_cursor_live_test.go) re-verify wherever the CLI exists.
package author

import "errors"

// cursorBin is the default executable for the cursor harness. The install
// script (https://cursor.com/install, 2026.07.01) symlinks the CLI as BOTH
// `agent` (primary) and `cursor-agent` (legacy); the legacy name is the
// default here because it exists on every install vintage and is unambiguous
// on PATH, while bare `agent` is only present on newer installs. Users on the
// primary name can set [agent] bin = "agent".
const cursorBin = "cursor-agent"

func init() {
	registerHarness("cursor", cursorHarness{}, Defaults{
		// Model "": cursor-agent picks its own default model selection when
		// --model is omitted, and the available catalog depends on the user's
		// Cursor subscription (`cursor-agent --list-models`) — the harness
		// default is the only always-valid choice, same reasoning as pi.
		Model: "",
		// TriageModel "": the classify pass also runs on cursor's own default —
		// any concrete cheap-model id we could bake in may not exist in the
		// user's plan/catalog, turning every classify into a hard failure. A
		// cheaper triage model is one [agent] triage_model line away.
		TriageModel: "",
		// Thinking "": cursor-agent has no reasoning-control flag or env var
		// (verified: `cursor-agent --help` lists none), so there is no lever for
		// this preference to drive — the harness ignores it. (Thinking events DO
		// stream in stream-json and the adapter surfaces them as Reasoning; there
		// is simply no way to tune the level.)
		Thinking: "",
		// Bin: the registry name ("cursor") is NOT the binary name — see
		// cursorBin.
		Bin: cursorBin,
	})
}

// cursorHarness is the Cursor {owned argv + stream adapter + process env}
// contract. Cursor ships BASIC (Capabilities{Tools:false}) — documented on
// ToolTransport below: it speaks MCP, but only via file discovery, which
// cannot be attached per-invocation in isolation.
type cursorHarness struct{}

func (cursorHarness) AdapterName() string { return "cursor" }

func (cursorHarness) DisplayName() string { return "Cursor" }

// Capabilities: BASIC. Two independent blockers, both documentation-derived
// (fixture-first — the live tests are the promotion path):
//
//  1. No isolated transport: cursor-agent discovers MCP servers ONLY from
//     config files — project `.cursor/mcp.json` or global `~/.cursor/mcp.json`
//     (cursor.com/docs/context/mcp); no per-invocation config flag exists
//     (cursor.com/docs/cli/reference/parameters). Writing into the user's
//     project `.cursor/mcp.json` is isolation-unsafe: it mutates (or
//     overwrites) a file the user may own, races concurrent ai-playbook
//     sessions in the same repo, leaks the per-session socket path into
//     durable project state, and — with no --strict-mcp-config analog — the
//     user's global servers would still load beside ours. An isolation-unsafe
//     transport is worse than none, so no transport ships.
//  2. Unproven tool loop: no Cursor documentation states that cursor-agent
//     enforces MCP tool input schemas and re-asks the model on validation
//     failure — the FULL-tier contract submit_playbook rides on.
//
// Promotion gate: an ISOLATED per-invocation MCP attachment plus a live
// schema-enforcement proof, verified by the RequireHarness-gated live tests
// on a machine that has the CLI. The strongest attachment candidate is
// `--workspace <temp dir>` holding OUR OWN .cursor/mcp.json (a root we
// control — no user-file mutation, no cross-session races); even then the
// global ~/.cursor/mcp.json servers still load, and headless MCP approval is
// undocumented (--approve-mcps would blanket-approve the user's servers too)
// — live probes required (cursor.com/docs/cli/mcp).
func (cursorHarness) Capabilities() Capabilities { return Capabilities{Tools: false} }

// Env: cursor-agent needs no extra process env — model selection is a flag,
// no thinking control exists (see the defaults row), and authentication comes
// from the user's own environment/login (CURSOR_API_KEY or `cursor-agent
// login`) untouched.
func (cursorHarness) Env(inv Invocation) []string { return nil }

// Argv builds the OWNED cursor-agent argv for the streaming event path. The
// invocation flags and the stream adapter are a single matched contract; the
// user only selects value prefs (model, bin) via config [agent]:
//
//	cursor-agent -p --output-format stream-json --stream-partial-output
//	             --mode ask --trust [--model <model>] [toolArgv...] <foldedPrompt>
//
// Flag rationale (live-verified against cursor-agent 2026.07.01-777f564):
//
//   - -p --output-format stream-json: the documented headless NDJSON shape
//     (cursor.com/docs/cli/reference/output-format). The prompt is positional,
//     per the documented examples (cursor.com/docs/cli/overview).
//   - --stream-partial-output: without it, assistant text arrives only as
//     whole message segments; with it, the CLI streams real text deltas the
//     adapter turns into live TextDelta events (the same role as claude's
//     --include-partial-messages). The adapter's dedup rule assumes this flag
//     is always set — see the cursor adapter (agentstream/cursor.go).
//   - --mode ask: Cursor's documented READ-ONLY mode ("answers questions and
//     explores code without making any edits",
//     cursor.com/help/ai-features/ask-mode; the CLI flag:
//     cursor.com/docs/cli/reference/parameters). Print mode can otherwise use
//     write and shell tools (cursor.com/docs/cli/reference/permissions) — an
//     unsanctioned mutation channel for an invocation whose entire job is to
//     produce text (the same hazard pi's --no-tools closes). Every cursor path
//     is text-producing (BASIC ⇒ text authoring, classify, followup, review),
//     so read-only is correct for all of them; a FULL promotion revisits this.
//   - --trust: REQUIRED for a headless run in a directory the user has not
//     already trusted interactively. Without it cursor-agent refuses to start
//     ("Workspace Trust Required") and the stream never opens — the BASIC floor
//     cannot be met (live-verified: a fresh dir fails, adding --trust succeeds).
//     Crucially --trust is EPHEMERAL and NARROW: it writes NOTHING durable
//     (verified — the ~/.cursor state files are byte-identical before and after
//     a --trust run) and, unlike --force/--yolo, does NOT lift the per-command
//     permission gates; it only clears the one-time workspace-trust prompt for
//     this invocation. The flag is documented as headless-only ("only works
//     with --print/headless mode", `cursor-agent --help`).
//   - NO session flags: --resume/--continue are never emitted (the one-shot
//     contract). cursor-agent has no documented flag to suppress its local
//     chat persistence (each print run still records a session id); no state
//     is ever REUSED, which is what the contract requires.
//   - NO --force/--yolo: those lift the per-command permission gates (run
//     everything), a mutation channel --mode ask + --trust deliberately do not
//     open — read-only tools only, no state mutation.
//
// System-prompt handling: cursor-agent has NEITHER a replace nor an append
// system-prompt flag (none documented in cursor.com/docs/cli/reference/
// parameters), and no context-suppression flags either — .cursor/rules,
// AGENTS.md, and CLAUDE.md auto-load unconditionally (cursor.com/docs/cli/
// using). So BOTH paths use the multi-harness spec's documented fallback:
// the system prompt is folded into the single positional user message
// (cursorFoldPrompt), and bare and append are the SAME argv shape — there is
// nothing to strip and nothing to replace.
func (cursorHarness) Argv(systemPrompt, userMessage string, inv Invocation) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"--mode", "ask",
		"--trust",
	}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	// The Invocation contract: a non-empty ToolArgv is spliced into the owned
	// argv. Cursor is BASIC today so the launcher never produces one (gated on
	// Capabilities().Tools); the splice is the promotion seam.
	args = append(args, inv.ToolArgv...)
	args = append(args, cursorFoldPrompt(systemPrompt, userMessage))
	return args
}

// ToolTransport: none — cursor ships BASIC. The rationale (file-discovery-only
// MCP attachment is isolation-unsafe; the schema-enforced tool loop is
// unproven) lives on Capabilities above. Callers gate on Capabilities().Tools,
// so reaching this is a caller bug and fails loudly. The shared-mcpServers-
// document factoring the spec sketches for a FULL cursor is deferred to the
// promotion — no speculative shared writer ships without a consumer.
func (cursorHarness) ToolTransport(inv Invocation, socketPath, dir string) (files []string, argv []string, err error) {
	return nil, nil, errors.New(
		"cursor tool transport: unavailable — cursor-agent attaches MCP servers only via " +
			".cursor/mcp.json file discovery (no per-invocation config flag), which cannot be " +
			"written in isolation; cursor runs BASIC")
}

// cursorFoldPrompt is the system-prompt fold (the spec's documented fallback
// for a harness with no system-prompt flags — see the Argv note): the system
// prompt travels at the head of the single positional user message, fenced in
// an explicit tag so the model reads it as standing instructions rather than
// part of the request.
func cursorFoldPrompt(systemPrompt, userMessage string) string {
	return "<system_instructions>\n" + systemPrompt + "\n</system_instructions>\n\n" + userMessage
}
