package author

import (
	"strings"
	"testing"
)

func TestStructuredToolInstruction_MandatesSubmit(t *testing.T) {
	s := StructuredToolInstruction()
	for _, want := range []string{
		"submit_playbook", // names the tool
		"project_bound",   // explains the gating bool
		"do NOT write",    // forbids markdown output
		"callout",         // explains content kinds
		"admonition",      // names the callout-type field
		"warning",         // lists at least one callout type
		"caution",         // …and another
		"verify",          // explains the verify field
		"$PROJECT_ROOT",   // portability guidance
		"$HOME",           // portability guidance
		"do not hardcode", // portability guidance
		"meta.env",        // portability guidance
	} {
		if !strings.Contains(s, want) {
			t.Errorf("structured instruction missing %q", want)
		}
	}
	// It must NOT instruct the old "{id=fix}/{id=verify} fenced code blocks" markdown.
	if strings.Contains(s, "fenced code blocks") {
		t.Errorf("structured instruction must not ask for markdown fenced blocks")
	}
}
