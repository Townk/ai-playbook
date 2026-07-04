package dialog

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// confirmKeyString normalises a KeyPressMsg to a string understood by
// resolveConfirmKey (in confirm_keys.go).
func confirmKeyString(msg tea.KeyPressMsg) string {
	switch msg.Key().Code {
	case tea.KeyEscape:
		return "esc"
	case tea.KeyEnter:
		return "enter"
	case tea.KeyTab:
		return "tab"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	}
	return msg.String()
}

// newConfirmModel builds the generic input model hosting a confirmField. The
// confirm dialog carries no widget state of its own beyond the field — the
// generic model owns the frame (title/prompt/variant/theme/width/padding/inset)
// and its submit/cancel bookkeeping, exactly as ask.go hosts confirm/choose
// fields. width defaults to the confirm dialog's 54-col geometry.
func newConfirmModel(theme Theme, variant, title, prompt, affirmative, negative string, defaultNegative bool, padding, inset int) model {
	m := newInputModel(theme, variant, title, prompt, "", "", 1, padding, inset, false, "")
	m.fld = newConfirmField(theme, variant, affirmative, negative, defaultNegative)
	m.width = 54
	return m
}

func runConfirm(theme Theme, variant, title, prompt, affirmative, negative string, defaultNegative bool, padding, inset int, outFile string) {
	fm, err := tea.NewProgram(
		newConfirmModel(theme, variant, title, prompt, affirmative, negative, defaultNegative, padding, inset),
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook input: error: %v\n", err)
		os.Exit(1)
	}
	res := fm.(model)
	if res.quitting || !res.submitted {
		if outFile != "" {
			writeCancelFile(outFile)
		}
		os.Exit(130)
	}
	result := res.fld.value()
	if outFile != "" {
		writeOutFile(outFile, result)
	}
	fmt.Print(result)
	if result == "yes" {
		os.Exit(0)
	}
	os.Exit(1)
}
