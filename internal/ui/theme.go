package ui

import (
	"github.com/alecthomas/chroma/v2"

	"github.com/Townk/ai-playbook/pkg/dialog/theme"
)

// Shared Catppuccin Mocha palette — aliased from pkg/dialog/theme so every
// existing colXxx call site stays unchanged.
const (
	colBlue     = theme.Blue
	colGreen    = theme.Green
	colMauve    = theme.Mauve
	colPeach    = theme.Peach
	colRed      = theme.Red
	colOverlay0 = theme.Overlay0
	colMantle   = theme.Mantle
	colSurface1 = theme.Surface1
	colSurface0 = theme.Surface0
	colBase     = theme.Base
	colText     = theme.Text
	colCodeBg   = theme.CodeBg

	// ui-only Catppuccin Mocha colors.
	colOverlay1 = "#7f849c"
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

	// Action-pill body foregrounds: the text/icon INSIDE a colored powerline
	// pill. The rule: a darker shade of the pill's own hue when the pill color
	// is bright, a lighter shade when it's dark.
	//   - colPeach (#fab387) is bright → a hand-tuned dark warm brown of the
	//     same hue; reads clearly on the peach body (~5:1 contrast) without
	//     collapsing to black like colBase would.
	colPillPeachFg = "#5c3212"
	//   - colBlue (#89b4fa) is mid-bright → the darker treatment reads better
	//     than a lighter tint (a light blue on blue washes out); hand-tuned
	//     dark navy of the same hue (~5:1 contrast).
	colPillBlueFg = "#16325c"

	// Flash highlight: bright background applied to a button glyph for ~140ms
	// after activation. A bright bold foreground with NO background — a background
	// on the glyph cell makes some terminals render the nerd-font (PUA) glyph
	// shifted down a row, so we pulse the foreground only.
	colFlashOn = "#ffffff" // bright white — bold flash pulse on the normal cell bg
)

// Helpers aliased from pkg/dialog/theme so every existing call site stays
// unchanged (var keeps the function signature identical to the original func).
var (
	parseHex = theme.ParseHex
	darken   = theme.Darken
	bgANSI   = theme.BgANSI
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
