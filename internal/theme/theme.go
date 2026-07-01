// Package theme is the single Catppuccin Mocha palette shared by the viewer
// (internal/ui) and the dialog widgets (internal/input).
package theme

import (
	"fmt"
	"strconv"
)

const (
	Blue     = "#89b4fa"
	Green    = "#a6e3a1"
	Mauve    = "#cba6f7"
	Peach    = "#fab387"
	Red      = "#f38ba8"
	Overlay0 = "#6c7086"
	Mantle   = "#181825"
	Surface1 = "#45475a"
	Surface0 = "#313244"
	Base     = "#1e1e2e"
	Text     = "#cdd6f4"
	CodeBg   = "#282C41"
)

// ParseHex returns the r,g,b components of a "#RRGGBB" hex string.
func ParseHex(hex string) (int, int, int) {
	h := hex
	if len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}
	rv, _ := strconv.ParseInt(h[0:2], 16, 32)
	gv, _ := strconv.ParseInt(h[2:4], 16, 32)
	bv, _ := strconv.ParseInt(h[4:6], 16, 32)
	return int(rv), int(gv), int(bv)
}

// Darken scales a #RRGGBB color toward black by factor f (0..1); returns "#RRGGBB".
// f=0 → black, f=1 → unchanged. Use f≈0.20 for a "very dark" tint.
func Darken(hex string, f float64) string {
	rv, gv, bv := ParseHex(hex)
	return fmt.Sprintf("#%02X%02X%02X", int(float64(rv)*f), int(float64(gv)*f), int(float64(bv)*f))
}

// BgANSI returns the truecolor background SGR sequence for a #RRGGBB hex color.
func BgANSI(hex string) string {
	rv, gv, bv := ParseHex(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", rv, gv, bv)
}

// FgANSI returns the truecolor foreground SGR sequence for a #RRGGBB hex color.
func FgANSI(hex string) string {
	rv, gv, bv := ParseHex(hex)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", rv, gv, bv)
}
