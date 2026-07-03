// Package author is the Go port of the producer's LLM half: it assembles the
// standing system prompt (the literate-playbook authoring instructions) and the
// per-request user message, runs the capable agent (claude, headless), and
// returns the agent's stdout STREAM so the ui can render it incrementally.
//
// It mirrors the shell producer in assist-agent-common.zsh:
//   - assist::system_prompt → SystemPrompt (the standing authoring prompt)
//   - the REQ_* → user-message assembly that the shell folds into the prompt is
//     ported here as BuildUserMessage (in claude.go we pass the system prompt and
//     the user message as two arguments rather than concatenating, matching the
//     `claude --print … "<prompt>"` invocation shape).
//   - assist_build_cmd / ASSIST_PANE_CMD (ai-assist-claude) → the harness invocation
//
// Fidelity note: the shell builds ONE prompt string (system instructions WITH the
// request context interpolated) and passes it as claude's positional prompt arg.
// Here the standing authoring instructions are SystemPrompt and the request
// context is BuildUserMessage; the owned harness invocation passes them as
// --append-system-prompt + positional prompt so claude sees the same total information.
package author

import (
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/capture"
)

// shellGuidance returns the shell-specific line(s) to prepend to the run-block
// guidance section of the authoring prompt. It identifies the executing shell
// for the model so it produces syntactically appropriate run blocks.
//
//   - "sh": POSIX-only instructions — warns against bash/zsh extensions.
//   - "bash" / "zsh": single identification line; extensions are available.
//   - anything else: empty (no shell claim; portable guidance still applies).
//
// The returned string is either empty or ends with "\n" so it can be
// concatenated directly before the universal set -e guidance.
func shellGuidance(shell string) string {
	switch shell {
	case "sh":
		return "Shell blocks execute under `sh` (POSIX shell). Use only POSIX-compatible syntax:\n" +
			"`[ ]` for tests (NOT `[[ ]]`), `printf` (NOT `print`), `$(…)` for command\n" +
			"substitution; avoid bash/zsh extensions (no arrays, no process substitution\n" +
			"`<(…)`, no `${var@Q}`).\n"
	case "bash":
		return "Shell blocks execute under `bash`.\n"
	case "zsh":
		return "Shell blocks execute under `zsh`.\n"
	default:
		return ""
	}
}

// KnowledgeBase is the per-project distilled-facts block the shell interpolates
// as "## What we already know about this project". Author loads it from disk via
// kb.Load (the Go port of assist::kb_path/kb_ensure) keyed on req.ProjectRoot;
// SystemPrompt folds it in verbatim, exactly like assist::system_prompt did with
// $kb_path. The package kb type and this one carry the same text.
//
// NOTE(stage 4c-i): only the KB READ path is wired. The KB WRITE/remember path
// (the remember tool appending a distilled fact) is DEFERRED to a later stage.
type KnowledgeBase string

