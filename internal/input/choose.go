package input

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// chooseModel is the thin bubbletea wrapper over a chooseField. It owns the
// frame (title/prompt/variant/theme/width/padding/inset) and delegates all key
// handling to the field.
type chooseModel struct {
	fld       *chooseField
	theme     Theme
	variant   string
	title     string
	prompt    string
	multi     bool
	width     int
	padding   int
	inset     int
	cancelled bool
	done      bool
}

func newChooseModel(theme Theme, variant, title, prompt string, options []string, multi bool, other string, padding, inset int) chooseModel {
	fld := newChooseField(theme, variant, options, multi, other)
	return chooseModel{
		fld:     fld,
		theme:   theme,
		variant: variant,
		title:   title,
		prompt:  prompt,
		multi:   multi,
		width:   54,
		padding: padding,
		inset:   inset,
	}
}

func (m chooseModel) Init() tea.Cmd { return nil }

func (m chooseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		f2, act, cmd := m.fld.handle(msg)
		m.fld = f2.(*chooseField)
		switch act {
		case fieldDone:
			m.done = true
			return m, tea.Quit
		case fieldCancel:
			m.cancelled = true
			return m, tea.Quit
		}
		return m, cmd
	}
	return m, nil
}

func (m chooseModel) innerW() int {
	w := m.width - frameBorder - 2*frameHPad
	if w < 1 {
		w = 1
	}
	return w
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

func (m chooseModel) hint() string {
	return chooseHint(m.theme, m.fld.totalRows(), m.multi, hintFrameBG)
}

func (m chooseModel) render() string {
	iW := m.innerW()
	sections := []string{}
	if m.prompt != "" {
		sections = append(sections, promptStyle(m.theme).Render(m.prompt))
	}
	sections = append(sections, m.fld.view(iW, true))
	return renderFrame(m.theme, m.variant, m.title, sections, m.hint(), m.width, m.padding, m.inset)
}

func (m chooseModel) View() tea.View { return tea.NewView(m.render()) }

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
	res := fm.(chooseModel)
	if res.cancelled || !res.done {
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
