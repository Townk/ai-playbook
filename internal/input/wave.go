package input

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Townk/ai-playbook/internal/theme"
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

// lerpHexColor linearly interpolates between two "#rrggbb" hex colors and returns
// the result as "#rrggbb". t is clamped to [0,1]; t=0 → a, t=1 → b, t=0.5 → the
// per-channel midpoint. A malformed input falls back to the other color (or "#000000"
// if both are malformed) so a bad theme value never panics the render.
func lerpHexColor(a, b string, t float64) string {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	ar, ag, ab, aok := parseHexRGB(a)
	br, bg, bb, bok := parseHexRGB(b)
	switch {
	case !aok && !bok:
		return "#000000"
	case !aok:
		ar, ag, ab = br, bg, bb
	case !bok:
		br, bg, bb = ar, ag, ab
	}
	lerp := func(x, y int) int {
		return int(math.Round(float64(x) + (float64(y)-float64(x))*t))
	}
	return fmt.Sprintf("#%02x%02x%02x", lerp(ar, br), lerp(ag, bg), lerp(ab, bb))
}

// parseHexRGB parses "#rrggbb" (case-insensitive, leading '#' optional) into its
// 0..255 channels. ok is false for any malformed input. Validation stays local
// (theme.ParseHex's zero-value fallback can't be told apart from a legitimate
// "#000000"); the actual channel extraction delegates to theme.ParseHex to
// avoid duplicating the hex-decode logic.
func parseHexRGB(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	if _, err := strconv.ParseUint(s, 16, 32); err != nil {
		return 0, 0, 0, false
	}
	r, g, b = theme.ParseHex(s)
	return r, g, b, true
}

// waveTickMsg advances the wave animation one frame.
type waveTickMsg struct{}

// doneSignalMsg carries the result of one poll tick: done=true means the
// launcher's <outFile>.done close signal appeared and the thinking float should
// exit; thinking is the current trimmed content of <outFile>.thinking (the live
// model output tail), empty when that file is absent.
type doneSignalMsg struct {
	done     bool
	thinking string
}

// thinkingBackstopMsg fires after the max thinking duration — a safety net so a
// dead launcher (one that never writes .done) can't hang the float forever.
type thinkingBackstopMsg struct{}

// outWrittenMsg acknowledges that the submitted value was handed to outFile.
type outWrittenMsg struct{}

// donePollInterval is how often the thinking float re-checks for <outFile>.done.
// thinkingBackstopAfter bounds how long the float will animate without a signal.
// Both are vars so tests can shrink them; defaults match the spec (~80ms / 60s).
var (
	donePollInterval      = 80 * time.Millisecond
	thinkingBackstopAfter = 60 * time.Second
)

func waveTick() tea.Cmd {
	// tea.Every aligns ticks to the wall clock, so the cadence is steady regardless
	// of per-frame render time. tea.Tick re-schedules from "now" AFTER each frame, so
	// its interval = d + render time and jitters frame to frame. ~33ms ≈ 30fps.
	return tea.Every(33*time.Millisecond, func(time.Time) tea.Msg { return waveTickMsg{} })
}

// writeOutCmd hands the submitted value to outFile immediately (so the launcher
// can read it while the float animates). Reuses writeOutFile's atomic write.
func writeOutCmd(outFile, val string) tea.Cmd {
	return func() tea.Msg {
		if outFile != "" {
			writeOutFile(outFile, val)
		}
		return outWrittenMsg{}
	}
}

// doneExists reports whether the launcher's <outFile>.done close marker is present.
func doneExists(outFile string) bool {
	if outFile == "" {
		return false
	}
	_, err := os.Stat(outFile + DoneSuffix)
	return err == nil
}

// readThinkingLine returns the trimmed content of <outFile>.thinking (the live
// model output tail the launcher writes during the classify), or "" when the file
// is absent/unreadable or outFile is empty.
func readThinkingLine(outFile string) string {
	if outFile == "" {
		return ""
	}
	b, err := os.ReadFile(outFile + ThinkingSuffix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pollDoneCmd waits one interval then reports whether <outFile>.done exists AND the
// current <outFile>.thinking content. The model re-arms it on a "not yet" result,
// forming the poll loop, quits on a "done" result, and refreshes its thinking line
// from the carried content each tick. Driveable directly in tests (it blocks one
// interval, then stats + reads).
func pollDoneCmd(outFile string) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(donePollInterval)
		return doneSignalMsg{done: doneExists(outFile), thinking: readThinkingLine(outFile)}
	}
}

// backstopCmd fires the thinking backstop after the max duration.
func backstopCmd() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(thinkingBackstopAfter)
		return thinkingBackstopMsg{}
	}
}

// waveDemoModel is the live preview for --wave-demo. It wraps the REAL input
// model forced into the thinking state so the user can eyeball the true framing
// (outer frame, title, "Thinking…" prompt, and the field box with the wave
// interior), not just a bare wave. It is self-contained: no --out, no .done poll
// — just the wave tick — and quits on q/esc/ctrl+c.
type waveDemoModel struct{ m model }

func newWaveDemoModel(theme Theme) waveDemoModel {
	m := newInputModel(theme, "default", "ai-playbook", "How can I help you today?",
		"list the last 3 commits of last week", "", 3, 1, 1, false, "")
	m.width = 64
	m.resize()
	m.submitted = true
	m.thinking = true
	// Sample text so --wave-demo previews the dark-grey thinking line beneath the box
	// (placeholder content — the live look only).
	m.thinkingLine = "deciding: command, quick answer, or a deeper question…"
	return waveDemoModel{m: m}
}

func (d waveDemoModel) Init() tea.Cmd {
	// Start the wave tick directly (the demo skips the .done/backstop cmds).
	return tea.Batch(d.m.Init(), waveTick())
}

func (d waveDemoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "ctrl+c", "q", "esc":
			return d, tea.Quit
		}
	}
	nm, cmd := d.m.Update(msg)
	d.m = nm.(model)
	return d, cmd
}

func (d waveDemoModel) View() tea.View { return d.m.View() }

func runWaveDemo(theme Theme) {
	_, err := tea.NewProgram(
		newWaveDemoModel(theme),
		tea.WithOutput(os.Stderr),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook input: error: %v\n", err)
		os.Exit(1)
	}
}
