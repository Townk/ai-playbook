package input

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

const promptIcon = "❯"

// historyCap bounds the request-history file (newest historyCap entries kept).
const historyCap = 1000

// applyHistory loads the JSONL history at path into the model's text field for
// UP/DOWN recall. It is a no-op when path is empty, when the field is not a text
// field (only the text widget supports recall), or when the file is missing — so
// non-text floats and the no-history case behave exactly as before. Extracted as
// a seam so the load step is unit-testable without a live TTY.
func applyHistory(m *model, path string) {
	if path == "" {
		return
	}
	tf, ok := m.fld.(*textField)
	if !ok {
		return
	}
	tf.SetHistory(LoadHistory(path))
}

// recordHistory appends a submitted value to the JSONL history at path (de-dup +
// cap honored by AppendHistory). It is a no-op when path is empty. A write error
// is reported to stderr but NON-FATAL — the submit/--out path must never block on
// history. Extracted as a seam so the append step is unit-testable.
func recordHistory(path, value string) {
	if path == "" {
		return
	}
	if err := AppendHistory(path, value, historyCap); err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: --history: %v\n", err)
	}
}

const (
	boxBorder = 2 // inner rounded box, left + right
	boxPadL   = 1 // inner box left padding
	iconCol   = 3 // prompt icon (1) + 2-space gap
	scrollGap = 1 // space between input text and scroll column
	scrollCol = 1 // scroll-indicator column
)

// model is the thin standalone bubbletea model. It wraps a single field and
// owns the frame (title/prompt/variant/theme/width/padding/inset).
type model struct {
	fld       field
	theme     Theme
	variant   string
	title     string
	prompt    string
	width     int
	padding   int
	inset     int
	submitted bool
	quitting  bool
	// singleLine is kept for hint rendering only.
	singleLine bool

	// --- thinking state (triage stage B) -------------------------------------
	// thinkingEnabled is set by --thinking (text type only): on submit the float
	// does NOT quit but transitions IN PLACE to a wave-animated "thinking" state,
	// staying open until the launcher writes <outFile>.done (or the backstop).
	thinkingEnabled bool
	outFile         string  // --out path; written on submit, polled (.done) while thinking
	thinking        bool    // currently animating the in-box wave
	phase           float64 // wave animation phase (advances on each waveTickMsg)
}

// initialModel keeps the original signature the existing tests call (text, 1/1
// padding/inset, default theme).
func initialModel(value, title string, height int) model {
	return newInputModel(defaultTheme(), "default", title, "", value, "", height, 1, 1, false, "")
}

func newInputModel(theme Theme, variant, title, prompt, value, placeholder string, height, padding, inset int, singleLine bool, icon string) model {
	fld := newTextField(theme, value, placeholder, height, singleLine)
	if icon != "" {
		fld.iconGlyph = icon
	}
	return model{
		fld: fld, theme: theme, variant: variant, title: title, prompt: prompt,
		singleLine: singleLine, width: 64,
		padding: padding, inset: inset,
	}
}

func (m model) Init() tea.Cmd { return m.fld.initCmd() }

// innerW computes the width available inside the outer frame for the field.
func (m *model) innerW() int {
	w := m.width - frameBorder - 2*frameHPad
	if w < 1 {
		w = 1
	}
	return w
}

// resize re-sizes the field from the current pane width.
func (m *model) resize() {
	if tf, ok := m.fld.(*textField); ok {
		tf.setWidth(m.innerW())
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.resize()
		return m, nil
	case waveTickMsg:
		// Advance the wave ONLY while thinking; re-tick to keep animating.
		if m.thinking {
			m.phase += 0.35
			return m, waveTick()
		}
		return m, nil
	case doneSignalMsg:
		// The launcher's <outFile>.done close signal (or its absence). Only acted
		// on while thinking; on "not yet" we re-arm the poll.
		if !m.thinking {
			return m, nil
		}
		if msg.done {
			return m, tea.Quit
		}
		return m, pollDoneCmd(m.outFile)
	case thinkingBackstopMsg:
		// Backstop: a dead launcher must not hang the float forever.
		if m.thinking {
			return m, tea.Quit
		}
		return m, nil
	case outWrittenMsg:
		// The submit value was handed to outFile; nothing further to do.
		return m, nil
	}

	// While thinking, the field is frozen (nothing to type/submit); the only key
	// affordance is cancel — Escape / ctrl+c quit (cancel mid-think is allowed).
	if m.thinking {
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "ctrl+c", "esc":
				m.quitting = true
				return m, tea.Quit
			}
		}
		return m, nil
	}

	f, act, cmd := m.fld.handle(msg)
	m.fld = f
	switch act {
	case fieldDone:
		m.submitted = true
		if m.thinkingEnabled {
			// Transition IN PLACE to the thinking state instead of quitting:
			// (a) hand the submitted value to outFile NOW so the launcher can read
			// it while we animate; (b) start the wave tick; (c) poll for the
			// launcher's <outFile>.done close signal; (d) arm the safety backstop.
			m.thinking = true
			return m, tea.Batch(
				writeOutCmd(m.outFile, m.fld.value()),
				waveTick(),
				pollDoneCmd(m.outFile),
				backstopCmd(),
			)
		}
		return m, tea.Quit
	case fieldCancel:
		m.quitting = true
		return m, tea.Quit
	}
	return m, cmd
}

