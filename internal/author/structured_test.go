package author

import (
	"strings"
	"testing"
)

// TestStructuredToolInstruction_FileChangeVocabulary asserts the standalone
// addendum still teaches the file-change MECHANICS of the playbook object (the
// JSON encoding: `file:` for a create, `lang:"diff"` for an edit). The file-change
// RATIONALE (why file=/diff, "diff block" discipline) lives in the shared
// authoringRubric, which arrives via SystemPrompt in the composed prompt — see
// TestStructuredToolInstruction_ComposedCarriesRubricGuidance.
func TestStructuredToolInstruction_FileChangeVocabulary(t *testing.T) {
	for _, want := range []string{"lang:\"diff\"", "file:", "new file"} {
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

// TestStructuredToolInstruction_MandatesSubmit pins the GENUINE submit mechanics
// the standalone addendum owns: the submit_playbook contract and the
// playbook-object schema. Quality-bar guidance (portability, env:, diff/file
// discipline) is NOT asserted here — it arrives via SystemPrompt's rubric in the
// composed prompt (see TestStructuredToolInstruction_ComposedCarriesRubricGuidance).
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

// TestStructuredToolInstruction_ComposedCarriesRubricGuidance asserts the
// COMPOSED structured prompt (SystemPrompt + StructuredToolInstruction, the shape
// RunHarnessEvents sends) still teaches the portability/env/diff quality bar that
// the standalone addendum used to restate — it now arrives once, via the shared
// rubric embedded in SystemPrompt.
func TestStructuredToolInstruction_ComposedCarriesRubricGuidance(t *testing.T) {
	composed := SystemPrompt(sampleFailure(), "", "", "zsh") + StructuredToolInstruction()
	for _, want := range []string{
		"$PROJECT_ROOT", // portability guidance (via the shared rubric)
		"$HOME",         // portability guidance (via the shared rubric)
		"env:",          // env-declaration guidance (via the shared rubric)
		"diff block",    // edit-with-a-diff discipline (via the shared rubric)
		"rollback",      // rollback discipline (via the shared rubric)
	} {
		if !strings.Contains(composed, want) {
			t.Errorf("composed structured prompt missing rubric guidance %q", want)
		}
	}
}
