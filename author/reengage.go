package author

import (
	"fmt"
	"io"
	"strings"

	"ai-playbook/capture"
)

// Followup is the Go port of ai-assist-followup: the "your fix didn't work"
// re-engagement. The agent is told the original request, the project, the failed
// verify command and (when available) the captured output of that failed command,
// then asked to diagnose WHY the previous fix failed and propose a DIFFERENT,
// corrected fix — with a fresh fix block and a SEPARATE {id=verify needs=<fix-id>}
// block that re-runs the original failed command exactly.
//
// The whole follow-up prompt is the system prompt (it carries both the standing
// instructions and the request context, like the shell's --prompt-file pass); the
// user message is the assembled request context so the agent sees the same total
// information ClaudeAgent passes for Author. The agent is injected (fake in tests).
//
// failedOutput is the captured output of the failed command (the shell read it
// from the run log's logpath, capped to 4000 bytes; the ui caps it the same way
// before calling). req.Command is the failed verify command.
func Followup(req capture.Request, failedOutput string, agent Agent) (io.ReadCloser, error) {
	sys := FollowupPrompt(req, failedOutput)
	user := BuildUserMessage(req)
	return agent(sys, user)
}

// FollowupPrompt assembles the follow-up system prompt, faithfully porting the
// heredoc in ai-assist-followup: original request + project + failed command +
// its output + the "try a DIFFERENT fix; keep a separate verify block" task.
func FollowupPrompt(req capture.Request, failedOutput string) string {
	userRequest := req.UserRequest
	if userRequest == "" {
		userRequest = "<unknown>"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "(unknown)"
	}
	failedCmd := req.Command
	if failedCmd == "" {
		failedCmd = "<unknown>"
	}
	verifyCmd := req.Command
	if verifyCmd == "" {
		verifyCmd = "<command>"
	}

	var b strings.Builder
	b.WriteString("You are a concise technical assistant helping debug a terminal fix that did not work.\n\n")
	fmt.Fprintf(&b, "## Original request\n%s\n\n", userRequest)
	fmt.Fprintf(&b, "## Project\n%s\n\n", projectRoot)
	fmt.Fprintf(&b, "## Failed command (the verify step that failed)\n```\n%s\n```\n\n", failedCmd)
	if failedOutput != "" {
		fmt.Fprintf(&b, "## Output of the failed command\n```\n%s\n```\n\n", failedOutput)
	} else {
		b.WriteString("## Output of the failed command\n(no output captured)\n\n")
	}
	b.WriteString("## Your task\n")
	b.WriteString("Your previous fix did NOT work — the step above failed.\n")
	b.WriteString("Diagnose WHY from the command and its output above, then propose a DIFFERENT,\n")
	b.WriteString("corrected fix. Do not repeat the failed approach.\n\n")
	b.WriteString("Write your answer as a literate troubleshooting playbook with:\n")
	b.WriteString("  1. A brief diagnosis: why the previous fix failed.\n")
	b.WriteString("  2. A fresh fix block, with a NEW approach, tagged exactly `{id=fix}`.\n")
	b.WriteString("  3. A SEPARATE verify block tagged exactly `{id=verify needs=fix}` that re-runs\n")
	fmt.Fprintf(&b, "     the original failed command exactly: `%s`\n", verifyCmd)
	b.WriteString("     so the runner can detect success (exit 0) or offer another follow-up.\n\n")
	b.WriteString("BLOCK TAGGING — REQUIRED. Tag a fenced code block by appending the tag to its\n")
	b.WriteString("language line, exactly like this:\n")
	b.WriteString("    ```bash {id=fix}\n    <your corrected commands>\n    ```\n")
	fmt.Fprintf(&b, "    ```bash {id=verify needs=fix}\n    %s\n    ```\n", verifyCmd)
	b.WriteString("The ids `fix` and `verify` are MANDATORY and must be spelled EXACTLY — the\n")
	b.WriteString("runner keys success detection, the \"did this solve it?\" confirmation, and any\n")
	b.WriteString("further follow-ups on them. Untagged blocks get auto-named ids and silently\n")
	b.WriteString("break that, so always include the {id=...} tag.\n\n")
	b.WriteString("EMIT THE ACTUAL BLOCKS. Write the real commands inside the fix and verify code\n")
	b.WriteString("blocks — the USER runs them. Use the `run` tool ONLY to diagnose; do NOT apply\n")
	b.WriteString("the fix yourself via `run` and then merely describe what you did. A reply with\n")
	b.WriteString("no runnable {id=fix}/{id=verify} blocks is a failure.\n\n")
	b.WriteString("Be concise. Do not re-diagnose the original error; focus on why the fix did not work.\n")
	return b.String()
}