// SystemPrompt assembles the standing literate-playbook authoring prompt for the
// given request, faithfully porting assist::system_prompt. The text is verbatim
// from the shell heredoc; the failure-vs-general branch, the {id/needs}/{static}
// block schema, the $APB_OUT/$APB_ERR/$APB_EXIT value-passing refs, and the C3a
// "verify re-runs the original failed command in a SEPARATE block" instruction
// are all preserved.
//
// B8: the failed command, scrollback, and the user's request are NOT
// interpolated here — they live ONLY in BuildUserMessage's output. Every
// authoring/followup/final call sends both SystemPrompt and BuildUserMessage
// (as --append-system-prompt + the positional prompt), so duplicating that
// context in both paid its token cost twice for no benefit. SystemPrompt keeps
// the standing rules/format sections (the block schema, verify-fold-in rule,
// shell guidance, etc.) plus the small project/kind/KB context; the per-request
// content is a one-line pointer to "read it in the user message" instead.
//
// shell is the resolved executing-shell name ("zsh", "bash", or "sh") — pass the
// result of driver.ResolveShellName(cfg.Driver.Shell). When shell is "sh" the
// prompt adds POSIX-only restrictions so the model avoids bash/zsh extensions.
// For "bash"/"zsh" it names the shell without extra restrictions. For an empty or
// unknown value no shell identification is emitted (safe fallback).
//
// kb is the optional knowledge-base block (empty when none — see KnowledgeBase).
func SystemPrompt(req capture.Request, kb KnowledgeBase, shell string) string {
	// Project display fields mirror the shell's REQ_* fallbacks.
	projectName := req.Project.Name
	if projectName == "" {
		projectName = req.ProjectRoot
	}
	if projectName == "" {
		projectName = "unknown"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "?"
	}
	branchSuffix := ""
	if req.Project.Branch != "" {
		branchSuffix = " on branch " + req.Project.Branch
	}

	kbBlock := ""
	if strings.TrimSpace(string(kb)) != "" {
		kbBlock = "\n\n## What we already know about this project\n\n" + string(kb)
	}

	// Failure vs general request. ONLY a non-zero last-command exit is a failure
	// to diagnose. A successful or absent last command means this is a general
	// question — there is almost always *some* last command, so do NOT frame a
	// general request as troubleshooting or invent an error from the last command.
	isFailure := req.Exit != "" && req.Exit != "0"

	var taskLine, structure string
	if isFailure {
		taskLine = "Diagnose the failure: explain what is going on and how to fix it."
		structure = "BEGIN the document with a single H1 title line — exactly `# Playbook — <short task>` — " +
			"as the VERY FIRST line. Do NOT write any conversational preamble before it (no \"Here's the picture…\", " +
			"no \"Everything's clear now\"). Everything after the title is the playbook body.\n\n" +
			"Write the body as a LITERATE TROUBLESHOOTING PLAYBOOK — a document a teammate\n" +
			"without the full context can follow — in three parts (as `##` sections under the title):\n\n" +
			"1. Goal & error — what the user was trying to do and the error they saw (concise).\n" +
			"2. Why it happens — the root cause (concise).\n" +
			"3. Fix steps — prose that walks through the fix, with the runnable steps woven in\n" +
			"   as fenced code blocks. Do NOT just dump a list of commands.\n\n" +
			"VERIFY (outcome-check): after the fix block, ALWAYS add a SEPARATE final block\n" +
			"tagged {id=verify needs=<fix-id>} whose only job is to re-run the original failed\n" +
			"command exactly as given in the accompanying user message — a clean exit (0) is\n" +
			"the proof the fix worked. Use the literal id `verify` so the runner can detect a\n" +
			"failed verification and offer to try another fix. Do NOT fold the re-run into the fix block or prose."
	} else {
		taskLine = "Answer the user's request directly. This is a general request, NOT a troubleshooting case: there is no failure here — do NOT invent or diagnose an error, and do NOT treat the last command as a problem."
		structure = "BEGIN the document with a single H1 title line — exactly `# Playbook — <short task>` — " +
			"as the VERY FIRST line. Do NOT write any conversational preamble before it (no \"Here's how…\", " +
			"no \"Sure, you can…\"). Everything after the title is the playbook body.\n\n" +
			"Write the body as a LITERATE HOW-TO PLAYBOOK — a document a teammate can\n" +
			"follow — in two parts (as `##` sections under the title):\n\n" +
			"1. Goal — what the user wants to accomplish (one line).\n" +
			"2. How — prose that walks through it, with the runnable steps woven in as fenced\n" +
			"   code blocks. Do NOT just dump a list of commands."
	}

	kind := req.Kind
	if kind == "" {
		kind = "question"
	}

	return fmt.Sprintf(`You are a terminal assistant helping with a single, self-contained request.

Work within a bounded context: rely only on the information below plus a
focused look at the project — do not crawl the whole repository or restate
history. The goal is one fresh, tightly-scoped pass that ends in a clear answer.

Project: %s (%s)%s
Request kind: %s

The user's request — and, for a failure, the failed command and the captured
terminal output — are given in the ACCOMPANYING USER MESSAGE, not here; read
them there. This system prompt carries only the standing rules below.%s

%s

%s

Each runnable step is a fenced code block. EVERY runnable block MUST carry a
unique short id, e.g. a bash block tagged {id=fix} — the runner keys run/diff/
apply, output capture, and needs-gating on that id. Use:
  - bash/sh/zsh blocks for shell steps (the user can run them in their shell or
    the assistant's),
  - python/node/etc. blocks for scripts,
  - diff blocks for file changes (the user views/applies them). A diff block
    MUST be a complete, applyable unified diff — include the `+"`--- a/<path>`"+`
    and `+"`+++ b/<path>`"+` file headers and at least one `+"`@@ … @@`"+` hunk header,
    with paths relative to the project root (a leading
    `+"`diff --git a/<path> b/<path>`"+` line is ideal). It must be valid for
    `+"`git apply`"+`. Do NOT emit a bare fragment of changed lines, and do NOT put
    the target filename only in prose — the file headers ARE how the viewer and
    apply know the target.
  - `+"`file=<path>`"+` blocks to CREATE a new file — the fenced block body is the new
    file's FULL content, and the `+"`file=<path>`"+` annotation names the target (relative
    to the project root). Use `+"`file=`"+` ONLY for files that do not exist yet — to
    edit an existing file use a diff block instead.
%sShell blocks run under `+"`set -e`"+`: a block FAILS at its FIRST failing command, so
a later command cannot mask an earlier failure. If a non-zero exit is expected
(a probe like `+"`command -v foo`"+` or `+"`grep …`"+`), guard it with `+"`|| true`"+`.
If a step uses a previous step's output, tag it {id=next needs=fix} and reference
the earlier output via $APB_OUT_fix / $APB_ERR_fix / $APB_EXIT_fix.
Show captured error output or sample output as a console block (or tag it
{static}) so it is NOT treated as runnable.
For example, an illustrative block starts with: `+"```"+`console {static}

Do NOT apply changes yourself — the user reviews and runs each step from the
playbook. The document MUST begin with the `+"`# Playbook — <task>`"+` H1 title as its
first line (no conversational preamble before it); spend your words on the steps.

Never write secrets, credentials, or raw environment dumps into a remembered
fact or into your answer.

Finish with a short summary and the recommended next step.
`,
		projectName, projectRoot, branchSuffix,
		kind,
		kbBlock,
		taskLine,
		structure,
		shellGuidance(shell),
	)
}

