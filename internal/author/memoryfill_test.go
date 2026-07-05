package author

import (
	"strings"
	"testing"
)

// memoryFillSentinel is a stable substring of the wrap-up memory-fill instruction
// the goldens pin: present in the wrap-up prompt, absent everywhere else.
const memoryFillSentinel = "before you finish, save the session's durable lessons"

// WithMemoryFill appends the memory-fill instruction; the sentinel + the taxonomy
// kinds + the secrets rule must all be present, and the base prompt must survive
// verbatim as a prefix (the fold is purely additive).
func TestWithMemoryFill_AddsInstruction(t *testing.T) {
	base := FinalPlaybookPrompt(sampleFailure(), "", "resolved", "", "")
	got := WithMemoryFill(base)

	if !strings.HasPrefix(got, base) {
		t.Fatalf("WithMemoryFill must append to the base prompt, not rewrite it")
	}
	wants := []string{
		memoryFillSentinel,
		"remember", // the tool name
		"system",   // the taxonomy kinds
		"user",
		"environment",
		"topic",
		"secrets", // the never-secrets rule stays
	}
	lower := strings.ToLower(got)
	for _, w := range wants {
		if !strings.Contains(lower, strings.ToLower(w)) {
			t.Errorf("memory-fill instruction missing %q\n--- prompt ---\n%s", w, got)
		}
	}
}

// The instruction is ABSENT from the plain wrap-up prompt and from the other
// authoring-shaped prompts — only WithMemoryFill (the MCP-wired wrap-up path) adds it.
func TestWithMemoryFill_AbsentFromPlainPrompts(t *testing.T) {
	req := sampleFailure()
	plain := []struct {
		name string
		sys  string
	}{
		{"finalplaybook_fresh", FinalPlaybookPrompt(req, "", "resolved", "", "")},
		{"finalplaybook_amend", FinalPlaybookPrompt(req, "# Playbook — base\n", "change", "", "")},
		{"authoring", SystemPrompt(req, "", "", "zsh")},
		{"followup", FollowupPrompt(req, "boom", "", "")},
	}
	for _, tc := range plain {
		if strings.Contains(strings.ToLower(tc.sys), memoryFillSentinel) {
			t.Errorf("%s prompt must NOT carry the memory-fill instruction:\n%s", tc.name, tc.sys)
		}
	}
}
