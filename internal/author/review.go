package author

import "os/exec"

// reviewProcess overrides process construction for ReviewOnce — the test seam
// mirroring AuthorOptions.Command (the fake-harness pattern ClassifyRequest/
// PlaybookMetadata's tests use). ReviewOnce's exported signature carries no
// AuthorOptions (Task 4's `validate` AI-review pass calls it as a plain
// systemPrompt/userMessage function), so there is no per-call Command to inject —
// tests swap this package var directly instead. nil in production →
// RunHarnessEvents' default (exec.Command).
var reviewProcess func(bin string, args []string) *exec.Cmd

// ReviewOnce runs a single one-shot text→text call on the authoring model — the
// `validate` command's AI-review pass (spec: a finished playbook is handed to
// the model for a free-text critique, not a structured decision). It mirrors
// ClassifyRequest's option construction exactly:
//
//   - opts.MCPConfigPath = "" — a review call needs no tools backend; never
//     attach --mcp-config.
//   - opts.Bare = true — a BARE quick-model invocation: REPLACE the default
//     system prompt (--system-prompt, not --append-system-prompt) and drop
//     CLAUDE.md auto-discovery, auto-memory, global MCP, and the dynamic
//     cwd/env/git-status/memory sections, exactly as the classify pass does.
//   - opts.NoThinking = true — a review needs no extended reasoning; disabling
//     thinking cuts latency the same way it does for classify/metadata.
//
// Unlike ClassifyRequest/PlaybookMetadata it does NOT run on an overridden
// model (no ModelOverride) — it runs on the plain authoring model — and it does
// NOT parse the result as JSON: it returns the harness's raw text UNCHANGED.
// It also makes exactly ONE attempt (no classify-style retry loop): the
// (string, error) from runMetadataOnce is returned to the caller as-is, so a
// no-backend/harness-unsupported condition surfaces as a plain error the
// caller can detect, rather than being swallowed into a retry-failed wrapper.
func ReviewOnce(systemPrompt, userMessage string) (string, error) {
	opts := AuthorOptions{
		MCPConfigPath: "",
		Bare:          true,
		NoThinking:    true,
		Command:       reviewProcess,
	}
	return runMetadataOnce(systemPrompt, userMessage, opts)
}
