package author

// StructuredToolInstruction is the system-prompt addendum for the STRUCTURED
// authoring path (the create flow). Unlike ToolInstruction (which asks the model
// to write `{id=fix}` markdown), it directs the model to diagnose with run/ask
// and then return the playbook as DATA via the submit_playbook tool. The host
// renders the markdown, so the model never formats it.
func StructuredToolInstruction() string {
	return "\n\n" +
		"## Diagnosing and submitting the playbook\n" +
		"You have MCP tools `run`, `remember`, `ask`, and `submit_playbook`.\n" +
		"- Use `run` ONLY to DIAGNOSE (reproduce the situation, inspect cwd/files/versions). " +
		"It runs in the USER's real shell — keep it READ-ONLY; do NOT apply changes with it.\n" +
		"- Use `ask` to get input from the user and `remember` for a durable project fact.\n" +
		"- Your DELIVERABLE is a single `submit_playbook` call. do NOT write the playbook as " +
		"markdown in your reply — call `submit_playbook` with the structured object. The host " +
		"renders the markdown deterministically.\n" +
		"\n" +
		"### The playbook object\n" +
		"- `title`: a short imperative name (rendered as the H1).\n" +
		"- `intro`: optional lead prose.\n" +
		"- `sections[]`: each has a `heading` and an ordered `content[]`. Each content item is one of:\n" +
		"  - `{kind:\"text\", text}` — literate prose (markdown).\n" +
		"  - `{kind:\"callout\", text}` — a note/warning (rendered as a blockquote).\n" +
		"  - `{kind:\"code\", lang, code, id?, needs?, rollback?, static?}` — a block. Mark non-runnable " +
		"output (console transcripts, illustrations) `static:true`. Use `id`+`needs` for value-passing " +
		"between runnable blocks (omit `id` to have one assigned).\n" +
		"- `verify`: the final outcome-check command — include it for a fix/troubleshooting playbook.\n" +
		"- `meta`: `description` (one line), `category`, `tags`, and `project_bound`. Set " +
		"`project_bound:true` when the playbook is specific to a project/working directory; `false` for " +
		"a general how-to that applies anywhere.\n" +
		"Interleave prose and code freely inside a section (intro prose, a block, closing prose).\n"
}
