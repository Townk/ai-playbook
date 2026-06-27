package input

import (
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"
)

// ThinkUpdate is one update to the in-box thinking state: Line replaces the
// dark-grey activity line under the box; Done==true ends the animation (the
// inline program quits and RunInline returns).
type ThinkUpdate struct {
	Line string
	Done bool
}

// InlineRequest configures RunInline (the no-mux inline input below the prompt).
type InlineRequest struct {
	Title   string
	Prompt  string
	Value   string
	History string
	Height  int // textarea rows (default 3)
}

// InlineResult is the outcome of RunInline.
type InlineResult struct {
	Value     string
	Submitted bool
}

// ClearInline erases `height` lines of inline (non-alt-screen) output from w,
// returning the cursor to the top of where the region began so the terminal is
// tidy after the inline program exits. height<=0 is a no-op.
func ClearInline(w io.Writer, height int) {
	if height <= 0 {
		return
	}
	fmt.Fprintf(w, "\x1b[%dF\x1b[0J", height)
}

// recvThink blocks for the next ThinkUpdate and maps it to the existing
// doneSignalMsg (reused so input.go's thinking handler is unchanged). A closed
// channel yields done=true so the model quits exactly once.
func recvThink(ch <-chan ThinkUpdate) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return doneSignalMsg{done: true}
		}
		return doneSignalMsg{done: u.Done, thinking: u.Line}
	}
}

// RunInline renders the input UI inline (below the shell prompt, NOT alt-screen)
// to w (typically /dev/tty). On submit it transitions IN-BOX to the sine-wave
// thinking state and calls onSubmit(value); onSubmit returns a channel of
// ThinkUpdate the model drains (each Line updates the activity line; Done — or a
// channel close — ends the animation). On cancel (Esc) Submitted is false and
// onSubmit is never called. The inline region is cleared from w before returning
// in every case so the terminal is left tidy.
func RunInline(w *os.File, req InlineRequest, onSubmit func(value string) <-chan ThinkUpdate) (InlineResult, error) {
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false

	h := req.Height
	if h <= 0 {
		h = 3
	}
	m := newInputModel(defaultTheme(), "default", req.Title, req.Prompt, req.Value, "", h, 1, 1, false, "")
	m.inlineSubmit = onSubmit
	applyHistory(&m, req.History)

	fm, err := tea.NewProgram(
		m,
		tea.WithInput(w),
		tea.WithOutput(w),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		return InlineResult{}, err
	}
	res := fm.(model)
	ClearInline(w, measureHeight(res.render()))
	if res.thinking || res.submitted {
		recordHistory(req.History, res.fld.value())
		return InlineResult{Value: res.fld.value(), Submitted: true}, nil
	}
	return InlineResult{Submitted: false}, nil
}
