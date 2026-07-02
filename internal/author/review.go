package author

import (
	"os/exec"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
)

// reviewProcess overrides process construction for ReviewStream — the test
// seam mirroring AuthorOptions.Command (the fake-harness pattern
// ClassifyRequest/PlaybookMetadata's tests use). ReviewStream's exported
// signature carries no AuthorOptions (the `validate` AI-review pass calls it
// as a plain systemPrompt/userMessage function), so there is no per-call
// Command to inject — tests swap this package var directly instead. nil in
// production → RunHarnessEvents' default (exec.Command).
var reviewProcess func(bin string, args []string) *exec.Cmd

// ReviewStream runs a streaming text→text call on the authoring model — the
// `validate` command's AI-review pass (spec: a finished playbook is handed to
// the model for a free-text critique, not a structured decision), driven so a
// caller can render live progress (model-activity feed) while the review
// runs, instead of blocking silently until the harness finishes. It mirrors
// ClassifyRequest's option construction:
//
//   - opts.MCPConfigPath = "" — a review call needs no tools backend; never
//     attach --mcp-config.
//   - opts.Bare = true — a BARE quick-model invocation: REPLACE the default
//     system prompt (--system-prompt, not --append-system-prompt) and drop
//     CLAUDE.md auto-discovery, auto-memory, global MCP, and the dynamic
//     cwd/env/git-status/memory sections, exactly as the classify pass does.
//   - opts.NoThinking = false — UNLIKE classify/metadata, a review leaves
//     extended thinking enabled, so the model-activity feed has reasoning
//     content to display as the review streams.
//
// Unlike ClassifyRequest/PlaybookMetadata it does NOT run on an overridden
// model (no ModelOverride), and it does NOT drain the event stream, parse
// JSON, or retry: it returns RunHarnessEvents' (events, closeFn, err) tuple
// directly. A start error (e.g. a no-backend/unsupported-harness condition) is
// returned UNCHANGED as the 3rd value; the caller drains events for the
// review text and calls closeFn to reap the process and observe its exit
// error.
//
// cfg supplies the project's configured [agent] harness/model/bin (same as
// ClassifyRequest/PlaybookMetadata's callers, e.g. internal/launcher) so
// RunHarnessEvents doesn't silently fall back to config.Default() for a
// project that configured a non-default harness/model. The `validate`
// command loads the project config and passes it here.
func ReviewStream(cfg *config.Config, systemPrompt, userMessage string) (<-chan agentstream.Event, func() error, error) {
	opts := AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: "",
		Bare:          true,
		NoThinking:    false,
		Command:       reviewProcess,
	}
	return RunHarnessEvents(systemPrompt, userMessage, opts)
}
