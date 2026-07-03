package author

import "strconv"

// Harness is the per-harness seam RunHarnessEvents drives. Config selects WHICH
// harness ([agent].harness); the Harness owns that harness's process argv, the
// agentstream adapter that parses its stdout, and any extra process env — the
// three pieces that used to live inline in RunHarnessEvents' one-armed
// `switch harness`. Keeping them behind this interface keeps prompt assembly
// (system/user message construction, the tool-instruction fold) harness-free: a
// Harness never sees a *config.Config, only the already-resolved Invocation. Only
// claude ships today (see harnessFor); pi/cursor are additive later.
type Harness interface {
	// Argv builds the owned process argv for the final systemPrompt + userMessage
	// and the resolved per-call knobs.
	Argv(systemPrompt, userMessage string, inv Invocation) []string
	// AdapterName names the agentstream adapter that parses this harness's stdout.
	AdapterName() string
	// Env returns extra KEY=VALUE entries appended to the harness process env.
	Env(inv Invocation) []string
}

// Invocation carries the resolved per-call knobs a Harness needs, decoupled from
// AuthorOptions/config so a Harness implementation stays free of config types.
// RunHarnessEvents resolves these (model override, thinking preference, …) before
// handing them to the harness.
type Invocation struct {
	// Model is the resolved model id (cfg [agent].Model, or the per-call override);
	// empty means "harness default".
	Model string
	// MCPConfigPath, when non-empty, wires the tools backend into the argv.
	MCPConfigPath string
	// Bare selects the stripped quick-model CLASSIFY invocation.
	Bare bool
	// Thinking is the resolved reasoning preference ("off" when NoThinking forced it).
	Thinking string
}

// harnessFor resolves a configured harness name to its implementation. The bool is
// false for a not-yet-supported harness (pi/cursor), letting RunHarnessEvents
// return a clear error instead of silently falling back to claude — the A5c fix
// (config selection is now honored on EVERY path, not just the events path).
func harnessFor(name string) (Harness, bool) {
	switch name {
	case "claude":
		return claudeHarness{}, true
	default:
		return nil, false
	}
}

// claudeHarness is the Claude Code {owned argv + stream adapter + process env}
// contract. It delegates to ClaudeArgs (the invocation flags) and
// claudeThinkingTokens (the MAX_THINKING_TOKENS budget) so the flag/adapter
// knowledge stays in one place.
type claudeHarness struct{}

func (claudeHarness) Argv(systemPrompt, userMessage string, inv Invocation) []string {
	return ClaudeArgs(inv.Model, inv.MCPConfigPath, systemPrompt, userMessage, inv.Bare)
}

func (claudeHarness) AdapterName() string { return "claude" }

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