// BuildUserMessage assembles the per-request user message from the captured
// Request, mirroring how the shell folds the REQ_* context into the prompt: the
// failed command + exit, the "Relevant terminal output (the failure)" block, the
// user's request text, and the project/branch line. For a non-failure request it
// omits the failure framing (matching assist::system_prompt's general branch).
//
// In the shell this context lived INSIDE the single prompt string; here it is the
// claude positional prompt (the "user message") while SystemPrompt carries the
// standing instructions — together they convey the same information.
func BuildUserMessage(req capture.Request) string {
	var b strings.Builder

	projectName := req.Project.Name
	if projectName == "" {
		projectName = req.ProjectRoot
	}
	if projectName == "" {
		projectName = "unknown"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "?"
	}
	fmt.Fprintf(&b, "Project: %s (%s)", projectName, projectRoot)
	if req.Project.Branch != "" {
		fmt.Fprintf(&b, " on branch %s", req.Project.Branch)
	}
	b.WriteString("\n")

	isFailure := req.Exit != "" && req.Exit != "0"
	if isFailure {
		fmt.Fprintf(&b, "Failed command: `%s` (exit %s)\n", req.Command, req.Exit)
	}

	b.WriteString("\nWhat the user is trying to do:\n")
	if req.UserRequest != "" {
		b.WriteString(req.UserRequest)
	} else {
		b.WriteString("(no description given)")
	}
	b.WriteString("\n")

	if isFailure {
		scroll := req.Scrollback
		if scroll == "" {
			scroll = "(none captured)"
		}
		b.WriteString("\nRelevant terminal output (the failure):\n")
		b.WriteString(scroll)
		b.WriteString("\n")
	}

	return b.String()
}
