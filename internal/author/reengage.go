package author

import (
	"fmt"
	"io"
	"strings"

	"github.com/Townk/ai-playbook/internal/capture"
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
