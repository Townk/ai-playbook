package author

import (
	"fmt"
	"os"
	"os/exec"

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
func ClaudeArgs(model, mcpConfigPath, systemPrompt, userMessage string) []string {
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
	args = append(args, "--append-system-prompt", systemPrompt, userMessage)
	return args
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
	)
	switch harness {
	case "claude":
		sys := systemPrompt
		if opts.MCPConfigPath != "" {
			sys += ToolInstruction
		}
		args = ClaudeArgs(cfg.Agent.Model, opts.MCPConfigPath, sys, userMessage)
		adapterName = "claude"
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
