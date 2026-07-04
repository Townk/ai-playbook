package author

import (
	"strings"
	"testing"
)

// WithConstraints is the session-constraints appender: it folds the user's
// rejected-approach reasons into a re-engagement prompt as a bounded section, or
// leaves the prompt untouched when there is nothing to add. An empty/nil/all-blank
// list MUST return the prompt string unchanged (byte-identical) so prompts stay
// characterization-stable when no constraint is active.
func TestWithConstraints(t *testing.T) {
	const base = "SYSTEM PROMPT BODY\n"

	const heading = "## Constraints (user-rejected approaches)"
	const lead = "The user explicitly rejected the following. Do NOT propose them again, in this or any alternative form:"

	t.Run("nil list returns the input unchanged", func(t *testing.T) {
		if got := WithConstraints(base, nil); got != base {
			t.Errorf("nil constraints must return the input unchanged\n got: %q\nwant: %q", got, base)
		}
	})

	t.Run("empty list returns the input unchanged", func(t *testing.T) {
		if got := WithConstraints(base, []string{}); got != base {
			t.Errorf("empty constraints must return the input unchanged\n got: %q\nwant: %q", got, base)
		}
	})

	t.Run("only-blank entries return the input unchanged", func(t *testing.T) {
		if got := WithConstraints(base, []string{"", "   ", "\t\n"}); got != base {
			t.Errorf("all-blank constraints must return the input unchanged\n got: %q\nwant: %q", got, base)
		}
	})

	t.Run("one entry renders the section", func(t *testing.T) {
		got := WithConstraints(base, []string{"no docker"})
		if !strings.HasPrefix(got, base) {
			t.Errorf("output must begin with the original prompt\n got: %q", got)
		}
		for _, w := range []string{heading, lead, "- no docker"} {
			if !strings.Contains(got, w) {
				t.Errorf("output missing %q\n--- output ---\n%s", w, got)
			}
		}
	})

	t.Run("two entries render both bullets", func(t *testing.T) {
		got := WithConstraints(base, []string{"no docker", "no sudo"})
		for _, w := range []string{heading, lead, "- no docker", "- no sudo"} {
			if !strings.Contains(got, w) {
				t.Errorf("output missing %q\n--- output ---\n%s", w, got)
			}
		}
	})

	t.Run("entries are trimmed and blanks skipped", func(t *testing.T) {
		got := WithConstraints(base, []string{"  no docker  ", "", "\tno sudo\n"})
		for _, w := range []string{"- no docker\n", "- no sudo"} {
			if !strings.Contains(got, w) {
				t.Errorf("output missing trimmed bullet %q\n--- output ---\n%s", w, got)
			}
		}
		if strings.Contains(got, "-  no docker") || strings.Contains(got, "no docker  ") {
			t.Errorf("entry was not trimmed:\n%s", got)
		}
	})

	t.Run("section is separated from the prompt by a blank line", func(t *testing.T) {
		got := WithConstraints(base, []string{"no docker"})
		if !strings.Contains(got, "\n\n"+heading) {
			t.Errorf("section must be preceded by a blank line\n--- output ---\n%s", got)
		}
	})
}
