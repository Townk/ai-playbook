package input

import (
	"fmt"
	"math"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"

	"github.com/Townk/ai-playbook/internal/theme"
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
		fmt.Fprintf(os.Stderr, "ai-playbook input: --history: %v\n", err)
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
	// thinkingLine, when set, is ONE line of dark-grey text rendered BELOW the input
	// box (with a blank-line gap) while thinking. A LOOK preview for now — the real
	// agent reasoning is redacted by `claude --print`, so this is a placeholder until
	// real/streamed content (or a status) is wired. Empty → nothing extra is drawn.
	thinkingLine string

	// in-process inline thinking (no-mux RunInline): when inlineSubmit is set, a
	// submit transitions IN-BOX to the wave state and drives the animation from
	// the channel inlineSubmit returns (instead of the float's <out>.done file).
	inlineSubmit func(value string) <-chan ThinkUpdate
	inlineThink  <-chan ThinkUpdate

	// inline renders the reduced no-mux layout: ONLY the description line, the
	// (self-bordered) input box, and the hint — no title bar and no outer frame
	// (those belong to the mux float). Set by RunInline. See ADR-0006 / the
	// inline-ask spec.
	inline bool
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
			m.phase += waveStep
			return m, waveTick()
		}
		return m, nil
	case doneSignalMsg:
		// The launcher's <outFile>.done close signal (or its absence). Only acted
		// on while thinking; on "not yet" we re-arm the poll.
		if !m.thinking {
			return m, nil
		}
		// Refresh the dark-grey thinking line from the launcher's live model-output
		// tail (<out>.thinking) WHEN present; keep the current line (the generic
		// "preparing" text, or the last tail) while the tail is still empty.
		if msg.thinking != "" {
			m.thinkingLine = msg.thinking
		}
		if msg.done {
			return m, tea.Quit
		}
		if m.inlineThink != nil {
			return m, recvThink(m.inlineThink)
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
		if m.inlineSubmit != nil {
			// In-process inline: enter the wave state and drive it from the channel.
			m.thinking = true
			m.thinkingLine = thinkingPrepLine
			m.inlineThink = m.inlineSubmit(m.fld.value())
			return m, tea.Batch(waveTick(), recvThink(m.inlineThink))
		}
		if m.thinkingEnabled {
			// Transition IN PLACE to the thinking state instead of quitting:
			// (a) hand the submitted value to outFile NOW so the launcher can read
			// it while we animate; (b) start the wave tick; (c) poll for the
			// launcher's <outFile>.done close signal; (d) arm the safety backstop.
			m.thinking = true
			// Seed the activity line with a generic "preparing" message until the
			// launcher's live model-output tail starts arriving.
			m.thinkingLine = thinkingPrepLine
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

func (m model) hint(bg string) string {
	key, word := hintKW(m.theme, bg)
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
	prompt := ""
	if m.prompt != "" {
		prompt = promptStyle(m.theme).Render(m.prompt)
	}
	// Prefer the field's own hint when it provides one (confirm/choose carry
	// type-specific accelerators); text/line fields have none, so the generic
	// submit/newline/cancel hint applies. This makes the embeddable Ask render the
	// correct hint per type while the standalone text/line path is unchanged.
	hintBG := hintFrameBG
	if m.inline {
		hintBG = ""
	}
	hint := m.hint(hintBG)
	if h, ok := m.fld.(interface{ hint(string) string }); ok {
		hint = h.hint(hintBG)
	}
	if m.inline {
		// Inline composites on the pane/terminal background, not Mantle — the box
		// interior must stay bg-less (mirrors why hintFrameBG passes "" here too).
		if tf, ok := m.fld.(*textField); ok {
			tf.boxBG = ""
		}
		inlinePrompt := prompt
		if m.prompt != "" {
			inlinePrompt = lipgloss.NewStyle().Foreground(lipgloss.Color(inlineDescColor)).Render(m.prompt)
		}
		return m.inlineLayout(inlinePrompt, m.fld.view(inlineBoxWidth, true), hint)
	}
	// Framed: the dialog frame fills Mantle (frame.go), so the box interior must
	// carry it too, or its interior (and border background) bleeds to the
	// terminal default — the same bleed promptStyle/hintFrameBG fix elsewhere.
	if tf, ok := m.fld.(*textField); ok {
		tf.boxBG = theme.Mantle
	}
	iW := m.innerW()
	sections := []string{}
	if prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections, m.fld.view(iW, true))
	return renderFrame(m.theme, m.variant, m.title, sections, hint, m.width, m.padding, m.inset)
}

const (
	// inlineBoxWidth is the fixed column width of the no-mux input box + separator.
	inlineBoxWidth = 78
	// inlineBoxIndent is the leading indent on the separator and the box.
	inlineBoxIndent = " "
	// inlineTextIndent is the leading indent on the description and hint lines.
	inlineTextIndent = "   "
	// inlineDescColor renders the no-mux description in the viewer's H2 color
	// (colPeach #fab387) rather than body Text. Exploratory — under evaluation.
	inlineDescColor = "#fab387"
)

// indentLines prepends pad to every line of s (so a multi-line box indents whole).
func indentLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// inlineLayout stacks the no-mux UI top→bottom: a dark-grey separator rule, the
// description line, the (self-bordered) box, and the hint/activity line. The
// separator and box carry inlineBoxIndent (1 space); the description and hint
// carry inlineTextIndent. No blank lines, no title bar / outer frame (the mux
// float's chrome).
func (m model) inlineLayout(top, box, bottom string) string {
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Rule)).Render(strings.Repeat("─", inlineBoxWidth))
	var rows []string
	rows = append(rows, indentLines(sep, inlineBoxIndent))
	if top != "" {
		rows = append(rows, indentLines(top, inlineTextIndent))
	}
	rows = append(rows, indentLines(strings.TrimRight(box, "\n"), inlineBoxIndent))
	rows = append(rows, indentLines(bottom, inlineTextIndent))
	return strings.Join(rows, "\n")
}

