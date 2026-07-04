// validate.go — shared render-validity predicate used by the playbook
// renderer's tests (internal/playbook/render_test.go) and formerly by the
// adapt-on-run junk-guard.
package ui

// ValidatePlaybook reports whether md is a REAL final playbook (an H1 title AND at
// least one runnable code block) rather than a narration. It is the exported
// wrapper the adapt-on-run junk-guard (internal/launcher) uses to decide whether a
// freshly adapted document is safe to display: it REUSES the unexported
// isValidPlaybook predicate and the same block-count rule the stream-EOF final-draft
// guard uses (countBlocks — a cheap goldmark parse, no styling) rather than
// reimplementing the check, so the definition of "a valid playbook" stays single-sourced.
func ValidatePlaybook(md string) bool {
	return isValidPlaybook(md, countBlocks(md))
}