// --- render ------------------------------------------------------------------

func (m model) hint() string {
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Key))
	word := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted))
	seg := func(k, w string) string { return key.Render(k) + word.Render(" "+w) }
	sep := word.Render(" · ")
	if m.singleLine {
		return strings.Join([]string{seg("󰌑", "submit"), seg("󱊷", "cancel")}, sep)
	}
	return strings.Join([]string{seg("󰌑", "submit"), seg("󰘶󰌑", "newline"), seg("󱊷", "cancel")}, sep)
}

func (m model) render() string {
	if m.thinking {
		return m.renderThinking()
	}
	iW := m.innerW()
	sections := []string{}
	if m.prompt != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text)).Render(m.prompt))
	}
	sections = append(sections, m.fld.view(iW, true))
	return renderFrame(m.theme, m.variant, m.title, sections, m.hint(), m.width, m.padding, m.inset)
}

// thinking-state wave palette: the same trio --wave-demo uses — theme Border as
// the blue wave, catppuccin red (#f38ba8) as the red wave, theme Accent (mauve)
// as the magenta overlap.
const thinkingWaveRed = "#f38ba8"

// renderThinking draws the in-place thinking state: the prompt line becomes
// "Thinking…", the input box interior becomes the wave canvas (SAME border + icon
// column as the text box), and the hint line is dropped. Same row count as the
// normal render so the float pane fills without a gap.
func (m model) renderThinking() string {
	iW := m.innerW()
	sections := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text)).Render("Thinking…"),
	}
	if tf, ok := m.fld.(*textField); ok {
		sections = append(sections, tf.thinkingView(iW, m.phase, m.theme.Border, thinkingWaveRed, m.theme.Accent))
	} else {
		sections = append(sections, m.fld.view(iW, true))
	}
	// hint = "" → no submit/newline/cancel line while thinking.
	return renderFrame(m.theme, m.variant, m.title, sections, "", m.width, m.padding, m.inset)
}

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAllKeysAsEscapeCodes: true}
	return v
}

// writeOutFile writes val to path (the --out one-shot file) so a FLOATED input,
// whose stdout is detached by the mux, can hand its answer back to a polling
// launcher. The file is created atomically (write a temp sibling, then rename) so
// the launcher never reads a half-written value. A write failure is reported but
// non-fatal — the value is still printed to stdout for the inline path. Returns
// false on failure.
func writeOutFile(path, val string) bool {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(val), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: --out: %v\n", err)
		return false
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: --out: %v\n", err)
		_ = os.Remove(tmp)
		return false
	}
	return true
}

// CancelSuffix is appended to an --out path to form the cancel-marker file. A
// floated input writes this (empty) file on cancel so a polling launcher learns
// of the cancel at once. Exported so the poller (floatinput) shares the contract.
const CancelSuffix = ".cancel"

// writeCancelFile creates the cancel marker for outFile (best-effort).
func writeCancelFile(outFile string) {
	_ = os.WriteFile(outFile+CancelSuffix, nil, 0o600)
}

// DoneSuffix is appended to an --out path to form the close-signal file the
// launcher writes after routing a thinking submit. The float, while animating,
// polls for it and exits when it appears. Exported so the launcher (stage C)
// shares the contract, mirroring CancelSuffix.
const DoneSuffix = ".done"

