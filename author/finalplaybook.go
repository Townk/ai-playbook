package author

import (
	"fmt"
	"io"
	"strings"

	"ai-playbook/agentstream"
	"ai-playbook/capture"
)

// FinalPlaybookPrompt assembles the FINAL-PLAYBOOK system prompt (spec §B/§C/§D):
// it turns a RESOLVED troubleshoot into a clean, REUSABLE Literate-Config playbook
// — a forward-looking SETUP GUIDE, not a diagnosis/debrief. It has two modes,
// selected by base:
//
//   - base == "" → FRESH: distill the resolved session (context = the troubleshoot
//     content: the diagnosis + the fixes that worked) into a new playbook. Cover the
//     prerequisites/configuration that must be in place to make this work FROM
//     SCRATCH, each as short prose + a runnable check/set block, in dependency
//     order, ending with a final {id=verify} block.
//   - base != "" → AMEND: given the base (the current/served playbook) + context
//     (the CHANGE to integrate — a resolved fix, or a user's free-form `f` request),
//     output the FULL UPDATED playbook with the change folded in in dependency
//     order, PRESERVING the existing steps (do not rewrite wholesale or drop
//     content).
//
// Both modes MANDATE the block-tag convention: the final verification block is
// tagged exactly `{id=verify}` (spelled exactly — the runner keys success detection
// on it), fix-style blocks `{id=fix}` / unique ids. The tagging syntax is shown
// (```bash {id=verify}) like FollowupPrompt does. Empty req fields fall back to the
// <unknown>/(unknown) placeholders the other prompts use.
func FinalPlaybookPrompt(req capture.Request, base, context string) string {
	task := req.UserRequest
	if task == "" {
		task = req.Project.Name
	}
	if task == "" {
		task = req.ProjectRoot
	}
	if task == "" {
		task = "<unknown>"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "(unknown)"
	}
	if strings.TrimSpace(context) == "" {
		context = "(none provided)"
	}

	amend := strings.TrimSpace(base) != ""

	var b strings.Builder
	if amend {
		b.WriteString("You are a concise technical assistant maintaining a reusable Literate-Config playbook.\n\n")
	} else {
		b.WriteString("You are a concise technical assistant authoring a reusable Literate-Config playbook.\n\n")
	}
	fmt.Fprintf(&b, "## Task\n%s\n\n", task)
	fmt.Fprintf(&b, "## Project\n%s\n\n", projectRoot)

	if amend {
		fmt.Fprintf(&b, "## Base playbook (the current, served playbook)\n%s\n\n", base)
		fmt.Fprintf(&b, "## The change to integrate\n%s\n\n", context)
		b.WriteString("## Your task\n")
		b.WriteString("Integrate the change above into the base playbook and output the FULL UPDATED\n")
		b.WriteString("playbook. PRESERVE the existing steps — do not rewrite the document wholesale\n")
		b.WriteString("and do not drop content; add or adjust only what the change requires, slotting\n")
		b.WriteString("any new prerequisite/step into the correct place in DEPENDENCY ORDER.\n")
		b.WriteString("The result is still a reusable SETUP GUIDE (a Literate-Config playbook), NOT a\n")
		b.WriteString("diagnosis or a debrief — fold the change in as a configuration requirement.\n\n")
		b.WriteString("Keep the title `# Playbook — <task>`. Keep the same {id=...} block-tag\n")
		b.WriteString("convention as the base, and keep the final verification block tagged exactly\n")
		b.WriteString("`{id=verify}`.\n\n")
	} else {
		b.WriteString("## What you resolved (the troubleshoot content — diagnosis + the fixes that worked)\n")
		fmt.Fprintf(&b, "%s\n\n", context)
		b.WriteString("## Your task\n")
		b.WriteString("You resolved a problem. Write a REUSABLE Literate-Config playbook — NOT a\n")
		b.WriteString("debrief or a diagnosis. Assume a reader starting FROM SCRATCH who wants this\n")
		b.WriteString("working. It is a SETUP GUIDE: fold the root causes you just fixed in as\n")
		b.WriteString("configuration requirements.\n\n")
		b.WriteString("Begin with the title `# Playbook — <task>` (derive <task> from the task above\n")
		b.WriteString("and the project). Then cover the prerequisites and configuration that must be\n")
		b.WriteString("in place for this to work from scratch, each as a short prose explanation\n")
		b.WriteString("followed by a runnable check/set block, in DEPENDENCY ORDER. End with a final\n")
		b.WriteString("verification block that confirms the whole setup works.\n\n")
	}

	b.WriteString("BLOCK TAGGING — REQUIRED. Tag a fenced code block by appending the tag to its\n")
	b.WriteString("language line, exactly like this:\n")
	b.WriteString("    ```bash {id=fix}\n    <a check/set step>\n    ```\n")
	b.WriteString("    ```bash {id=verify}\n    <the final whole-setup verification>\n    ```\n")
	b.WriteString("The final verification block MUST be tagged exactly `{id=verify}` — spelled\n")
	b.WriteString("EXACTLY, the runner keys success detection on `{id=verify}`. Earlier runnable\n")
	b.WriteString("blocks get `{id=fix}` or unique short ids. Untagged blocks get auto-named ids\n")
	b.WriteString("and silently break that, so always include the {id=...} tag.\n\n")
	b.WriteString("Be concise: spend your words on the steps, keep the prose tight.\n")
	return b.String()
}

// FinalPlaybook runs the FINAL-PLAYBOOK generation through the OWNED harness
// invocation (the same streaming path as AuthorEvents/Followup): it builds the
// system prompt via FinalPlaybookPrompt(req, base, context) and delegates to
// RunHarnessEvents, returning a channel of normalized agentstream.Events, a
// close/wait func that reaps the process, and a start error. When opts has an
// MCPConfigPath the tools backend is wired in, exactly like the standard path.
//
// User message: the standard BuildUserMessage(req). The load-bearing inputs (the
// base playbook + the change/context) live in the system prompt; BuildUserMessage
// supplies the same project/request framing the rest of the author path uses, so
// the harness sees a consistent request shape across fresh authoring, follow-up,
// and final-playbook generation.
//
// The returned func() error waits for the process to exit (reaping it); call it
// after draining the channel.
func FinalPlaybook(req capture.Request, base, context string, opts AuthorOptions) (<-chan agentstream.Event, func() error, error) {
	sys := FinalPlaybookPrompt(req, base, context)
	user := BuildUserMessage(req)
	return RunHarnessEvents(sys, user, opts)
}

// FinalPlaybookText is the text-path (io.ReadCloser) fallback for FinalPlaybook,
// mirroring Author/Followup: it builds the FINAL-PLAYBOOK system prompt via
// FinalPlaybookPrompt(req, base, context) and runs it through the injected text
// Agent. Used by the orchestrator when the owned event stream can't start (and in
// tests with a fake Agent). base=="" → fresh; base!="" → amend.
func FinalPlaybookText(req capture.Request, base, context string, agent Agent) (io.ReadCloser, error) {
	sys := FinalPlaybookPrompt(req, base, context)
	user := BuildUserMessage(req)
	return agent(sys, user)
}
