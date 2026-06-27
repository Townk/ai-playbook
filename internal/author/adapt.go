// adapt.go — the adapt-on-run authoring prompt (Task 9). AdaptPrompt instructs the
// authoring model to faithfully REWRITE a saved playbook so it applies to a
// specific target directory (its paths, versions, and project-specifics), emitting
// a VALID playbook (an H1 title + runnable blocks) that reproduces the full
// document. The launcher (internal/launcher) makes ONE authoring-model call with
// this system prompt and the original playbook body as the user message, then
// junk-guards the result before display.
//
// Note: this file imports internal/store for store.Meta. That is safe — store does
// NOT import author (store depends only on config/capture/frontmatter), so there is
// no import cycle.
package author

import (
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/store"
)

// AdaptPrompt assembles the adapt-on-run system prompt. meta is the saved
// playbook's metadata (name/description/its original workdir) and targetDir is the
// directory the user is running it in NOW — the playbook must be rewritten to apply
// THERE. The original playbook body is supplied separately as the user message
// (the launcher passes it), so this prompt only carries the instructions + context.
func AdaptPrompt(meta store.Meta, targetDir string) string {
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSpace(meta.Slug)
	}
	if name == "" {
		name = "<unknown>"
	}
	target := strings.TrimSpace(targetDir)
	if target == "" {
		target = "(the current directory)"
	}
	origin := strings.TrimSpace(meta.Workdir)
	if origin == "" {
		origin = "(unknown / unspecified)"
	}

	var b strings.Builder
	b.WriteString("You are a concise technical assistant adapting a reusable Literate-Config\n")
	b.WriteString("playbook so it applies to a SPECIFIC target project directory.\n\n")

	fmt.Fprintf(&b, "## Playbook\n%s\n\n", name)
	if d := strings.TrimSpace(meta.Description); d != "" {
		fmt.Fprintf(&b, "## What it does\n%s\n\n", d)
	}
	fmt.Fprintf(&b, "## Original target directory\n%s\n\n", origin)
	fmt.Fprintf(&b, "## New target directory (adapt the playbook FOR this directory)\n%s\n\n", target)

	b.WriteString("## Your task\n")
	fmt.Fprintf(&b, "Rewrite the playbook (supplied as the user message) so it applies to %s.\n", target)
	b.WriteString("Adapt the project-specifics to the NEW target: file and directory PATHS, tool\n")
	b.WriteString("and dependency VERSIONS, package/module names, and any other detail that is tied\n")
	b.WriteString("to the original project. Keep the adaptation FAITHFUL: preserve the playbook's\n")
	b.WriteString("structure, intent, and the order of its steps — change ONLY what must change for\n")
	b.WriteString("the new directory. Do NOT invent new steps, drop existing ones, or turn the\n")
	b.WriteString("setup guide into a diagnosis or a debrief.\n\n")

	b.WriteString("OUTPUT A VALID PLAYBOOK. The result MUST be a real playbook, not a narration:\n")
	b.WriteString("it MUST begin with an H1 title line (`# <title>`) and MUST contain at least one\n")
	b.WriteString("runnable fenced code block. Keep the same {id=...} block-tag convention as the\n")
	b.WriteString("original, and keep the final verification block tagged exactly `{id=verify}` —\n")
	b.WriteString("spelled EXACTLY, the runner keys success detection on `{id=verify}`.\n\n")

	b.WriteString("REPRODUCE THE FULL DOCUMENT as your ENTIRE response — every line of prose and\n")
	b.WriteString("every runnable block, starting with the `# <title>` line. NEVER refer to a\n")
	b.WriteString("playbook \"above\", \"already shown\", or \"in the context\", and never merely\n")
	b.WriteString("acknowledge or summarize one: your response IS the playbook file and must stand\n")
	b.WriteString("entirely on its own. A reply that points at the original instead of reproducing\n")
	b.WriteString("the adapted document in full is a failure.\n")
	return b.String()
}
