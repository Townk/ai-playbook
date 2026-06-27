package ui

import (
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// Window returns exactly height visible rows starting at yOffset.
// Wide lines are horizontally sliced by xOffset; prose lines ignore xOffset
// (stay anchored at column 0).
func Window(lines []Line, xOffset, yOffset, width, height int) []string {
	if width < 1 {
		width = 1
	}
	out := make([]string, 0, height)
	for row := 0; row < height; row++ {
		idx := yOffset + row
		if idx < 0 || idx >= len(lines) {
			out = append(out, "")
			continue
		}
		l := lines[idx]
		if !l.Wide {
			out = append(out, l.Text)
			continue
		}
		sl := hslice(l.Text, xOffset, width)
		if l.Bg != "" {
			sl = band(sl, l.Bg, width)
		}
		out = append(out, sl)
	}
	return out
}

// hslice returns the display columns [start, start+width) of an ANSI string.
// It walks the string rune-by-rune, passing SGR/CSI escape sequences through
// verbatim (they have zero display width) while counting visible columns.
func hslice(s string, start, width int) string {
	end := start + width
	var out []byte
	col := 0 // current display column
	i := 0
	n := len(s)
	for i < n {
		if s[i] == 0x1b && i+1 < n && s[i+1] == '[' {
			// CSI sequence: ESC [ ... <final byte 0x40–0x7e>
			// Copy verbatim; zero display width.
			j := i
			i += 2 // skip ESC [
			for i < n && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < n {
				i++ // include final byte
			}
			// Always emit SGR/CSI escape sequences verbatim: they have zero display
			// width and thread colour state into (and through) the visible slice.
			out = append(out, s[j:i]...)
			continue
		}

		// Decode a UTF-8 rune manually.
		r, size := decodeRune(s, i)
		w := runewidth.RuneWidth(r)

		if col+w > start && col < end {
			// At least partially inside the window — emit the rune.
			out = append(out, s[i:i+size]...)
		}
		col += w
		i += size

		// Keep scanning to the end so trailing zero-width escapes (the closing reset) are emitted; out-of-window runes are already skipped by the col<end guard above.
	}
	return string(out)
}

// spliceOver overlays `over` onto `base` starting at display column `col`,
// replacing base's columns [col, col+width(over)) and preserving base's styling
// on both sides (ANSI-aware). base should be at least col+width(over) columns
// wide; columns beyond base are simply absent.
func spliceOver(base, over string, col int) string {
	w := lipgloss.Width(over)
	return hslice(base, 0, col) + over + hslice(base, col+w, 1<<30)
}

// decodeRune decodes a UTF-8 rune from s[i:], returning the rune and its byte
// width. Falls back to a single byte on invalid sequences.
func decodeRune(s string, i int) (rune, int) {
	b := s[i]
	if b < 0x80 {
		return rune(b), 1
	}
	// Multi-byte UTF-8.
	var size int
	switch {
	case b < 0xE0:
		size = 2
	case b < 0xF0:
		size = 3
	default:
		size = 4
	}
	if i+size > len(s) {
		return rune(b), 1
	}
	var r rune
	switch size {
	case 2:
		r = rune(b&0x1F)<<6 | rune(s[i+1]&0x3F)
	case 3:
		r = rune(b&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
	case 4:
		r = rune(b&0x07)<<18 | rune(s[i+1]&0x3F)<<12 | rune(s[i+2]&0x3F)<<6 | rune(s[i+3]&0x3F)
	}
	return r, size
}

// MaxWideWidth returns the widest Wide line's display width (0 if none).
func MaxWideWidth(lines []Line) int {
	max := 0
	for _, l := range lines {
		if l.Wide {
			if w := lipgloss.Width(l.Text); w > max {
				max = w
			}
		}
	}
	return max
}
