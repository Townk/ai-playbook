package dialog

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// newChooseModel builds the generic input model hosting a chooseField. Like
// confirm, the choose dialog carries no state beyond the field: the generic
// model owns the frame and submit/cancel bookkeeping (mirroring how ask.go
// hosts confirm/choose fields), and the choose-specific hint is served by the
// field itself. width defaults to the choose dialog's 54-col geometry.
func newChooseModel(theme Theme, variant, title, prompt string, options []string, multi bool, other string, padding, inset int) model {
	m := newInputModel(theme, variant, title, prompt, "", "", 1, padding, inset, false, "")
	m.fld = newChooseField(theme, variant, options, multi, other)
	m.width = 54
	return m
}

// chooseHint builds the keyboard-hint line for a choose dialog.
// Only key glyphs are rendered in theme.Key (bright white); separators,
// dashes, and descriptive words are in theme.Muted (dark grey).
// rows is kept for API compatibility but is no longer used.
func chooseHint(t Theme, rows int, multi bool, bg string) string {
	keyStyle, mutedStyle := hintKW(t, bg)
	seg := func(k, w string) string { return keyStyle.Render(k) + mutedStyle.Render(w) }
	sep := mutedStyle.Render(" · ")

	// Move segment: ↑↓ / jk move
	move := seg("↑↓", "") + mutedStyle.Render("/") + seg("jk", " move")

	segs := []string{move}

	if multi {
		segs = append(segs, seg("space", " toggle"))
	}

	segs = append(segs, seg("↵", " ok"), seg("󱊷", " dismiss"))
	return strings.Join(segs, sep)
}

func runChoose(theme Theme, variant, title, prompt string, options []string, multi bool, other string, padding, inset int, outFile string) {
	fm, err := tea.NewProgram(
		newChooseModel(theme, variant, title, prompt, options, multi, other, padding, inset),
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
	val := res.fld.value()
	if val == "" {
		if outFile != "" {
			writeCancelFile(outFile)
		}
		os.Exit(130)
	}
	if outFile != "" {
		writeOutFile(outFile, val)
	}
	fmt.Print(val)
	os.Exit(0)
}
