package author

import (
	"io"
	"os"
	"os/exec"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/kb"
)

// Agent runs the capable agent with the given system prompt and user message and
// returns its stdout as a STREAM (io.ReadCloser) so the ui can render the produced
// playbook incrementally as the model emits it. It is injectable so tests can
// substitute a deterministic fake (no live claude).
type Agent func(systemPrompt, userMessage string) (io.ReadCloser, error)

// Author is the producer's LLM half: it assembles the standing system prompt and
// the per-request user message from req, then runs the agent and returns its
// stdout stream. The agent is injected (ClaudeAgent in production; a fake in
// tests) so this function is deterministic to unit-test.
//
// The per-project knowledge base is loaded from disk (kb.Load) keyed on
// req.ProjectRoot and folded into the system prompt's "## What we already know
// about this project" section, exactly as assist::system_prompt did with the
// $kb_path file. (The KB WRITE/remember path is deferred — see package kb.)
func Author(req capture.Request, agent Agent) (io.ReadCloser, error) {
	sys := SystemPrompt(req, KnowledgeBase(kb.Load(req.ProjectRoot)), driver.ResolveShellName(""))
	user := BuildUserMessage(req)
	return agent(sys, user)
}

// claudeBin resolves the claude executable, mirroring ai-assist-claude's
// $AI_PLAYBOOK_CLAUDE_BIN (default "claude").
func claudeBin() string {
	if v := os.Getenv("AI_PLAYBOOK_CLAUDE_BIN"); v != "" {
		return v
	}
	return "claude"
}

// claudeModel resolves the capable model, mirroring ai-assist-claude:
// $ASSIST_MODEL, else $AI_PLAYBOOK_MODEL, else "sonnet". Capable by design — never
// a cheap one (the cheap haiku pass was the triage classify step, not authoring).
func claudeModel() string {
	if v := os.Getenv("ASSIST_MODEL"); v != "" {
		return v
	}
	if v := os.Getenv("AI_PLAYBOOK_MODEL"); v != "" {
		return v
	}
	return "sonnet"
}

// claudePermissionMode resolves the headless permission posture, mirroring
// $AI_PLAYBOOK_CLAUDE_PERMISSION_MODE (default bypassPermissions) so the headless
// agent never blocks on an interactive permission prompt.
func claudePermissionMode() string {
	if v := os.Getenv("AI_PLAYBOOK_CLAUDE_PERMISSION_MODE"); v != "" {
		return v
	}
	return "bypassPermissions"
}

// ClaudeAgent is the real Agent: it runs claude headless and streams stdout.
//
// Ported from ai-assist-claude's assist_build_cmd / ASSIST_PANE_CMD:
//
//	claude --print --output-format text \
//	       --permission-mode <bypassPermissions> \
//	       --model <sonnet> \
//	       "<prompt>"
//
// In the shell the single prompt arg carried BOTH the standing instructions and
// the request context (assist::system_prompt interpolated REQ_*). Here we keep
// them separate: the standing authoring instructions go on
// --append-system-prompt and the request context is the positional prompt (the
// user message) — claude sees the same total information, and the split lets the
// ui render exactly the model's reply.
//
// Stdout is returned as a streaming pipe (cmd.StdoutPipe) so the ui renders
// incrementally; closing the returned ReadCloser waits for the process to exit.
func ClaudeAgent(systemPrompt, userMessage string) (io.ReadCloser, error) {
	return runClaude(systemPrompt, userMessage, nil)
}

// runClaude builds and starts the claude headless invocation with the base flags,
// optionally appending extraArgs (e.g. --mcp-config for the tools backend). Stdout
// is returned as a streaming pipe (Close waits for the process).
func runClaude(systemPrompt, userMessage string, extraArgs []string) (io.ReadCloser, error) {
	args := []string{
		"--print", "--output-format", "text",
		"--permission-mode", claudePermissionMode(),
		"--model", claudeModel(),
	}
	args = append(args, extraArgs...)
	args = append(args, "--append-system-prompt", systemPrompt, userMessage)

	cmd := exec.Command(claudeBin(), args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procStream{ReadCloser: stdout, cmd: cmd}, nil
}

// procStream wraps the command's stdout pipe so Close also reaps the process
// (Wait), preventing a zombie and surfacing a non-zero exit to the caller.
type procStream struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (p *procStream) Close() error {
	cerr := p.ReadCloser.Close()
	werr := p.cmd.Wait()
	if cerr != nil {
		return cerr
	}
	return werr
}
