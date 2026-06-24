package input

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// confirmModel is the thin bubbletea wrapper over a confirmField. It owns the
// frame (title/prompt/variant/theme/width/padding/inset) and delegates all
// key handling to the field.
type confirmModel struct {
	fld       *confirmField
	theme     Theme
	variant   string
	title     string
	prompt    string
	width     int
	padding   int
	inset     int
	cancelled bool
	// focus mirrors fld.focus so existing tests that inspect m.focus still pass.
	focus int
}

func newConfirmModel(theme Theme, variant, title, prompt, affirmative, negative string, defaultNegative bool, padding, inset int) confirmModel {
	fld := newConfirmField(theme, variant, affirmative, negative, defaultNegative)
	return confirmModel{
		fld:     fld,
		theme:   theme,
		variant: variant,
		title:   title,
		prompt:  prompt,
		focus:   fld.focus,
		width:   54,
		padding: padding,
		inset:   inset,
	}
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		f2, act, cmd := m.fld.handle(msg)
		m.fld = f2.(*confirmField)
		m.focus = m.fld.focus
		switch act {
		case fieldDone:
			return m, tea.Quit
		case fieldCancel:
			m.cancelled = true
			return m, tea.Quit
		}
		return m, cmd
	}
	return m, nil
}

func (m confirmModel) innerW() int {
	w := m.width - frameBorder - 2*frameHPad
	if w < 1 {
		w = 1
	}
	return w
}

func (m confirmModel) render() string {
	iW := m.innerW()
	sections := []string{}
	if m.prompt != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text)).Render(m.prompt))
	}
	sections = append(sections, m.fld.view(iW, true))
	return renderFrame(m.theme, m.variant, m.title, sections, m.fld.hint(), m.width, m.padding, m.inset)
}

func (m confirmModel) View() tea.View { return tea.NewView(m.render()) }

func runConfirm(theme Theme, variant, title, prompt, affirmative, negative string, defaultNegative bool, padding, inset int) {
	fm, err := tea.NewProgram(
		newConfirmModel(theme, variant, title, prompt, affirmative, negative, defaultNegative, padding, inset),
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: error: %v\n", err)
		os.Exit(1)
	}
	res := fm.(confirmModel)
	if res.cancelled || !res.fld.accepted {
		os.Exit(130)
	}
	result := res.fld.accepted_v
	fmt.Print(result)
	if result == "yes" {
		os.Exit(0)
	}
	os.Exit(1)
}
