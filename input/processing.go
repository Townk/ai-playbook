package input

import (
	"bufio"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Messages understood by processingModel.
type statusMsg string
type closeMsg struct{}
type submitMsg string

// processingModel is a Bubble Tea model that shows a spinner + status label
// inside the same renderFrame used by the input widget. It is swapped in
// in-place after the user submits input, so the floating pane never closes.
type processingModel struct {
	theme   Theme
	title   string
	width   int
	height  int // target total render height (= the float pane height) to fill
	label   string
	inFifo  string // path to read status/close records from (empty = no reader)
	recs    chan tea.Msg
	spinner spinner.Model
}

func newProcessingModel(theme Theme, title string, width, height int) processingModel {
	// Pulse (█▓▒░) is the boldest width-safe preset — a prominent, centered
	// "thinking" indicator.
	sp := spinner.New(
		spinner.WithSpinner(spinner.Pulse),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent))),
	)
	return processingModel{
		theme:   theme,
		title:   title,
		width:   width,
		height:  height,
		label:   "Processing…",
		spinner: sp,
	}
}

func newProcessingModelWithFifo(theme Theme, title string, width, height int, inFifo string) processingModel {
	m := newProcessingModel(theme, title, width, height)
	m.inFifo = inFifo
	// Open the IN fifo ONCE and keep a single persistent scanner across records
	// so a batched "close" is never dropped. Init() drains it via nextRecord.
	if inFifo != "" {
		m.recs = startInFifoReader(inFifo)
	}
	return m
}

func (m processingModel) Init() tea.Cmd {
	tick := tea.Tick(m.spinner.Spinner.FPS, func(t time.Time) tea.Msg {
		return spinner.TickMsg{Time: t, ID: m.spinner.ID()}
	})
	if m.recs == nil {
		return tick
	}
	return tea.Batch(tick, nextRecord(m.recs))
}

func (m processingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		m.label = string(msg)
		// Re-issue to receive the next record from the persistent reader.
		var next tea.Cmd
		if m.recs != nil {
			next = nextRecord(m.recs)
		}
		return m, next
	case closeMsg:
		return m, tea.Quit
	case tea.QuitMsg:
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}
	return m, nil
}

// render draws the same-sized, borderless-pane-filling frame as the input it
// replaced: title + rule at the top, then the spinner + label centered in the
// remaining space. It does NOT shrink the frame (which would leave a black gap
// in the float) and shows no hint statusbar.
func (m processingModel) render() string {
	const pad = 1
	innerW := m.width - frameBorder - 2*frameHPad
	if innerW < 1 {
		innerW = 1
	}
	// Total render height must equal the float pane height so the frame fills it.
	h := m.height
	if h < 5 {
		h = 5
	}
	contentH := h - frameBorder - 2*pad // rows inside the border (minus padding)
	if contentH < 1 {
		contentH = 1
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(m.theme.titleColor("default")))
	ruleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Rule))
	title := titleStyle.Render("▓▓▓ " + m.title)
	rule := ruleStyle.Render(strings.Repeat("━", innerW))

	label := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text)).Render(m.label)
	spin := m.spinner.View() + "  " + label
	bodyH := contentH - 2 // minus the title + rule rows
	if bodyH < 1 {
		bodyH = 1
	}
	centered := lipgloss.Place(innerW, bodyH, lipgloss.Center, lipgloss.Center, spin)

	content := strings.Join([]string{title, rule, centered}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.variantColor("default"))).
		Padding(pad, frameHPad, pad, frameHPad).
		Render(content)
}

func (m processingModel) View() tea.View {
	return tea.NewView(m.render())
}

// startInFifoReader opens the IN fifo ONCE and starts a goroutine that scans
// every framed record from it, feeding the returned channel. Opening the fifo
// for reading blocks until a writer appears, so this runs in its own goroutine.
// The channel is buffered and is closed by scanRecords on close/EOF, so no
// record (especially a batched "close") is ever dropped.
func startInFifoReader(path string) chan tea.Msg {
	ch := make(chan tea.Msg, 16)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			// No reader could be established — treat as immediate close so the
			// float quits exactly once.
			ch <- closeMsg{}
			close(ch)
			return
		}
		defer f.Close()
		scanRecords(f, ch)
	}()
	return ch
}

// scanRecords reads framed records from r until "close" or EOF, mapping each:
//   - "status" → statusMsg(label)
//   - "close"  → closeMsg{} (then stop)
//
// On EOF (writer gone) without an explicit close it emits exactly one closeMsg.
// It ALWAYS closes ch on return. A single read carrying several records (e.g.
// "status…␞close␞") yields one msg per record — the trailing close is never
// discarded because a single persistent scanner drains the buffer. It is
// separated from the fifo os.Open so it can be tested against any io.Reader.
func scanRecords(r io.Reader, ch chan<- tea.Msg) {
	defer close(ch)

	sc := bufio.NewScanner(r)
	sc.Split(recordSplitFunc)
	for sc.Scan() {
		cmd, args := decodeRecord(sc.Text())
		switch cmd {
		case "close":
			ch <- closeMsg{}
			return
		case "status":
			label := ""
			if len(args) > 0 {
				label = strings.Join(args, recUS)
			}
			ch <- statusMsg(label)
		}
		// Unknown records are ignored; the scanner keeps draining.
	}
	// EOF / writer gone with no explicit close — quit exactly once.
	ch <- closeMsg{}
}

// nextRecord returns a tea.Cmd that blocks until the next record arrives on ch
// (fed by scanRecords). When ch is closed it yields a single closeMsg so the
// model quits exactly once. Re-issue it after each statusMsg.
func nextRecord(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return closeMsg{}
		}
		return msg
	}
}

// recordSplitFunc is a bufio.SplitFunc that splits on the RS byte (\x1e).
func recordSplitFunc(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == recRS[0] {
			return i + 1, data[:i+1], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// writeOutFifo writes a pre-encoded record to path (best-effort; non-blocking).
func writeOutFifo(path, record string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(record) //nolint:errcheck
}
