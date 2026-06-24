package input

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// formField is a parsed entry from the US/RS form spec.
type formField struct {
	name  string
	ftype string // "line" | "text" | "choose"
	label string
	param string
}

// parseFormSpec parses the US/RS form specification. Records are separated by
// RS (\x1e); each record is US (\x1f)-separated into: name, type (default
// "line"), label, param. Returns an error if the field count is not in [2, 5].
func parseFormSpec(raw string) ([]formField, error) {
	const (
		usChar = "\x1f"
		rsChar = "\x1e"
	)
	records := strings.Split(raw, rsChar)
	// Trim trailing empty record from trailing RS.
	if len(records) > 0 && records[len(records)-1] == "" {
		records = records[:len(records)-1]
	}
	if len(records) < 2 || len(records) > 5 {
		return nil, fmt.Errorf("form: expected 2–5 fields, got %d", len(records))
	}
	fields := make([]formField, len(records))
	for i, rec := range records {
		parts := strings.SplitN(rec, usChar, 4)
		ff := formField{}
		if len(parts) > 0 {
			ff.name = parts[0]
		}
		if len(parts) > 1 && parts[1] != "" {
			ff.ftype = parts[1]
		} else {
			ff.ftype = "line"
		}
		if len(parts) > 2 {
			ff.label = parts[2]
		}
		if len(parts) > 3 {
			ff.param = parts[3]
		}
		// Default label to name if empty.
		if ff.label == "" {
			ff.label = ff.name
		}
		// Validate field type: only line/text/choose are supported.
		switch ff.ftype {
		case "line", "text", "choose":
			// valid
		default:
			return nil, fmt.Errorf("form: field %q has unsupported type %q (use line, text, or choose)", ff.name, ff.ftype)
		}
		fields[i] = ff
	}
	return fields, nil
}

// formModel is the bubbletea model for the native single-frame tabbed form.
type formModel struct {
	theme     Theme
	title     string
	specs     []formField // original spec (for names/labels/types)
	fields    []field     // constructed field implementations
	focus     int
	width     int
	padding   int
	inset     int
	submitted bool
	cancelled bool
}

// buildField constructs a field implementation from a formField spec.
// Supported types: "line", "text", "choose". Callers must validate the type
// before calling (parseFormSpec does this).
func buildField(theme Theme, ff formField) field {
	switch ff.ftype {
	case "text":
		return newTextField(theme, "", "", 4, false)
	case "choose":
		param := ff.param
		multi := false
		other := ""
		// Strip multi: prefix.
		if strings.HasPrefix(param, "multi:") {
			multi = true
			param = strings.TrimPrefix(param, "multi:")
		}
		// Strip other: prefix.
		if strings.HasPrefix(param, "other:") {
			other = strings.TrimPrefix(param, "other:")
			// other label may be before GS-separated options.
			if idx := strings.Index(other, "\x1d"); idx >= 0 {
				other = other[:idx]
				param = param[len("other:"+other)+1:]
			} else {
				param = ""
			}
		}
		// Options are GS-separated.
		var options []string
		if param != "" {
			options = strings.Split(param, "\x1d")
		}
		return newChooseField(theme, "default", options, multi, other)
	default: // "line" and any unknown fallback
		return newTextField(theme, "", "", 1, true)
	}
}

// newFormModel constructs a formModel from parsed field specs.
func newFormModel(theme Theme, title string, specs []formField, padding, inset int) formModel {
	fields := make([]field, len(specs))
	for i, ff := range specs {
		fields[i] = buildField(theme, ff)
	}
	return formModel{
		theme:   theme,
		title:   title,
		specs:   specs,
		fields:  fields,
		focus:   0,
		width:   64,
		padding: padding,
		inset:   inset,
	}
}

func (m formModel) Init() tea.Cmd {
	if len(m.fields) == 0 {
		return nil
	}
	return m.fields[m.focus].initCmd()
}

func (m formModel) innerW() int {
	w := m.width - frameBorder - 2*frameHPad
	if w < 1 {
		w = 1
	}
	return w
}

// allFilled returns true when every field reports filled().
func (m *formModel) allFilled() bool {
	for _, f := range m.fields {
		if !f.filled() {
			return false
		}
	}
	return true
}

// nextUnfilled returns the index of the next unfilled field starting after
// current focus (wrapping). Returns -1 if all are filled.
func (m *formModel) nextUnfilled() int {
	n := len(m.fields)
	for i := 1; i <= n; i++ {
		idx := (m.focus + i) % n
		if !m.fields[idx].filled() {
			return idx
		}
	}
	return -1
}

