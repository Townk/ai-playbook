package author

// StructuredToolInstruction is the system-prompt addendum for the STRUCTURED
// authoring path (the create flow). Unlike ToolInstruction (which asks the model
// to write `{id=fix}` markdown), it directs the model to diagnose with run/ask
// and then return the playbook as DATA via the submit_playbook tool. The host
// renders the markdown, so the model never formats it.
//
// The authoring quality bar (authoringRubric, rubric.go) is NOT embedded here:
// this addendum is always APPENDED to SystemPrompt (RunHarnessEvents), which
// already carries the rubric — embedding it here too would send it twice. This
// function keeps only the submit_playbook mechanics (the tool contract and the
// playbook-object schema).
func StructuredToolInstruction() string {
	return "\n\n" +
		"## Diagnosing and submitting the playbook\n" +
		"You have MCP tools `run`, `remember`, `ask`, and `submit_playbook`.\n" +
		"- Use `run` ONLY to DIAGNOSE (reproduce the situation, inspect cwd/files/versions). " +
		"It runs in the USER's real shell — keep it READ-ONLY; do NOT apply changes with it.\n" +
		"- Use `ask` to get input from the user and `remember` to save a durable fact, " +
		"classified with `kind` by how closely it is tied to the topic at hand: `system` " +
		"for machine/tooling truths, `user` for who the user is or prefers, `environment` " +
		"for this project's setup, `topic` (with a `topic` name) for a domain-specific lesson.\n" +
		"- Your DELIVERABLE is a single `submit_playbook` call; do NOT write the playbook as " +
		"markdown in your reply — call `submit_playbook` with the structured object. The host " +
		"renders the markdown deterministically.\n" +
		"\n" +
		"### The playbook object\n" +
		"- `title`: a short imperative name (rendered as the H1).\n" +
		"- `intro`: optional lead prose.\n" +
		"- `sections[]`: each has a `heading` and an ordered `content[]`. Each content item is one of:\n" +
		"  - `{kind:\"text\", text}` — literate prose (markdown).\n" +
		"  - `{kind:\"callout\", text, admonition}` — a STYLED callout (icon + color). Set " +
		"`admonition` to one of `note`, `tip`, `important`, `warning`, `caution`. Use them " +
		"deliberately: `warning`/`caution` for risky or destructive steps, `important` for " +
		"must-knows, `tip` for shortcuts, `note` for asides.\n" +
		"  - `{kind:\"code\", lang, code, id?, needs?, rollback?, static?, file?}` — a block (omit `id` to " +
		"have one assigned). Set `file:<relative path>` on a create block (the body is the new file's FULL " +
		"content); set `lang:\"diff\"` on an edit block (a unified patch).\n" +
		"- `verify`: the final outcome-check command — include it for a fix/troubleshooting playbook.\n" +
		"- `meta`: `description` (one line), `category`, `tags`, and `project_bound`. Set " +
		"`project_bound:true` when the playbook is specific to a project/working directory; `false` for " +
		"a general how-to that applies anywhere.\n" +
		"Interleave prose and code freely inside a section (intro prose, a block, closing prose). " +
		"END the playbook with a short wrap-up section (e.g. a `Summary` or `Done` heading) that " +
		"confirms the successful end state and any follow-ups — do not stop abruptly after the last command.\n"
}
