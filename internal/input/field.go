package input

import tea "charm.land/bubbletea/v2"

// fieldAction is the outcome of a field handling a message.
type fieldAction int

const (
	fieldNone   fieldAction = iota // no special action; keep running
	fieldDone                      // user submitted; field is complete
	fieldCancel                    // user cancelled; discard value
)

// field is the interface every interactive control must satisfy. A field owns
// its own textarea (or buttons, etc.); the standalone input wrapper and future
// form composer interact with it only through this interface.
type field interface {
	handle(msg tea.Msg) (field, fieldAction, tea.Cmd) // process a msg while focused
	// view renders the interactive area only (no outer frame). frameBG is the
	// background the hosting frame fills with (theme.Mantle for a framed dialog,
	// "" for the inline/unframed layout that composites on the terminal). Every
	// span a field paints — including muted rows, section labels, gaps, and
	// unfocused widgets — must carry frameBG so a foreground-only SGR reset never
	// drops those cells to the terminal default (the lipgloss v2 inner-reset bleed
	// renderFrame's own Background wrapper does not protect against). A focused
	// widget's own selected background overrides frameBG on its cells.
	view(innerW int, focused bool, frameBG string) string
	value() string
	filled() bool
	initCmd() tea.Cmd
}
