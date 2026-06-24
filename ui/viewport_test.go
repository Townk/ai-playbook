package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWindowProseIgnoresXOffset(t *testing.T) {
	lines := []Line{{Text: "hello", Wide: false}}
	out := Window(lines, 3 /*xOffset*/, 0, 10, 1)
	if len(out) != 1 || out[0] != "hello" {
		t.Fatalf("prose should ignore xOffset, got %q", out)
	}
}

func TestWindowWideAppliesXOffset(t *testing.T) {
	lines := []Line{{Text: "0123456789", Wide: true}}
	out := Window(lines, 4 /*xOffset*/, 0, 3 /*width*/, 1)
	if len(out) != 1 || strip(out[0]) != "456" {
		t.Fatalf("wide line should show slice [4:7] = 456, got %q", strip(out[0]))
	}
}

func TestWindowVerticalOffsetAndHeightPadding(t *testing.T) {
	lines := []Line{{Text: "a"}, {Text: "b"}, {Text: "c"}}
	out := Window(lines, 0, 1 /*yOffset*/, 10, 4 /*height*/)
	if len(out) != 4 {
		t.Fatalf("expected 4 rows (padded), got %d", len(out))
	}
	if out[0] != "b" || out[1] != "c" || out[2] != "" || out[3] != "" {
		t.Fatalf("unexpected window: %#v", out)
	}
}

func TestHsliceKeepsTrailingResetWhenWindowEndsBeforeReset(t *testing.T) {
	// bg + "ABCDE" + reset, sliced to width 3 → visible "ABC" but the closing
	// reset must survive so the background doesn't bleed past the slice.
	line := "\x1b[48;2;1;1;1mABCDE\x1b[0m"
	got := Window([]Line{{Text: line, Wide: true}}, 0, 0, 3, 1)[0]
	if strip(got) != "ABC" {
		t.Fatalf("visible content = %q, want %q", strip(got), "ABC")
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("trailing reset dropped (bg would bleed): %q", got)
	}
}

func TestMaxWideWidth(t *testing.T) {
	lines := []Line{{Text: "short", Wide: false}, {Text: "0123456789", Wide: true}, {Text: "012", Wide: true}}
	if got := MaxWideWidth(lines); got != 10 {
		t.Fatalf("MaxWideWidth = %d, want 10", got)
	}
	if got := MaxWideWidth([]Line{{Text: "p", Wide: false}}); got != 0 {
		t.Fatalf("MaxWideWidth with no wide lines = %d, want 0", got)
	}
}

func TestWindowBgBackdropFillsWhenScrolledPastText(t *testing.T) {
	l := Line{Text: "\x1b[38;2;1;2;3mABC\x1b[0m", Wide: true, Bg: "\x1b[48;2;9;9;9m"}
	out := Window([]Line{l}, 5 /*xOff past the 3-col text*/, 0, 10, 1)[0]
	if lipgloss.Width(out) != 10 {
		t.Fatalf("backdrop should fill 10 cols, got %d", lipgloss.Width(out))
	}
	if !strings.HasPrefix(out, "\x1b[48;2;9;9;9m") {
		t.Fatalf("missing bg backdrop")
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Fatalf("missing closing reset")
	}
}

