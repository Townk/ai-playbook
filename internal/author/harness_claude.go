// harness_claude.go — the Claude Code harness: the owned claude argv, the
// MAX_THINKING_TOKENS env mapping, and the mcp-config tool transport. Everything
// claude-specific in package author lives HERE (the ADR-0012 seam): the rest of
// the package is harness-agnostic and reaches claude only through the Harness
// interface and the registry row this file installs.
package author

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// defaultHarnessName is the compiled-in [agent].harness default: a no-config run
// drives Claude Code. It lives here (not in config) so the default SELECTION is
// a property of the shipped harness set, and harness-agnostic code stays free of
// concrete harness names.
const defaultHarnessName = "claude"

func init() {
	registerHarness(defaultHarnessName, claudeHarness{}, Defaults{
		// Model "": claude picks its own default authoring model.
		Model: "",
		// The cheap classify pass: "haiku" is the claude CLI model alias
		// (cheap+fast) for a one-shot command/answer/escalate decision.
		TriageModel: "haiku",
		// "medium" → MAX_THINKING_TOKENS=8000 in the owned invocation, so
		// reasoning blocks stream as live activity by default.
		Thinking: "medium",
	})
}

// claudeHarness is the Claude Code {owned argv + stream adapter + process env +
// tool transport} contract. It delegates to claudeArgs (the invocation flags)
// and claudeThinkingTokens (the MAX_THINKING_TOKENS budget) so the flag/adapter
// knowledge stays in one place.
type claudeHarness struct{}

func (claudeHarness) Argv(systemPrompt, userMessage string, inv Invocation) []string {
	return claudeArgs(inv.Model, inv.ToolArgv, systemPrompt, userMessage, inv.Bare)
}

func (claudeHarness) AdapterName() string { return "claude" }

func (claudeHarness) DisplayName() string { return "Claude Code" }

// Capabilities: claude is a FULL harness — its tool loop presents MCP tools,
// enforces their JSON schemas (re-asking the model on validation failure), and
// returns tool results, which is what submit_playbook/run/ask/remember ride on.
func (claudeHarness) Capabilities() Capabilities { return Capabilities{Tools: true} }

// Env sets MAX_THINKING_TOKENS EXPLICITLY both ways. A budget (>0) enables thinking
// so the claude adapter's Reasoning mapping has blocks to emit; 0 DISABLES it.
// Crucially, OMITTING the var does NOT disable thinking — Claude Code defaults
// thinking ON — so "off" (and opts.NoThinking, resolved into inv.Thinking) must emit
// MAX_THINKING_TOKENS=0, not skip the var. Disabling thinking cuts a quick
// classify/metadata call from ~7s to ~2.6s (haiku thinks by default).
func (claudeHarness) Env(inv Invocation) []string {
	if tok := claudeThinkingTokens(inv.Thinking); tok > 0 {
		return []string{"MAX_THINKING_TOKENS=" + strconv.Itoa(tok)}
	}
	return []string{"MAX_THINKING_TOKENS=0"}
}

