package author

import (
	"strings"
	"testing"
)

func TestStructuredToolInstruction_FileChangeVocabulary(t *testing.T) {
	for _, want := range []string{"lang:\"diff\"", "file:", "new file", "diff block"} {
		if !strings.Contains(StructuredToolInstruction(), want) {
			t.Errorf("structured instruction missing file-change vocabulary %q", want)
		}
	}
}

// TestStructuredToolInstruction_TeachesRememberClassification asserts the
// structured tool instruction teaches the kind taxonomy (K2): lessons are
// classified by closeness to the topic at hand across the four kinds.
func TestStructuredToolInstruction_TeachesRememberClassification(t *testing.T) {
	s := StructuredToolInstruction()
	for _, want := range []string{
		"`kind`",
		"topic at hand",
		"`system`",
		"`user`",
		"`environment`",
		"`topic`",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("structured instruction missing remember-classification guidance %q", want)
		}
	}
}

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
