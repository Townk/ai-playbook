package ui

import (
	"strings"
	"testing"
)

func TestBuildHelpLines(t *testing.T) {
	lines := buildHelpLines()
	var b strings.Builder
	allWide := true
	for _, l := range lines {
		if !l.Wide {
			allWide = false
		}
		b.WriteString(strip(l.Text))
		b.WriteString("\n")
	}
	got := b.String()
	if !allWide {
		t.Fatal("help lines must be Wide (horizontally scrollable)")
	}
	for _, want := range []string{
		"Key Bindings", "Other Interactions", // top-level (Mauve) section headers
		"Actions", "Movement", "Horizontal", "Buttons", // sub-group headers
		"down one line", "half page down / up", "left / right half-width",
		"hint mode for keyboard-only click", "mouse clicks activate buttons",
		"refine (note persists as a session constraint)", "wrap-up work in the playbook",
		"generate a playbook for the solution",
		"toggle this help", "quit/dismiss",
		"copy block to clipboard", "run entire block in origin shell",
		"invalidate cache re-run prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("help content missing %q\n---\n%s", want, got)
		}
	}
	// Descriptions align: the column where "down one line" and
	// "half page down / up" start must match.
	col := func(needle string) int {
		for _, line := range strings.Split(got, "\n") {
			if i := strings.Index(line, needle); i >= 0 {
				return len([]rune(line[:i]))
			}
		}
		return -1
	}
	if c1, c2 := col("down one line"), col("half page down / up"); c1 != c2 || c1 < 0 {
		t.Fatalf("descriptions not column-aligned: %d vs %d", c1, c2)
	}
}
