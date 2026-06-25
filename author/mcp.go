package author

import (
	"encoding/json"
	"io"
	"os"
)

// ToolInstruction is appended to the authoring system prompt when the claude
// harness is invoked with the tools backend (--mcp-config). It tells the agent to
// diagnose via the `run` MCP tool — which executes in the USER's real interactive
// shell — rather than its own bash, so commands run in the environment the
// playbook will run in.
const ToolInstruction = "\n\n" +
	"## Diagnosing in the user's environment\n" +
	"You have MCP tools `run`, `remember`, and `ask`. To run any command while " +
	"diagnosing, ALWAYS use the `run` tool — NOT your own shell — so the command " +
	"executes in the USER's real interactive shell (their cwd, aliases, and " +
	"environment), the exact shell the playbook's steps will run in. Keep those " +
	"commands read-only or idempotent. Use `remember` to save a durable project " +
	"fact and `ask` to get input from the user.\n"

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

// WriteMCPConfig writes a claude --mcp-config JSON to a temp file pointing claude
// at `<selfExe> mcp --socket <socketPath>` (the MCP adapter, package mcpserver),
// and returns the file path. The caller passes the path to claude via
// --mcp-config and removes it when authoring is done. selfExe is the running
// ai-playbook binary (os.Executable).
func WriteMCPConfig(selfExe, socketPath string) (string, error) {
	doc := mcpConfig{McpServers: map[string]mcpServerSpec{
		"ai-playbook": {
			Command: selfExe,
			Args:    []string{"mcp", "--socket", socketPath},
		},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "ai-playbook-mcp-*.json")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// claudeRunner is the seam tests substitute to capture the claude argv without
// launching the real process. Production uses runClaude.
type claudeRunner func(systemPrompt, userMessage string, extraArgs []string) (io.ReadCloser, error)

// defaultClaudeRunner is the production runner (runClaude); ClaudeAgentWithMCP
// uses it unless a test swaps it via claudeAgentWithMCPRunner.
var defaultClaudeRunner claudeRunner = runClaude

// ClaudeAgentWithMCP returns an Agent that runs claude headless WITH the tools
// backend wired: it writes a --mcp-config pointing at `<selfExe> mcp --socket
// <socketPath>` and appends ToolInstruction to the system prompt so the agent
// uses the `run` tool (the user's real shell) instead of its own bash. The temp
// mcp-config file is removed when the returned stream is closed.
//
// If the mcp-config can't be written, it falls back to the plain ClaudeAgent
// (author-as-before) so a backend/config hiccup never blocks authoring.
func ClaudeAgentWithMCP(selfExe, socketPath string) Agent {
	return claudeAgentWithMCPRunner(selfExe, socketPath, defaultClaudeRunner)
}

// claudeAgentWithMCPRunner is ClaudeAgentWithMCP with an injectable runner (the
// test seam). The returned Agent appends ToolInstruction, writes the mcp-config,
// and forwards --mcp-config to the runner; on a config-write failure it authors
// without tools (plain runner, no extra args).
func claudeAgentWithMCPRunner(selfExe, socketPath string, run claudeRunner) Agent {
	return func(systemPrompt, userMessage string) (io.ReadCloser, error) {
		cfg, err := WriteMCPConfig(selfExe, socketPath)
		if err != nil {
			// Fallback: author as before (no tools) rather than fail the session.
			return run(systemPrompt, userMessage, nil)
		}
		stream, rerr := run(systemPrompt+ToolInstruction, userMessage, []string{"--mcp-config", cfg})
		if rerr != nil {
			os.Remove(cfg)
			return nil, rerr
		}
		// Remove the temp config when the stream closes (the process has exited).
		return &removeOnClose{ReadCloser: stream, path: cfg}, nil
	}
}

// removeOnClose removes path after the wrapped stream is closed (the mcp-config
// temp file is needed only for claude's lifetime).
type removeOnClose struct {
	io.ReadCloser
	path string
}

func (r *removeOnClose) Close() error {
	err := r.ReadCloser.Close()
	os.Remove(r.path)
	return err
}