func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyPressMsg:
		// Tab / Shift+Tab: free-tab navigation (all field types).
		if msg.Code == tea.KeyTab && !msg.Mod.Contains(tea.ModShift) {
			n := len(m.fields)
			m.focus = (m.focus + 1) % n
			return m, m.fields[m.focus].initCmd()
		}
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			n := len(m.fields)
			m.focus = (m.focus - 1 + n) % n
			return m, m.fields[m.focus].initCmd()
		}

		// Right / Left arrow: field navigation only for choose fields.
		// For line/text fields, ←/→ move the text cursor (pass through below).
		if m.specs[m.focus].ftype == "choose" {
			if msg.Code == tea.KeyRight {
				n := len(m.fields)
				m.focus = (m.focus + 1) % n
				return m, m.fields[m.focus].initCmd()
			}
			if msg.Code == tea.KeyLeft {
				n := len(m.fields)
				m.focus = (m.focus - 1 + n) % n
				return m, m.fields[m.focus].initCmd()
			}
		}

		// Delegate to focused field.
		f, act, cmd := m.fields[m.focus].handle(msg)
		m.fields[m.focus] = f

		switch act {
		case fieldCancel:
			m.cancelled = true
			return m, tea.Quit
		case fieldDone:
			if m.allFilled() {
				m.submitted = true
				return m, tea.Quit
			}
			// Move to next unfilled field.
			next := m.nextUnfilled()
			if next >= 0 {
				m.focus = next
				return m, m.fields[m.focus].initCmd()
			}
			// Shouldn't happen (allFilled would be true), but guard.
			m.submitted = true
			return m, tea.Quit
		}
		return m, cmd
	}
	return m, nil
}

// tabRow renders the tab labels: done=✓ (muted), active=◆ (accent), pending=(muted).
func (m formModel) tabRow() string {
	accentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted))

	parts := make([]string, len(m.specs))
	for i, ff := range m.specs {
		label := ff.label
		switch {
		case i == m.focus:
			parts[i] = accentStyle.Render("◆ " + label)
		case m.fields[i].filled():
			parts[i] = mutedStyle.Render("✓ " + label)
		default:
			parts[i] = mutedStyle.Render(label)
		}
	}
	sep := mutedStyle.Render(" · ")
	return strings.Join(parts, sep)
}

// hint returns the keyboard hint line for the form.
func (m formModel) hint() string {
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Key))
	word := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted))
	seg := func(k, w string) string { return key.Render(k) + word.Render(" "+w) }
	sep := word.Render(" · ")
	return strings.Join([]string{
		seg("⇄", "field"),
		seg("↵", "next"),
		seg("󱊷", "dismiss"),
	}, sep)
}

// maxHeight returns the tallest rendered height (in lines) across all field
// focus states at the given width. The measure branch calls this instead of
// measuring a single render() (which only reflects focus=0) so that the pane
// is sized for the worst-case navigable height.
func (m formModel) maxHeight(width int) int {
	m.width = width
	saved := m.focus
	max := 0
	for i := range m.fields {
		m.focus = i
		h := measureHeight(m.render())
		if h > max {
			max = h
		}
	}
	m.focus = saved
	return max
}

func (m formModel) render() string {
	iW := m.innerW()
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text))
	desc := descStyle.Render(m.specs[m.focus].label)
	body := []string{
		m.tabRow(),
		desc,
		m.fields[m.focus].view(iW, true),
	}
	return renderFrame(m.theme, "default", m.title, body, m.hint(), m.width, m.padding, m.inset)
}

func (m formModel) View() tea.View {
	v := tea.NewView(m.render())
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAllKeysAsEscapeCodes: true}
	return v
}

// runForm reads the spec (from a file path or stdin), parses it, and runs the
// interactive form TUI. On submit it prints the US/RS answer protocol to stdout.
func runForm(theme Theme, title, spec string, padding, inset int) {
	var raw string
	if spec != "" {
		data, err := os.ReadFile(spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-assist-input: --spec: %v\n", err)
			os.Exit(1)
		}
		raw = string(data)
	} else {
		data, err := os.ReadFile("/dev/stdin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-assist-input: reading stdin: %v\n", err)
			os.Exit(1)
		}
		raw = string(data)
	}

	parsed, err := parseFormSpec(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: %v\n", err)
		os.Exit(1)
	}

	fm, err := tea.NewProgram(
		newFormModel(theme, title, parsed, padding, inset),
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: error: %v\n", err)
		os.Exit(1)
	}

	res := fm.(formModel)
	if res.cancelled || !res.submitted {
		os.Exit(130)
	}

	// Emit US/RS answer protocol.
	// Each record: name US value; records joined by RS.
	// Multi-choose values are \n-joined by the field; convert to GS.
	records := make([]string, len(res.specs))
	for i, ff := range res.specs {
		val := res.fields[i].value()
		val = strings.ReplaceAll(val, "\n", "\x1d")
		records[i] = ff.name + "\x1f" + val
	}
	fmt.Print(strings.Join(records, "\x1e"))
	os.Exit(0)
}
