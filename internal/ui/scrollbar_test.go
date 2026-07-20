package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestVThumb(t *testing.T) {
	if p, s := vthumb(10, 20, 0); p != 0 || s != 0 {
		t.Fatalf("fits → (0,0), got (%d,%d)", p, s)
	}
	if _, s := vthumb(100, 10, 0); s < 1 {
		t.Fatalf("size must be ≥1, got %d", s)
	}
	if p, _ := vthumb(100, 10, 0); p != 0 {
		t.Fatalf("top → pos 0, got %d", p)
	}
	p, s := vthumb(100, 10, 90) // maxOff = 90
	if p != 10-s {
		t.Fatalf("bottom → pos == visible-size (%d), got %d", 10-s, p)
	}
}

func TestHThumb(t *testing.T) {
	if p, s := hthumb(40, 80, 5); p != 0 || s != 80 {
		t.Fatalf("blockW≤view → full track (0,view), got (%d,%d)", p, s)
	}
	if p, _ := hthumb(200, 80, 0); p != 0 {
		t.Fatalf("xoff 0 → pos 0, got %d", p)
	}
	p, s := hthumb(200, 80, 120) // maxX = 120
	if p != 80-s {
		t.Fatalf("max xoff → pos == view-size (%d), got %d", 80-s, p)
	}
	if p, _ := hthumb(200, 80, 9999); p != 80-s2(200, 80) {
		t.Fatalf("xoff clamps to maxX")
	}
}

func s2(b, v int) int { _, s := hthumb(b, v, 1<<30); return s }

func TestHScrollbarRowWidthAndGlyphs(t *testing.T) {
	row := hscrollbarRow(200, 0, 40, colCodeBg)
	if lipgloss.Width(row) != 40 {
		t.Fatalf("width = %d, want 40", lipgloss.Width(row))
	}
	plain := []rune(strip(row))
	// 1 leading + 1 trailing pad space so the bar floats inside the block.
	if plain[0] != ' ' || plain[len(plain)-1] != ' ' {
		t.Fatalf("want leading+trailing pad space; got %q", string(plain))
	}
	thumbN := 0
	for _, r := range plain[1 : len(plain)-1] {
		if r != '─' && r != '━' {
			t.Fatalf("inner glyph %q not ─/━", r)
		}
		if r == '━' {
			thumbN++
		}
	}
	_, size := hthumb(200, 40-2, 0) // inner = cw-2
	if thumbN != size {
		t.Fatalf("thumb run = %d, want %d", thumbN, size)
	}
}

func TestPadTo(t *testing.T) {
	if got := padTo("ab", 5); lipgloss.Width(got) != 5 || strip(got) != "ab   " {
		t.Fatalf("padTo = %q", strip(got))
	}
	if got := padTo("abcdef", 3); strip(got) != "abcdef" {
		t.Fatalf("padTo must not truncate, got %q", strip(got))
	}
}

func TestHintCodeRow(t *testing.T) {
	row := "\x1b[38;2;1;2;3mhi\x1b[0m"
	got := hintCodeRow(row, 6, nil)
	if lipgloss.Width(got) != 6 {
		t.Fatalf("width = %d, want 6", lipgloss.Width(got))
	}
	if strip(got) != "hi    " {
		t.Fatalf("strip = %q, want %q", strip(got), "hi    ")
	}
	// lipgloss may emit the bg inside a combined fg+bg SGR, so match the bg
	// color params (#282C41 → 48;2;40;44;65) rather than the standalone escape.
	const codeBgParams = "48;2;40;44;65"
	if !strings.Contains(got, codeBgParams) {
		t.Fatal("content must paint the code-bg fill")
	}
	// Border glyphs (▂ / 🮂) keep their normal color and get NO bg fill, so a
	// pure border row (the bottom bar) is unchanged.
	bar := hintCodeRow(strings.Repeat("\U0001FB82", 6), 6, nil)
	if strings.Contains(bar, codeBgParams) {
		t.Fatal("border glyphs must not get the code-bg fill")
	}
	if strip(bar) != strings.Repeat("\U0001FB82", 6) {
		t.Fatalf("border row strip = %q", strip(bar))
	}
	// No per-cell button treatment: the overlapping label chip is the only
	// dark-red marking, so a plain content row must never carry the label bg
	// (a letter-less button would otherwise read as a phantom hint).
	if strings.Contains(got, "48;2;58;18;18") {
		t.Fatal("content rows must not carry the dark-red label background")
	}
	// A pill span keeps its filled shape via the inverted trick: the body cells
	// get the solid colSurface0 fill (#313244) and the cap cells take that fill
	// color as their foreground over the row's code bg — never an empty center.
	pill := hintCodeRow("\U0000E0B6\U000F0450 go\U0000E0B4x", 8, [][2]int{{0, 6}})
	const surfaceBgParams = "48;2;49;50;68" // colSurface0 #313244
	const surfaceFgParams = "38;2;49;50;68"
	if !strings.Contains(pill, surfaceBgParams) {
		t.Fatal("pill body must get the solid colSurface0 fill")
	}
	if !strings.Contains(pill, surfaceFgParams) {
		t.Fatal("pill caps must take the fill color as their foreground")
	}
}

func TestOverlayLabels(t *testing.T) {
	lab := lipgloss.NewStyle().Bold(true)
	row := "abcde"
	got := overlayLabels(row, map[int]string{2: "X"}, lab)
	if strip(got) != "abXde" {
		t.Fatalf("strip = %q, want abXde", strip(got))
	}
	if overlayLabels(row, nil, lab) != row {
		t.Fatal("no labels → unchanged")
	}
}
