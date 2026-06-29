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
	view(innerW int, focused bool) string             // interactive area only (no frame)
	value() string
	filled() bool
	initCmd() tea.Cmd
}
