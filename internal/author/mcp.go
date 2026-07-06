package author

import (
	"io"

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
	"- Use `remember` to save a durable fact, classified with `kind` by how closely it " +
	"is tied to the topic at hand: `system` for machine/tooling truths, `user` for who " +
	"the user is or prefers, `environment` for this project's setup, `topic` (with a " +
	"`topic` name) for a domain-specific lesson. Use `ask` to get input from the user.\n"

// HarnessAgentWithMCP is the tools-wired production Agent: per call it asks the
// CONFIGURED harness's ToolTransport for a fresh transport artifact pointing at
// `<selfExe> mcp --socket <socketPath>` (claude: the --mcp-config JSON) and runs
// the harness via RunHarnessEvents, which splices the transport argv into the
// invocation AND appends the tool instruction to the system prompt (so the agent
// reaches run/ask/remember in the user's real shell). The transport artifact is
// removed when the returned stream closes.
//
// It routes through the SAME events path as the initial authoring, so the harness
// selection ([agent].harness) is honored — replacing the retired claude-only
// ClaudeAgentWithMCP, whose own runner + duplicate tool-instruction fold are gone
// (the events path owns both now). A BASIC harness (no tool loop) and a
// transport-write failure both author WITHOUT tools (plain harness call) so a
// missing capability or a backend/config hiccup never blocks authoring; an
// UNKNOWN harness falls through to the plain call, which surfaces the same clear
// not-yet-supported error the events path reports.
func HarnessAgentWithMCP(cfg *config.Config, selfExe, socketPath string) Agent {
	return func(systemPrompt, userMessage string) (io.ReadCloser, error) {
		h, err := ConfiguredHarness(cfg)
		if err != nil || !h.Capabilities().Tools {
			return runHarnessText(systemPrompt, userMessage, AuthorOptions{Cfg: cfg})
		}
		argv, cleanup, err := WriteToolTransport(h, selfExe, socketPath)
		if err != nil {
			// Fallback: author as before (no tools) rather than fail the session.
			return runHarnessText(systemPrompt, userMessage, AuthorOptions{Cfg: cfg})
		}
		stream, rerr := runHarnessText(systemPrompt, userMessage, AuthorOptions{Cfg: cfg, ToolArgv: argv})
		if rerr != nil {
			cleanup()
			return nil, rerr
		}
		// Remove the transport artifact when the stream closes (the process exited).
		return &cleanupOnClose{ReadCloser: stream, cleanup: cleanup}, nil
	}
}

// cleanupOnClose runs cleanup after the wrapped stream is closed (the transport
// artifact is needed only for the harness process's lifetime).
type cleanupOnClose struct {
	io.ReadCloser
	cleanup func()
}

func (r *cleanupOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.cleanup()
	return err
}
