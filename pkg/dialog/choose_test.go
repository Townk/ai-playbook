package dialog

import (
	"strings"
	"testing"
)

// TestChooseDialog_PromptHasDialogBackground mirrors
// TestConfirmDialog_PromptHasDialogBackground (confirm_test.go): the choose
// dialog's prompt line must carry the Mantle background SGR, not bleed to the
// terminal default. Before the fix, chooseModel.render() built the prompt with
// a foreground-only style instead of the shared promptStyle helper.
func TestChooseDialog_PromptHasDialogBackground(t *testing.T) {
	m := newChooseModel(defaultTheme(), "default", "T", "Pick one:", []string{"a", "b", "c"}, false, "", 1, 1)
	m.width = 50
	out := m.render()
	promptLine := findLine(t, out, "Pick one:")
	if !strings.Contains(promptLine, mantleBgSGR) {
		t.Fatalf("choose prompt line missing Mantle background SGR:\n%q", promptLine)
	}
}
