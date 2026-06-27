package ui

import (
	"fmt"
	"strconv"

	"github.com/alecthomas/chroma/v2"
)

// parseHex returns the r,g,b components of a "#RRGGBB" hex string.
func parseHex(hex string) (int, int, int) {
	h := hex
	if len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}
	rv, _ := strconv.ParseInt(h[0:2], 16, 32)
	gv, _ := strconv.ParseInt(h[2:4], 16, 32)
	bv, _ := strconv.ParseInt(h[4:6], 16, 32)
	return int(rv), int(gv), int(bv)
}

// darken scales a #RRGGBB color toward black by factor f (0..1); returns "#RRGGBB".
// Use f≈0.20 for a "very dark" tint.
func darken(hex string, f float64) string {
	rv, gv, bv := parseHex(hex)
	return fmt.Sprintf("#%02X%02X%02X", int(float64(rv)*f), int(float64(gv)*f), int(float64(bv)*f))
}

// bgANSI returns the truecolor background SGR sequence for a #RRGGBB hex color.
func bgANSI(hex string) string {
	rv, gv, bv := parseHex(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", rv, gv, bv)
}

// Catppuccin Mocha.
const (
	colMauve    = "#cba6f7"
	colText     = "#cdd6f4"
	colBase     = "#1e1e2e"
	colCodeBg   = "#282C41" // code block background
	colOverlay0 = "#6c7086"
	colOverlay1 = "#7f849c"
	colSurface0 = "#313244"
	colSurface1 = "#45475a" // dark grey — modal border
	colMantle   = "#181825" // darker panel — modal background
	colBlue     = "#89b4fa"
	colGreen    = "#a6e3a1"
	colPeach    = "#fab387"
	colRed      = "#f38ba8"
	colYellow   = "#f9e2af"
	colWhite    = "#ffffff" // bright white — key bindings
	colLavender = "#b4befe"
	colSky      = "#89dceb"
	colSubtext  = "#9399b2"
	colSubtext0 = "#a6adc8" // Catppuccin Mocha Subtext0 — MiniIconsGrey
	colSapphire = "#74c7ec" // Catppuccin Mocha Sapphire  — MiniIconsAzure
	colRun      = "#6495ED" // cornflower blue — run button glyph
	colStop     = colRed    // stop button glyph — same red as Catppuccin Mocha #f38ba8

	// Hint-mode label colors (flash.nvim style): bright red on dark red.
	colHintLabelFg = "#ff5555"
	colHintLabelBg = "#3a1212"

	// Flash highlight: bright background applied to a button glyph for ~140ms
	// after activation. A bright bold foreground with NO background — a background
	// on the glyph cell makes some terminals render the nerd-font (PUA) glyph
	// shifted down a row, so we pulse the foreground only.
	colFlashOn = "#ffffff" // bright white — bold flash pulse on the normal cell bg
)

// codeBgANSI is the code block background (#282C41 = R40 G44 B65) applied
// manually so it survives chroma's per-token resets.
const codeBgANSI = "\x1b[48;2;40;44;65m"

// codeFgANSI is the foreground-only version of colCodeBg (#282C41 = R40 G44 B65),
// used to draw the top/bottom edge bars with no background.
const codeFgANSI = "\x1b[38;2;40;44;65m"

// diffAddBgANSI / diffDelBgANSI are the per-line background sequences for
// hunk-style diff rendering (added / deleted lines respectively).
const diffAddBgANSI = "\x1b[48;2;42;59;46m" // #2a3b2e — dark green tint
const diffDelBgANSI = "\x1b[48;2;59;42;46m" // #3b2a2e — dark red tint

// codeStyle is a chroma style built from the Catppuccin token colors (the same
// map the glow theme uses), so code highlighting matches the rest of the UI
// regardless of whether the chroma version ships a Catppuccin style.
var catppuccinChroma = chroma.MustNewStyle("catppuccin-mocha", chroma.StyleEntries{
	chroma.Text:           colText,
	chroma.Comment:        colOverlay0,
	chroma.CommentPreproc: colBlue,
	chroma.Keyword:        colMauve,
	chroma.KeywordType:    colYellow,
	chroma.Operator:       colSky,
	chroma.Punctuation:    colSubtext,
	chroma.Name:           colLavender,
	chroma.NameBuiltin:    colPeach,
	chroma.NameFunction:   colBlue,
	chroma.NameClass:      colYellow,
	chroma.NameTag:        colMauve,
	chroma.NameAttribute:  colYellow,
	chroma.LiteralNumber:  colPeach,
	chroma.LiteralString:  colGreen,
})

func codeStyle() *chroma.Style { return catppuccinChroma }