// ToolTransport writes claude's transport artifact — the --mcp-config JSON
// pointing claude at `<SelfExe> mcp --socket <socketPath>` (the MCP stdio
// adapter, package mcpserver, which forwards tool calls to the session's tools
// backend) — into dir, and returns the argv addition that attaches it.
func (claudeHarness) ToolTransport(inv Invocation, socketPath, dir string) (files []string, argv []string, err error) {
	if inv.SelfExe == "" {
		return nil, nil, errors.New("claude tool transport: the ai-playbook executable path (SelfExe) is unknown")
	}
	doc := mcpConfig{McpServers: map[string]mcpServerSpec{
		"ai-playbook": {
			Command: inv.SelfExe,
			Args:    []string{"mcp", "--socket", socketPath},
		},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "mcp-config.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, nil, err
	}
	return []string{path}, []string{"--mcp-config", path}, nil
}

// mcpConfig is the claude --mcp-config document shape: a map of server name → an
// stdio server spec (command + args) claude launches and speaks MCP to over its
// stdio. Our server is `ai-playbook mcp --socket <path>`, which forwards tool
// calls to the session's tools backend.
type mcpConfig struct {
	McpServers map[string]mcpServerSpec `json:"mcpServers"`
}

type mcpServerSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// claudeArgs builds the OWNED claude argv for the streaming event path. The
// invocation flags and the stream adapter are a single matched contract — these
// flags are NOT user-configurable; the user only selects the harness + value
// prefs (model, bin) via config [agent]. The flags mirror the existing
// tools-backend wiring (the ToolTransport argv + append-system-prompt +
// positional user message), in stream-json so agentstream's claude adapter can
// parse it:
//
//	claude -p --output-format stream-json --verbose --include-partial-messages
//	       [--model <model>] [--mcp-config <path>]
//	       --append-system-prompt <systemPrompt> <userMessage>
//
// model and toolArgv are optional (omitted when empty); toolArgv is the
// ToolTransport-returned attachment (["--mcp-config", <path>]).
//
// When bare is set (the cheap CLASSIFY pass), the call is stripped to a BARE
// quick-model invocation: it REPLACES the default system prompt with
// --system-prompt (instead of --append-system-prompt) — which, per `claude
// --help`, drops Claude's auto-discovery of CLAUDE.md, sync/attribution, and
// auto-memory — and adds --strict-mcp-config (confine MCP to --mcp-config, which
// classify never passes → no global MCP) and
// --exclude-dynamic-system-prompt-sections (drop the cwd/env/git-status/memory
// machine sections). All three flags are present in the current claude build
// (verified against `claude --help`). The authoring path (bare=false) keeps
// --append-system-prompt and Claude's full default context, unchanged.
func claudeArgs(model string, toolArgv []string, systemPrompt, userMessage string, bare bool) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		// Use ONLY the MCP servers we pass via --mcp-config (or none) — NEVER the
		// user's global/project MCP servers (Gmail/Calendar/Drive/…). Each of those
		// is a process spawn + handshake at startup, irrelevant to authoring/adapt/
		// classify, and a major chunk of time-to-first-token. Applies to every path
		// (the bare classify already relied on it).
		"--strict-mcp-config",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, toolArgv...)
	if bare {
		args = append(args,
			"--exclude-dynamic-system-prompt-sections",
			"--system-prompt", systemPrompt,
			userMessage,
		)
		return args
	}
	args = append(args, "--append-system-prompt", systemPrompt, userMessage)
	return args
}

// claudeThinkingTokens maps the config [agent].thinking preference to a
// MAX_THINKING_TOKENS budget for the OWNED claude invocation. Claude Code's
// `--print --output-format stream-json` only EMITS thinking blocks (which the
// claude adapter maps to Reasoning events) when extended thinking is enabled,
// and the env var MAX_THINKING_TOKENS is the mechanism Claude Code honors in
// print mode. An empty/unrecognized preference defaults to "on" (medium) so
// reasoning activity streams out of the box. "off" returns 0 → the env var is
// set to 0 → no thinking. NOTE: in --print stream-json the thinking block TEXT
// is omitted (Claude Code does not surface the readable summary); the blocks
// still stream, so the live "model is reasoning" activity fires even though the
// text is empty. pi (--mode json, thinking_delta) surfaces the reasoning text
// natively.
func claudeThinkingTokens(thinking string) int {
	switch thinking {
	case "off", "none", "0":
		return 0
	case "low":
		return 4000
	case "high":
		return 16000
	case "medium", "on", "":
		return 8000
	default:
		// Unknown value: tolerate a bare integer budget; else default to medium.
		if n, err := strconv.Atoi(thinking); err == nil && n >= 0 {
			return n
		}
		return 8000
	}
}
