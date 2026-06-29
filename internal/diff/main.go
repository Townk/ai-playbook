package diff

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// renderFile is the headless core: read the patch at path, parse, and render
// at the given terminal width. It is the seam the tests exercise; Main wraps
// it in the scrollable TUI.
func renderFile(path string, width int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("diff: cannot read %s: %v\n", path, err)
	}
	files := Parse(string(data))
	// Identity highlight function — the float's overlay applies chroma when
	// needed; the stand-alone viewer keeps it simple.
	identity := func(code, _ string) string { return code }
	return Render(files, width, identity)
}

// ── TUI model ─────────────────────────────────────────────────────────────

// viewerModel is a minimal scrollable line-window over the rendered diff. It
// stores the pre-split lines and a (offset, height, width) window. On
// WindowSizeMsg the content is re-rendered at the new width so the side-by-
// side / unified threshold (minSideBySide) is honoured live.
type viewerModel struct {
	path   string
	lines  []string
	offset int // index of first visible line
	height int // number of visible rows (terminal height − 1 for hint)
	width  int
}

func newViewerModel(path string) viewerModel {
	return viewerModel{path: path, width: 120, height: 24}
}

func (m viewerModel) rerender() viewerModel {
	text := renderFile(m.path, m.width)
	// Trim trailing blank line that Render appends after each file.
	text = strings.TrimRight(text, "\n")
	m.lines = strings.Split(text, "\n")
	// Clamp offset after potential content shrink.
	m.offset = clamp(m.offset, 0, max(0, len(m.lines)-m.height))
	return m
}

func (m viewerModel) Init() tea.Cmd { return nil }

func (m viewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height - 1 // reserve 1 line for the hint bar
		if m.height < 1 {
			m.height = 1
		}
		return m.rerender(), nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			m.offset = clamp(m.offset-1, 0, max(0, len(m.lines)-m.height))
		case "down", "j":
			m.offset = clamp(m.offset+1, 0, max(0, len(m.lines)-m.height))

		case "pgup", "ctrl+b":
			m.offset = clamp(m.offset-m.height, 0, max(0, len(m.lines)-m.height))
		case "pgdown", "ctrl+f":
			m.offset = clamp(m.offset+m.height, 0, max(0, len(m.lines)-m.height))

		case "g", "home":
			m.offset = 0
		case "G", "end":
			m.offset = max(0, len(m.lines)-m.height)
		}
	}
	return m, nil
}

func (m viewerModel) View() tea.View {
	end := m.offset + m.height
	if end > len(m.lines) {
		end = len(m.lines)
	}
	visible := m.lines[m.offset:end]

	// Pad short content so the hint always appears at the bottom.
	padded := make([]string, m.height)
	copy(padded, visible)

	hint := "  ↑/↓ j/k  PgUp/PgDn  g/G top/bottom  q quit"
	v := tea.NewView(strings.Join(padded, "\n") + "\n" + hint)
	v.AltScreen = true
	return v
}

// ── helpers ────────────────────────────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── subcommand entrypoint ──────────────────────────────────────────────────

// Main is the entrypoint for the `ai-playbook diff <patchfile>` subcommand.
// It parses os.Args[2:] (os.Args[1] is "diff") and returns a process exit
// code; the caller is responsible for os.Exit.
func Main() int {
	args := os.Args[2:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ai-playbook diff <patchfile>")
		return 2
	}
	path := args[0]

	m := newViewerModel(path)
	_, err := tea.NewProgram(
		m,
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook diff: %v\n", err)
		return 1
	}
	return 0
}
