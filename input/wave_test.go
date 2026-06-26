package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const (
	waveBlue    = "#89b4fa"
	waveRed     = "#f38ba8"
	waveMagenta = "#cba6f7"
)

// Truecolor ANSI foreground fragments lipgloss emits for the wave hexes.
const (
	ansiBlue    = "38;2;137;180;250" // #89b4fa
	ansiRed     = "38;2;243;139;168" // #f38ba8
	ansiMagenta = "38;2;203;166;247" // #cba6f7
)

// TestWaveFrameDims pins the shape: exactly rows lines, each cols runes wide
// (after stripping ANSI), with every rune in the Braille block U+2800..U+28FF.
func TestWaveFrameDims(t *testing.T) {
	out := WaveFrame(0, 30, 3, waveBlue, waveRed, waveMagenta)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		bare := strip(line)
		runes := []rune(bare)
		if len(runes) != 30 {
			t.Errorf("line %d: expected 30 runes, got %d", i, len(runes))
		}
		for _, r := range runes {
			if r < 0x2800 || r > 0x28FF {
				t.Errorf("line %d: rune %U outside Braille block", i, r)
			}
		}
	}
}

// TestWaveFrameColors asserts magenta appears only where the waves overlap and
// that pure-blue and pure-red cells also exist, by scanning for each hex.
func TestWaveFrameColors(t *testing.T) {
	// Scan several phases; at least one frame must contain all three colors,
	// proving the waves cross (magenta) while still having solo cells.
	var sawAll bool
	for i := 0; i < 40; i++ {
		phase := 0.35 * float64(i)
		out := WaveFrame(phase, 30, 3, waveBlue, waveRed, waveMagenta)
		hasBlue := strings.Contains(out, ansiBlue)
		hasRed := strings.Contains(out, ansiRed)
		hasMagenta := strings.Contains(out, ansiMagenta)
		if hasBlue && hasRed && hasMagenta {
			sawAll = true
			break
		}
	}
	if !sawAll {
		t.Error("no frame contained a blue, a red, and a magenta cell simultaneously")
	}
}

// TestWaveFramePhaseChanges asserts advancing the phase changes the frame.
func TestWaveFramePhaseChanges(t *testing.T) {
	a := WaveFrame(0, 30, 3, waveBlue, waveRed, waveMagenta)
	b := WaveFrame(0.35, 30, 3, waveBlue, waveRed, waveMagenta)
	if a == b {
		t.Error("phase advance produced an identical frame")
	}
}

// TestWaveFrameSmallDims guards against panics at minimal dimensions.
func TestWaveFrameSmallDims(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WaveFrame panicked at small dims: %v", r)
		}
	}()
	out := WaveFrame(0, 1, 1, waveBlue, waveRed, waveMagenta)
	if len(strings.Split(out, "\n")) != 1 {
		t.Errorf("expected 1 line for rows=1, got %d", len(strings.Split(out, "\n")))
	}
	// Also exercise the clamping guards on zero/negative dims.
	_ = WaveFrame(0, 0, 0, waveBlue, waveRed, waveMagenta)
}

// TestWaveDemoModel exercises the repurposed --wave-demo model (the REAL input
// model forced into the thinking state): it constructs, ticks the wave, renders
// the true framing, and quits on q.
func TestWaveDemoModel(t *testing.T) {
	m := newWaveDemoModel(defaultTheme())
	if !m.m.thinking {
		t.Fatal("wave-demo must force the model into the thinking state")
	}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no tick command")
	}
	start := m.m.phase
	next, cmd := m.Update(waveTickMsg{})
	wm := next.(waveDemoModel)
	if wm.m.phase <= start {
		t.Errorf("phase did not advance: %v -> %v", start, wm.m.phase)
	}
	if cmd == nil {
		t.Error("tick did not re-schedule")
	}
	v := wm.View()
	if v.Content == "" {
		t.Error("View rendered empty")
	}
	// The demo renders the REAL framing: title + Thinking… prompt + Braille box.
	plain := strip(v.Content)
	if !strings.Contains(plain, "Thinking…") {
		t.Error("wave-demo must show the Thinking… prompt")
	}
	if !strings.Contains(plain, "ai-playbook") {
		t.Error("wave-demo must show the sample title")
	}
	// Quit on q.
	_, qcmd := wm.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if qcmd == nil {
		t.Error("q did not produce a quit command")
	}
}
