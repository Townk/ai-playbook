package author

import (
	"strings"
	"testing"
)

// TestAuthoringRubric_InBothPaths pins Surface 1's single-source contract at the
// COMPOSITION level: the exact authoringRubric fragment appears byte-identical and
// EXACTLY ONCE in each runtime authoring prompt, composed the way RunHarnessEvents
// assembles it — SystemPrompt + ToolInstruction (the markdown/tools path) and
// SystemPrompt + StructuredToolInstruction() (the structured create path). A count
// of zero means a path drifted off the shared quality bar; a count above one means
// a component re-embedded the fragment and the composed prompt pays its token cost
// twice (the B8-class defect this test exists to prevent).
func TestAuthoringRubric_InBothPaths(t *testing.T) {
	cases := []struct {
		name     string
		composed string
	}{
		{"markdown/failure", SystemPrompt(sampleFailure(), "", "", "zsh") + ToolInstruction},
		{"markdown/general", SystemPrompt(recallGeneralReq(), "", "", "zsh") + ToolInstruction},
		{"structured/failure", SystemPrompt(sampleFailure(), "", "", "zsh") + StructuredToolInstruction()},
		{"structured/general", SystemPrompt(recallGeneralReq(), "", "", "zsh") + StructuredToolInstruction()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := strings.Count(tc.composed, authoringRubric); n != 1 {
				t.Errorf("composed %s prompt must carry the authoringRubric fragment byte-identical EXACTLY once, got %d\n--- fragment ---\n%s\n--- composed ---\n%s", tc.name, n, authoringRubric, tc.composed)
			}
		})
	}
}

// TestAuthoringRubric_CoversNineRules asserts the fragment distills each of the
// nine rubric rules (docs/specifications/playbook-authoring.md) via keyword
// coverage: atomicity, file=-not-heredoc, diff-for-edits, rollback-per-mutating-
// step, verify-always-both-kinds, needs/from distinction, static, env+portability,
// and callouts. Substring assertions on the rubric keywords catch a rule silently
// dropped in a future edit.
func TestAuthoringRubric_CoversNineRules(t *testing.T) {
	cases := []struct {
		rule  string
		wants []string
	}{
		{"1 atomicity", []string{"one logical step per block"}},
		{"2 file=-not-heredoc", []string{"file=", "file=<path>", "heredoc"}},
		{"3 diff-for-edits", []string{"diff block", "unified diff", "paths relative to the project root"}},
		{"4 rollback-per-mutating-step", []string{"rollback", "MUTATES"}},
		{"5 verify-always-both-kinds", []string{"verify", "troubleshooting", "how-to"}},
		{"6 needs/from distinction", []string{"needs=", "from="}},
		{"7 static", []string{"static"}},
		{"8 env+portability", []string{"env:", "$PROJECT_ROOT", "$HOME"}},
		{"9 callouts", []string{"callout", "warning", "caution"}},
	}
	for _, tc := range cases {
		for _, w := range tc.wants {
			if !strings.Contains(authoringRubric, w) {
				t.Errorf("rubric rule %s: fragment missing keyword %q", tc.rule, w)
			}
		}
	}
}
