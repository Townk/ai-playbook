package author

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
)

// TestSystemPrompt_FileChangeVocabulary asserts that SystemPrompt names both the
// diff block (edit existing) and the file= block (create new) vocabularies, and
// that it articulates the new-vs-edit distinction. Mirrors
// TestStructuredToolInstruction_FileChangeVocabulary for the markdown prompt path.
func TestSystemPrompt_FileChangeVocabulary(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "", "zsh")
	for _, want := range []string{"file=<path>", "file=", "new file", "diff block"} {
		if !strings.Contains(sys, want) {
			t.Errorf("SystemPrompt missing file-change vocabulary %q", want)
		}
	}
}

// Both authoring kinds must mandate the `# Playbook — <task>` H1 as the first
// line (no conversational preamble) — the viewer's preamble-strip + title
// extraction key off that H1, and its absence produced a malformed render.
func TestSystemPrompt_MandatesPlaybookH1Title(t *testing.T) {
	cases := []struct {
		name string
		req  capture.Request
	}{
		{"troubleshooting", sampleFailure()},
		{"how-to", capture.Request{Kind: "question", UserRequest: "set up CI"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := SystemPrompt(tc.req, "", "", "zsh")
			if !strings.Contains(sys, "# Playbook — <short task>") {
				t.Errorf("%s prompt must mandate the `# Playbook — <task>` H1 title", tc.name)
			}
			if !strings.Contains(sys, "VERY FIRST line") {
				t.Errorf("%s prompt must require the title as the very first line", tc.name)
			}
			if !strings.Contains(sys, "no conversational preamble") {
				t.Errorf("%s prompt must forbid a conversational preamble before the title", tc.name)
			}
		})
	}
}