func runInput(theme Theme, variant, title, prompt, value, placeholder string, height, padding, inset int, singleLine bool, icon, outFile, historyPath string, thinkingEnabled bool) {
	m := newInputModel(theme, variant, title, prompt, value, placeholder, height, padding, inset, singleLine, icon)
	m.thinkingEnabled = thinkingEnabled
	m.outFile = outFile
	applyHistory(&m, historyPath)
	fm, err := tea.NewProgram(
		m,
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: error: %v\n", err)
		os.Exit(1)
	}
	res := fm.(model)
	// Thinking submit: the value was ALREADY written to outFile on submit (while
	// the wave animated) and the launcher already consumed it — do NOT re-write
	// --out, do NOT print it again, and do NOT write a .cancel marker. Only the
	// (still-unwritten) history append remains.
	if res.thinking {
		recordHistory(historyPath, res.fld.value())
		os.Exit(0)
	}
	if res.submitted {
		if outFile != "" {
			writeOutFile(outFile, res.fld.value())
		}
		recordHistory(historyPath, res.fld.value())
		fmt.Print(res.fld.value())
		os.Exit(0)
	}
	// Cancelled: write the cancel marker (<out>.cancel) so a polling launcher
	// distinguishes cancel from "still deciding" immediately, instead of waiting
	// out the poll timeout. The submit value file is left absent (its existence is
	// the submit signal).
	if outFile != "" {
		writeCancelFile(outFile)
	}
	os.Exit(130)
}

// rootModel is a thin wrapper that owns the input→processing transition when
// --out-fifo/--in-fifo are both set. It holds the currently active tea.Model
// and switches it from inputModel to processingModel on submit, staying inside
// the same tea.Program so the floating pane never closes.
type rootModel struct {
	current     tea.Model
	theme       Theme
	title       string
	width       int
	outFifo     string
	inFifo      string
	historyPath string
}

func newRootModel(inner model, outFifo, inFifo, historyPath string) rootModel {
	return rootModel{
		current:     inner,
		theme:       inner.theme,
		title:       inner.title,
		width:       inner.width,
		outFifo:     outFifo,
		inFifo:      inFifo,
		historyPath: historyPath,
	}
}

func (r rootModel) Init() tea.Cmd { return r.current.Init() }

func (r rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		r.width = wm.Width
		m2, cmd := r.current.Update(msg)
		r.current = m2
		return r, cmd
	}

	// If currently in processing state, delegate all messages to it.
	if pm, ok := r.current.(processingModel); ok {
		m2, cmd := pm.Update(msg)
		r.current = m2
		return r, cmd
	}

	// Currently in input state — delegate and check for submit/cancel.
	m2, cmd := r.current.Update(msg)
	inputM, isInput := m2.(model)
	if !isInput {
		r.current = m2
		return r, cmd
	}

	if inputM.submitted {
		// Write submit record to out-fifo and transition to processing.
		writeOutFifo(r.outFifo, encodeRecord("submit", inputM.fld.value()))
		recordHistory(r.historyPath, inputM.fld.value())
		// Match the spinner frame to the input frame's exact rendered height (=
		// the float pane height), so the processing state fills the SAME space —
		// no shrink, no black gap below where the input box used to be.
		paneH := len(strings.Split(inputM.render(), "\n"))
		pm := newProcessingModelWithFifo(r.theme, r.title, r.width, paneH, r.inFifo)
		r.current = pm
		// pm.Init() starts the spinner tick AND the first nextRecord receive;
		// the persistent reader (opened in the constructor) feeds the channel.
		return r, pm.Init()
	}

	if inputM.quitting {
		// Write cancel record to out-fifo and quit.
		writeOutFifo(r.outFifo, encodeRecord("cancel"))
		return r, tea.Quit
	}

	r.current = inputM
	return r, cmd
}

func (r rootModel) View() tea.View {
	return r.current.View()
}

func runInputWithFifo(theme Theme, variant, title, prompt, value, placeholder string, height, padding, inset int, singleLine bool, icon, outFifo, inFifo, historyPath string) {
	inner := newInputModel(theme, variant, title, prompt, value, placeholder, height, padding, inset, singleLine, icon)
	applyHistory(&inner, historyPath)
	root := newRootModel(inner, outFifo, inFifo, historyPath)
	_, err := tea.NewProgram(
		root,
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: error: %v\n", err)
		os.Exit(1)
	}
	// In fifo mode the outcome is communicated via the fifo protocol, not stdout.
	os.Exit(0)
}