// thinking-state wave palette: the same trio --wave-demo uses — theme Border as
// the blue wave, catppuccin red (#f38ba8) as the red wave, theme Accent (mauve)
// as the magenta overlap.
const thinkingWaveRed = "#f38ba8"

// waveStep is the per-frame phase advance for the sine animation. waveLoopFrames is
// the number of DISTINCT frames in one seamless cycle: the phase advances exactly 2π
// over that many frames, so frame N is identical to frame 0 (the next cycle's start —
// a clean loop, NOT a duplicate; the N→0 transition is a normal single step). It MUST
// be an exact 2π/integer or the cycle overshoots 2π each loop and hitches.
//
// N trades off two ways and the sweet spot is at the FAST end: slower (large N) gives
// the eye time to recognize the recurring image, so the loop reads as a "beat" (N=18 ≈
// 0.6s and N=36 ≈ 1.2s both did). Faster (small N) flies the recurrence past before the
// eye can lock onto it. At ~30fps (tea.Every 33ms): N=12 ≈ 0.4s/cycle, ~1.5× the
// original 2π/17.95 step.
const (
	waveLoopFrames = 12
	waveStep       = 2 * math.Pi / waveLoopFrames
)

// thinkingPrepLine is the generic activity line shown the moment thinking starts,
// before the launcher's live model-output tail begins arriving.
const thinkingPrepLine = "Deciding how to handle this…"

// The "Thinking…" prompt "breathes" its foreground between bright white and
// catppuccin peach, synced to the wave phase (no extra tick).
//   - thinkingBreatheBright / thinkingBreathePeach: the two LERP endpoints.
//   - thinkingBreatheK: the pulse rate applied to phase. With phase += waveStep (≈0.349) per
//     ~33ms tick (~10.5 phase/sec), the sine period 2π/k ≈ 20.9 phase-units ≈
//     ~2.0s per full breath (in the spec's 1.5-2.5s band).
const (
	thinkingBreatheBright = "#ffffff"
	thinkingBreathePeach  = "#fab387"
	thinkingBreatheK      = 0.3
)

