package author

import (
	"io"
	"os"
	"os/exec"

	"ai-playbook/capture"
)

// Agent runs the capable agent with the given system prompt and user message and
// returns its stdout as a STREAM (io.ReadCloser) so the ui can render the produced
// playbook incrementally as the model emits it. It is injectable so tests can
// substitute a deterministic fake (no live claude).
type Agent func(systemPrompt, userMessage string) (io.ReadCloser, error)

// KB is the knowledge base folded into the system prompt. Empty for now — see the
// KnowledgeBase type doc; the KB store/lookup port lands with a later stage.
var KB KnowledgeBase = ""

// Author is the producer's LLM half: it assembles the standing system prompt and
// the per-request user message from req, then runs the agent and returns its
// stdout stream. The agent is injected (ClaudeAgent in production; a fake in
// tests) so this function is deterministic to unit-test.
func Author(req capture.Request, agent Agent) (io.ReadCloser, error) {
	sys := SystemPrompt(req, KB)
	user := BuildUserMessage(req)
	return agent(sys, user)
}

// claudeBin resolves the claude executable, mirroring ai-assist-claude's
// $AI_ASSIST_CLAUDE_BIN (default "claude").
func claudeBin() string {
	if v := os.Getenv("AI_ASSIST_CLAUDE_BIN"); v != "" {
		return v
	}
	return "claude"
}

// claudeModel resolves the capable model, mirroring ai-assist-claude:
// $ASSIST_MODEL, else $AI_ASSIST_MODEL, else "sonnet". Capable by design — never
// a cheap one (the cheap haiku pass was the triage classify step, not authoring).
func claudeModel() string {
	if v := os.Getenv("ASSIST_MODEL"); v != "" {
		return v
	}
	if v := os.Getenv("AI_ASSIST_MODEL"); v != "" {
		return v
	}
	return "sonnet"
}

// claudePermissionMode resolves the headless permission posture, mirroring
// $AI_ASSIST_CLAUDE_PERMISSION_MODE (default bypassPermissions) so the headless
// agent never blocks on an interactive permission prompt.
func claudePermissionMode() string {
	if v := os.Getenv("AI_ASSIST_CLAUDE_PERMISSION_MODE"); v != "" {
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
	cmd := exec.Command(
		claudeBin(),
		"--print", "--output-format", "text",
		"--permission-mode", claudePermissionMode(),
		"--model", claudeModel(),
		"--append-system-prompt", systemPrompt,
		userMessage,
	)
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