// Wrapup is the Go port of ai-assist-wrapup: the `w`-key session wrap-up. The
// agent verifies whether the original request now appears resolved given the run
// outcomes, writes a reusable `## Solution` section, and (when resolved) records
// one durable fact. The CUSTOM system prompt (verify + `## Solution` framing)
// REPLACES the standard authoring prompt — like the shell's --prompt-file pass.
//
// runlog is the raw run log (the shell catted $run_dir/runlog.jsonl; absent is
// OK). The user message is the assembled request context. The KB-write and
// solution-artifact side effects are the orchestrator's concern (so this function
// stays pure/injectable); here we only build the prompt + stream.
func Wrapup(req capture.Request, runlog string, agent Agent) (io.ReadCloser, error) {
	sys := WrapupPrompt(req, runlog)
	user := BuildUserMessage(req)
	return agent(sys, user)
}

// WrapupPrompt assembles the wrap-up system prompt, faithfully porting the
// heredoc in ai-assist-wrapup: original request + project root + the blocks the
// user ran (the run log) + the (1) resolved? (2) `## Solution` (3) remember-once
// task. The verify framing and the `## Solution` section header are preserved.
func WrapupPrompt(req capture.Request, runlog string) string {
	userRequest := req.UserRequest
	if userRequest == "" {
		userRequest = "<unknown>"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "(unknown)"
	}

	var b strings.Builder
	b.WriteString("You are a concise technical assistant wrapping up a terminal debugging session.\n\n")
	fmt.Fprintf(&b, "## Original request\n%s\n\n", userRequest)
	fmt.Fprintf(&b, "## Project root\n%s\n\n", projectRoot)
	if strings.TrimSpace(runlog) != "" {
		b.WriteString("## Blocks the user ran (id / exit code / timestamp)\n")
		fmt.Fprintf(&b, "%s\n\n", runlog)
		b.WriteString("Exit code 0 = success; non-zero = failure.\n\n")
	} else {
		b.WriteString("## Blocks the user ran\nNo blocks were run in this session.\n\n")
	}
	b.WriteString("## Your task\n")
	b.WriteString("The verify step passed (exit 0), so the fix LOOKS resolved. Confirm it with the\n")
	b.WriteString("user before declaring success — a green verify is not always the user's\n")
	b.WriteString("definition of \"solved\".\n\n")
	b.WriteString("(1) In 1-2 lines, state whether the original request now appears RESOLVED\n")
	b.WriteString("    given the run outcomes above.\n")
	b.WriteString("(2) ASK the user to confirm, using the ask tool with the confirm type:\n")
	b.WriteString("      ai-assist-ask --type confirm \"Did this solve your problem?\"\n")
	b.WriteString("    This is the only way to know whether the fix actually solved THEIR problem.\n")
	b.WriteString("(3) ONLY IF the user confirms (answers yes):\n")
	b.WriteString("      - write a short `## Solution` section a teammate could reuse later, and\n")
	b.WriteString("      - call `ai-assist-remember` exactly once with one durable, reusable fact\n")
	b.WriteString("        about this project — something that would help a future session. Example:\n")
	b.WriteString("          ai-assist-remember \"The test suite requires PGPASSWORD=test to be set.\"\n")
	b.WriteString("(4) IF the user says NO (it did not solve their problem): do NOT write a\n")
	b.WriteString("    `## Solution` and do NOT call ai-assist-remember. Instead, briefly\n")
	b.WriteString("    acknowledge it and propose a DIFFERENT next fix to try (a fresh fix block\n")
	b.WriteString("    plus a {id=verify needs=<fix-id>} block), handing back to another attempt\n")
	b.WriteString("    rather than declaring the session solved.\n")
	b.WriteString("\n")
	b.WriteString("Be concise: this is a wrap-up, not a new investigation. Do not re-diagnose.\n")
	return b.String()
}
