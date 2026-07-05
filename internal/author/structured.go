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
		"  - `{kind:\"code\", lang, code, id?, needs?, rollback?, static?}` — a block. Mark non-runnable " +
		"output (console transcripts, illustrations) `static:true`. Use `id`+`needs` for value-passing " +
		"between runnable blocks (omit `id` to have one assigned).\n" +
		"    File-change blocks: to **edit an existing file**, use a diff block (set `lang:\"diff\"` with a " +
		"unified patch). To **create a new file**, set `file:<relative path>` alongside the target `lang`; " +
		"the body is the new file's FULL content. Use `file=` ONLY for files that don't exist yet — " +
		"edit existing files with a diff block.\n" +
		"- `verify`: the final outcome-check command — include it for a fix/troubleshooting playbook.\n" +
		"- `meta`: `description` (one line), `category`, `tags`, and `project_bound`. Set " +
		"`project_bound:true` when the playbook is specific to a project/working directory; `false` for " +
		"a general how-to that applies anywhere.\n" +
		"Interleave prose and code freely inside a section (intro prose, a block, closing prose). " +
		"END the playbook with a short wrap-up section (e.g. a `Summary` or `Done` heading) that " +
		"confirms the successful end state and any follow-ups — do not stop abruptly after the last command.\n" +
		"\n### Portability\n" +
		"Reference machine- or project-specific local resources through shell variables, " +
		"do not hardcode absolute paths: use `$PROJECT_ROOT` for anything under the project " +
		"directory (the host sets it at run), `$HOME` for home paths, and the standard tool " +
		"variables (`$ANDROID_SDK_ROOT`, `$JAVA_HOME`, …) for SDK/tool locations. Declare each " +
		"non-standard variable the playbook relies on in `meta.env` (name + why) so a reader on " +
		"another machine knows what to set.\n"
}
