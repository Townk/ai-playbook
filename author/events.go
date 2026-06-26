package author

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"ai-playbook/agentstream"
	"ai-playbook/capture"
	"ai-playbook/config"
	"ai-playbook/kb"
)

// ClaudeArgs builds the OWNED claude argv for the streaming event path. The
// invocation flags and the stream adapter are a single matched contract — these
// flags are NOT user-configurable; the user only selects the harness + value
// prefs (model, bin) via config [agent]. The flags mirror the existing
// ClaudeAgentWithMCP wiring (mcp-config + append-system-prompt + positional user
// message), swapped to stream-json so agentstream's claude adapter can parse it:
//
//	claude -p --output-format stream-json --verbose --include-partial-messages
//	       [--model <model>] [--mcp-config <path>]
//	       --append-system-prompt <systemPrompt> <userMessage>
//
// model and mcpConfigPath are optional (omitted when empty).
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
func ClaudeArgs(model, mcpConfigPath, systemPrompt, userMessage string, bare bool) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if mcpConfigPath != "" {
		args = append(args, "--mcp-config", mcpConfigPath)
	}
	if bare {
		args = append(args,
			"--strict-mcp-config",
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
// reasoning activity streams out of the box. "off" returns 0 → no env var set →
// no thinking. NOTE: in --print stream-json the thinking block TEXT is omitted
// (Claude Code does not surface the readable summary); the blocks still stream,
// so the live "model is reasoning" activity fires even though the text is empty.
// pi (--mode json, thinkingText) surfaces the reasoning text natively.
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

// AuthorOptions tunes AuthorEvents. Cfg supplies the harness selection + value
// prefs ([agent]); MCPConfigPath, when set, wires the tools backend into the
// owned argv. Command is the test seam: when non-nil it replaces the real
// process launch with a caller-built *exec.Cmd (the fake-harness pattern), so
// AuthorEvents is unit-testable without a real claude on PATH.
type AuthorOptions struct {
	Cfg *config.Config
	// MCPConfigPath, when non-empty, is forwarded as --mcp-config (claude harness).
	MCPConfigPath string
	// ModelOverride, when non-empty, replaces cfg [agent].Model for THIS invocation
	// only (the owned argv's --model). It is the seam the cheap CLASSIFY pass uses to
	// run on the triage model without disturbing the authoring path (which keeps
	// using cfg [agent].Model). Empty → the configured Model is used as before.
	ModelOverride string
	// Bare, when true, strips the owned claude argv to a BARE quick-model call: it
	// REPLACES the default system prompt (--system-prompt, not --append-system-prompt)
	// and adds --strict-mcp-config + --exclude-dynamic-system-prompt-sections, so the
	// classify pass runs without CLAUDE.md auto-discovery, auto-memory, global MCP, or
	// the dynamic machine sections. The AUTHORING path leaves this false (full context).
	Bare bool
	// NoThinking, when true, forces MAX_THINKING_TOKENS=0 for THIS invocation,
	// disabling extended thinking regardless of cfg [agent].Thinking. The quick
	// structured calls (classify, metadata) set it: a triage/JSON decision needs no
	// reasoning, and leaving thinking on costs ~4-6s of latency (haiku thinks by
	// default). The AUTHORING path leaves this false so its reasoning streams as
	// live activity.
	NoThinking bool
	// OnText, when non-nil, is called with the ACCUMULATED assistant text as each
	// stream-json TEXT delta arrives (a live tap of the model output, used by the
	// classify pass to surface its reasoning on the float's thinking line). nil →
	// no-op; behavior unchanged.
	OnText func(accumulated string)
	// Command overrides process construction for tests. It receives the resolved
	// bin and owned argv and returns the *exec.Cmd to run. nil → exec.Command.
	Command func(bin string, args []string) *exec.Cmd
}

// AuthorEvents runs the configured harness on req and returns a channel of
// normalized agentstream.Events, a close/wait func that reaps the process, and a
// start error. It builds the STANDARD authoring prompt (system prompt + folded KB,
// user message) from req and delegates the owned invocation + adapter to
// RunHarnessEvents. Re-engagement (followup/wrapup/regenerate) reuses
// RunHarnessEvents directly with its own prompts.
//
// The returned func() error waits for the process to exit (reaping it) and
// returns its exit error; call it after draining the channel.
func AuthorEvents(req capture.Request, opts AuthorOptions) (<-chan agentstream.Event, func() error, error) {
	sys := SystemPrompt(req, KnowledgeBase(kb.Load(req.ProjectRoot)))
	user := BuildUserMessage(req)
	return RunHarnessEvents(sys, user, opts)
}

// RunHarnessEvents is the OWNED harness invocation + adapter core: given a final
// systemPrompt and userMessage, it selects the harness from cfg [agent].Harness
// (claude implemented; pi/cursor return a clear "not yet supported" error),
// resolves the bin (cfg [agent].Bin else the harness name on PATH), builds the
// OWNED argv, starts the process, and streams its stdout through
// agentstream.Get(adapter)'s Parse, forwarding each event on the returned channel
// (closed on EOF).
//
// When opts.MCPConfigPath is set, the harness's tool-use instruction is appended
// to systemPrompt (so the agent reaches the session's run/ask/remember backend)
// and --mcp-config is wired into the argv — exactly as the standard authoring path
// does, so re-engagement's followup/wrapup/regenerate prompts get the same tools.
//
// The returned func() error waits for the process to exit (reaping it) and returns
// its exit error; call it after draining the channel.
func RunHarnessEvents(systemPrompt, userMessage string, opts AuthorOptions) (<-chan agentstream.Event, func() error, error) {
	cfg := opts.Cfg
	if cfg == nil {
		cfg = config.Default()
	}

	harness := cfg.Agent.Harness
	if harness == "" {
		harness = config.Default().Agent.Harness
	}

	// Per-harness {owned argv + stream adapter} pair. Only claude ships today.
	var (
		args        []string
		adapterName string
		extraEnv    []string // appended to the harness process env (e.g. thinking)
	)
	switch harness {
	case "claude":
		sys := systemPrompt
		if opts.MCPConfigPath != "" {
			sys += ToolInstruction
		}
		// Per-invocation model override (the classify pass selects the triage model)
		// falls back to the configured authoring model when unset.
		model := cfg.Agent.Model
		if opts.ModelOverride != "" {
			model = opts.ModelOverride
		}
		args = ClaudeArgs(model, opts.MCPConfigPath, sys, userMessage, opts.Bare)
		adapterName = "claude"
		// MAX_THINKING_TOKENS must be set EXPLICITLY both ways. A budget (>0) enables
		// thinking so the claude adapter's Reasoning mapping has blocks to emit; 0
		// DISABLES it. Crucially, OMITTING the var does NOT disable thinking — Claude
		// Code defaults thinking ON — so "off" (and opts.NoThinking) must emit
		// MAX_THINKING_TOKENS=0, not skip the var. Disabling thinking cuts a quick
		// classify/metadata call from ~7s to ~2.6s (haiku thinks by default).
		thinking := cfg.Agent.Thinking
		if opts.NoThinking {
			thinking = "off"
		}
		if tok := claudeThinkingTokens(thinking); tok > 0 {
			extraEnv = append(extraEnv, "MAX_THINKING_TOKENS="+strconv.Itoa(tok))
		} else {
			extraEnv = append(extraEnv, "MAX_THINKING_TOKENS=0")
		}
	default:
		return nil, nil, fmt.Errorf("harness %q not yet supported", harness)
	}

	adapter, ok := agentstream.Get(adapterName)
	if !ok {
		// Should not happen for a shipped harness; fall back to passthrough.
		adapter, _ = agentstream.Get("text")
	}

	bin := cfg.Agent.Bin
	if bin == "" {
		bin = harness
	}

	cmd := buildCommand(opts.Command, bin, args)
	if len(extraEnv) > 0 {
		// Inherit the parent env (nil Env == os.Environ at exec time) and append
		// our extras. Set explicitly only when adding, to avoid disturbing a
		// test-seam command that may have configured its own Env.
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	events := make(chan agentstream.Event)
	go func() {
		defer close(events)
		// Parse forwards normalized events; its error (a fatal read failure) is
		// not surfaced here — the close/wait func reports the process exit, which
		// is the authoritative signal for the caller.
		_ = adapter.Parse(stdout, func(e agentstream.Event) { events <- e })
	}()

	wait := func() error {
		return cmd.Wait()
	}
	return events, wait, nil
}

// buildCommand applies the test seam if present, else exec.Command.
func buildCommand(override func(string, []string) *exec.Cmd, bin string, args []string) *exec.Cmd {
	if override != nil {
		return override(bin, args)
	}
	return exec.Command(bin, args...)
}
