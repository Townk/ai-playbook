package author

import (
	"encoding/json"
	"io"
	"os"

	"github.com/Townk/ai-playbook/internal/config"
)

// ToolInstruction is appended to the authoring system prompt when the claude
// harness is invoked with the tools backend (--mcp-config). It tells the agent to
// diagnose via the `run` MCP tool — which executes in the USER's real interactive
// shell — rather than its own bash, so commands run in the environment the
// playbook will run in.
const ToolInstruction = "\n\n" +
	"## Diagnosing in the user's environment\n" +
	"You have MCP tools `run`, `remember`, and `ask`.\n" +
	"- Use `run` ONLY to DIAGNOSE: reproduce the failure and inspect state (cwd, " +
	"files, versions). It executes in the USER's real interactive shell — their cwd, " +
	"aliases, and env, the exact shell the playbook's steps will run in. Keep these " +
	"checks READ-ONLY; do not mutate the project with it.\n" +
	"- Do NOT use `run` to APPLY the fix or perform the task. The fix and its " +
	"verification are the PLAYBOOK's job: you MUST WRITE them as `{id=fix}` and " +
	"`{id=verify needs=fix}` fenced code blocks for the USER to run. Authoring that " +
	"playbook IS your deliverable — NEVER apply the fix via `run` and then just " +
	"summarize what you did, and NEVER merely describe the steps in prose; emit the " +
	"ACTUAL runnable code blocks. A reply with no `{id=fix}`/`{id=verify}` blocks is " +
	"a failure.\n" +
	"- Use `remember` for a durable project fact and `ask` to get input from the user.\n"

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

// HarnessAgentWithMCP is the tools-wired production Agent: per call it writes a
// fresh --mcp-config pointing at `<selfExe> mcp --socket <socketPath>` and runs the
// CONFIGURED harness via RunHarnessEvents, which wires --mcp-config into the argv
// AND appends the tool instruction to the system prompt (so the agent reaches
// run/ask/remember in the user's real shell). The temp config is removed when the
// returned stream closes.
//
// It routes through the SAME events path as the initial authoring, so the harness
// selection ([agent].harness) is honored — replacing the retired claude-only
// ClaudeAgentWithMCP, whose own runner + duplicate tool-instruction fold are gone
// (the events path owns both now). On a config-write failure it authors WITHOUT
// tools (plain harness call) so a backend/config hiccup never blocks authoring.
func HarnessAgentWithMCP(cfg *config.Config, selfExe, socketPath string) Agent {
	return func(systemPrompt, userMessage string) (io.ReadCloser, error) {
		path, err := WriteMCPConfig(selfExe, socketPath)
		if err != nil {
			// Fallback: author as before (no tools) rather than fail the session.
			return runHarnessText(systemPrompt, userMessage, AuthorOptions{Cfg: cfg})
		}
		stream, rerr := runHarnessText(systemPrompt, userMessage, AuthorOptions{Cfg: cfg, MCPConfigPath: path})
		if rerr != nil {
			os.Remove(path)
			return nil, rerr
		}
		// Remove the temp config when the stream closes (the process has exited).
		return &removeOnClose{ReadCloser: stream, path: path}, nil
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
