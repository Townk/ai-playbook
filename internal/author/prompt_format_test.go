package author

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
)

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
			sys := SystemPrompt(tc.req, "", "zsh")
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
