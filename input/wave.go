package input

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// brailleBitMap maps a [row][col] dot position (4 rows × 2 cols) to its bit
// within a Braille glyph, exactly as in ~/sine.py.
var brailleBitMap = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// brailleChar OR-combines the 4×2 dot sub-grid into a single Braille glyph
// (base U+2800). dots is indexed [row][col].
func brailleChar(dots [4][2]bool) rune {
	code := rune(0x2800)
	for r := 0; r < 4; r++ {
		for c := 0; c < 2; c++ {
			if dots[r][c] {
				code |= brailleBitMap[r][c]
			}
		}
	}
	return code
}

// WaveFrame is a faithful port of ~/sine.py: it renders two crossing sine
// waves into a Braille dot grid (cols×2 wide, rows×4 tall) and returns the
// rows-line string (lines joined by "\n").
//
// A blue wave follows y = round(midline - amplitude*sin(x*F + phase)); a red
// wave is the same with +π added to the phase. Per CHAR cell the color is
// magenta when both waves place a dot in that cell, blue when only the blue
// wave does, red when only the red wave does, and a blank Braille cell when
// neither does. blue/red/magenta are hex color strings.
//
// Pure and deterministic for a given phase.
func WaveFrame(phase float64, cols, rows int, blue, red, magenta string) string {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	width := cols * 2
	height := rows * 4

	freq := (2 * math.Pi) / (float64(width) * 0.35)
	amplitude := float64(height-1) / 2
	midline := float64(height-1) / 2

	blueGrid := make([][]bool, height)
	redGrid := make([][]bool, height)
	for y := 0; y < height; y++ {
		blueGrid[y] = make([]bool, width)
		redGrid[y] = make([]bool, width)
	}

	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > height-1 {
			return height - 1
		}
		return v
	}

	for x := 0; x < width; x++ {
		yBlue := clamp(int(math.Round(midline - amplitude*math.Sin(float64(x)*freq+phase))))
		blueGrid[yBlue][x] = true

		yRed := clamp(int(math.Round(midline - amplitude*math.Sin(float64(x)*freq+phase+math.Pi))))
		redGrid[yRed][x] = true
	}

	blueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(blue))
	redStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(red))
	magentaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(magenta))

	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		for c := 0; c < cols; c++ {
			var dots [4][2]bool
			hasBlue := false
			hasRed := false
			for sr := 0; sr < 4; sr++ {
				for sc := 0; sc < 2; sc++ {
					b := blueGrid[r*4+sr][c*2+sc]
					rd := redGrid[r*4+sr][c*2+sc]
					if b {
						hasBlue = true
					}
					if rd {
						hasRed = true
					}
					dots[sr][sc] = b || rd
				}
			}

			glyph := brailleChar(dots)
			switch {
			case hasBlue && hasRed:
				sb.WriteString(magentaStyle.Render(string(glyph)))
			case hasBlue:
				sb.WriteString(blueStyle.Render(string(glyph)))
			case hasRed:
				sb.WriteString(redStyle.Render(string(glyph)))
			default:
				// No dots in this cell: a blank Braille glyph, uncolored.
				sb.WriteString(string(glyph))
			}
		}
		lines[r] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// waveTickMsg advances the demo animation one frame.
type waveTickMsg struct{}

// waveDemoModel is the live preview for --wave-demo: a rounded-border box
// showing a crossing-sine-wave Braille spinner under a "Thinking…" caption.
type waveDemoModel struct {
	theme   Theme
	phase   float64
	cols    int
	rows    int
	blue    string
	red     string
	magenta string
}

func newWaveDemoModel(theme Theme) waveDemoModel {
	return waveDemoModel{
		theme:   theme,
		cols:    30,
		rows:    3,
		blue:    theme.Border, // #89b4fa-ish blue
		red:     "#f38ba8",    // catppuccin red/peach (theme has no red)
		magenta: theme.Accent, // #cba6f7 mauve / accent
	}
}

func waveTick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return waveTickMsg{} })
}

func (m waveDemoModel) Init() tea.Cmd { return waveTick() }

func (m waveDemoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case waveTickMsg:
		m.phase += 0.35
		return m, waveTick()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m waveDemoModel) render() string {
	caption := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Text)).Render("Thinking…")
	wave := WaveFrame(m.phase, m.cols, m.rows, m.blue, m.red, m.magenta)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.Border)).
		Render(wave)
	return lipgloss.JoinVertical(lipgloss.Left, caption, box)
}

func (m waveDemoModel) View() tea.View { return tea.NewView(m.render()) }

func runWaveDemo(theme Theme) {
	_, err := tea.NewProgram(
		newWaveDemoModel(theme),
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-assist-input: error: %v\n", err)
		os.Exit(1)
	}
}