// thinkingPromptColor returns the breathing foreground for "Thinking…" at the
// given phase: t = (sin(phase·k)+1)/2 LERPed bright-white ↔ peach.
func thinkingPromptColor(phase float64) string {
	t := (math.Sin(phase*thinkingBreatheK) + 1) / 2
	return lerpHexColor(thinkingBreatheBright, thinkingBreathePeach, t)
}

// renderThinking draws the in-place thinking state: the prompt line becomes
// "Thinking…", the input box interior becomes the wave canvas (SAME border + icon
// column as the text box), and the hint line is dropped. Same row count as the
// normal render so the float pane fills without a gap.
func (m model) renderThinking() string {
	// Inline keeps the box at the fixed inlineBoxWidth so it doesn't jump width
	// when the box transitions from input to the wave on submit.
	iW := m.innerW()
	if m.inline {
		iW = inlineBoxWidth
	}
	top := lipgloss.NewStyle().Foreground(lipgloss.Color(thinkingPromptColor(m.phase))).Render("Thinking…")
	var box string
	if tf, ok := m.fld.(*textField); ok {
		box = tf.thinkingView(iW, m.phase, m.theme.Border, thinkingWaveRed, m.theme.Accent)
	} else {
		box = m.fld.view(iW, true)
	}
	// The model-activity line goes in the bottom slot, dark grey, truncated to the
	// box width. (The waves stay full inside the input box.)
	activity := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Muted)).
		Render(truncateToWidth(m.thinkingLine, iW))
	if m.inline {
		return m.inlineLayout(top, box, activity)
	}
	return renderFrame(m.theme, m.variant, m.title, []string{top, box}, activity, m.width, m.padding, m.inset)
}

// truncateToWidth shortens s to at most w display columns (rune/width-safe, no
// wrap), appending an ellipsis when it must cut. w<=0 yields "".
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
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
		fmt.Fprintf(os.Stderr, "ai-playbook input: --out: %v\n", err)
		return false
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook input: --out: %v\n", err)
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

// ThinkingSuffix is appended to an --out path to form the live-output file the
// launcher rewrites (a single-line, whitespace-collapsed sliding tail of the
// classify model's streamed text) WHILE the thinking float animates. The float,
// while polling for <out>.done, also reads this file each tick into its dark-grey
// thinking line. Exported so the launcher shares the contract, mirroring
// DoneSuffix / ClosedSuffix. Absent file → the thinking line is left empty.
const ThinkingSuffix = ".thinking"

// ClosedSuffix is appended to an --out path to form the torn-down marker the
// THINKING float writes just before it exits (after it has observed <out>.done
// and the tea program has returned). The launcher polls for it so it can wait
// until the float process is actually gone — and zellij has returned focus to
// the origin tiled pane — BEFORE spawning the result pane. Without this wait the
// "docked" result pane inherits the still-focused float's FLOATING context and
// opens floating behind the float. Exported so the launcher shares the contract,
// mirroring DoneSuffix / CancelSuffix.
const ClosedSuffix = ".closed"

// writeClosedFile creates the torn-down marker for outFile (best-effort). Called
// from the THINKING-mode exit path just before the process exits, so the launcher
// learns the float has fully torn down. A no-op on an empty path.
func writeClosedFile(outFile string) {
	if outFile == "" {
		return
	}
	_ = os.WriteFile(outFile+ClosedSuffix, nil, 0o600)
}

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
		fmt.Fprintf(os.Stderr, "ai-playbook input: error: %v\n", err)
		os.Exit(1)
	}
	res := fm.(model)
	// Thinking submit: the value was ALREADY written to outFile on submit (while
	// the wave animated) and the launcher already consumed it — do NOT re-write
	// --out, do NOT print it again, and do NOT write a .cancel marker. Only the
	// (still-unwritten) history append remains.
	if res.thinking {
		recordHistory(historyPath, res.fld.value())
		// Signal the launcher the float has fully torn down (the tea program has
		// returned). The launcher waits for this marker before spawning the result
		// pane so the docked pane isn't created in the float's floating context.
		writeClosedFile(outFile)
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
		fmt.Fprintf(os.Stderr, "ai-playbook input: error: %v\n", err)
		os.Exit(1)
	}
	// In fifo mode the outcome is communicated via the fifo protocol, not stdout.
	os.Exit(0)
}
