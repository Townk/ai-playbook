package ui

import "strings"

type langIconDef struct {
	glyph string // nerd-font glyph (verbatim from mini.icons)
	color string // "#RRGGBB" — catppuccin-mocha palette
}

// langIcons maps a CANONICAL language name to its icon. Glyphs are copied
// verbatim from mini.icons (lua/mini/icons.lua); colors are the catppuccin-mocha
// equivalents of the MiniIcons highlight groups:
//
//	MiniIconsRed    → colRed      #f38ba8
//	MiniIconsGreen  → colGreen    #a6e3a1
//	MiniIconsBlue   → colBlue     #89b4fa
//	MiniIconsAzure  → colSapphire #74c7ec
//	MiniIconsCyan   → colSky      #89dceb
//	MiniIconsYellow → colYellow   #f9e2af
//	MiniIconsOrange → colPeach    #fab387
//	MiniIconsPurple → colMauve    #cba6f7
//	MiniIconsGrey   → colSubtext0 #a6adc8
var langIcons = map[string]langIconDef{
	// shells — sh/bash/zsh share one terminal glyph (nf-dev-terminal), all green
	"bash": {"\U0000E795", colGreen},
	"zsh":  {"\U0000E795", colGreen},
	"sh":   {"\U0000E795", colGreen},

	// diffs
	"diff": {"\U000F0993", colRed},

	// scripting / general
	"python":     {"\U000F0320", colYellow},
	"javascript": {"\U000F031E", colYellow},
	"typescript": {"\U000F06E6", colSapphire},
	"lua":        {"\U000F024B", colBlue},

	// data / config
	"json":     {"\U000F0626", colYellow},
	"toml":     {"\U0000E6B2", colPeach},
	"yaml":     {"\U0000E8EB", colMauve},
	"text":     {"\U000F09AA", colYellow},
	"markdown": {"\U000F0354", colSubtext0},

	// systems
	"go":   {"\U0000E627", colSapphire},
	"rust": {"\U000F1617", colPeach},
	"c":    {"\U0000E61E", colBlue},
	"cpp":  {"\U0000E61D", colSapphire},

	// keep a few popular entries from the previous table
	"java":       {"󰬷", colPeach},
	"ruby":       {"󰴭", colRed},
	"php":        {"", colMauve},
	"html":       {"", colPeach},
	"css":        {"", colBlue},
	"sql":        {"", colSubtext0},
	"dockerfile": {"", colBlue},
	"kotlin":     {"", colMauve},
	"swift":      {"", colPeach},
	"xml":        {"󰗀", colPeach},
	"ini":        {"󰒓", colSubtext0},
	"nix":        {"", colBlue},
	"make":       {"", colSubtext0},
	"plantuml":   {"", colPeach},
	"mermaid":    {"💹", colRed},
}

// langAliases maps the strings authors actually write in a fence to the
// canonical key above.
var langAliases = map[string]string{
	"py": "python", "js": "javascript", "ts": "typescript",
	"rs": "rust",
	// patch reuses diff's glyph/color
	"patch": "diff",
	// console / shell-session: treat as sh (grey, no run glyph either)
	"console": "sh", "shell-session": "sh",
	// c++ spellings
	"c++": "cpp", "cxx": "cpp",
	// other common aliases
	"rb": "ruby", "md": "markdown", "yml": "yaml", "kt": "kotlin",
	"docker": "dockerfile", "makefile": "make",
	"puml": "plantuml", "uml": "plantuml", "mmd": "mermaid",
	"conf": "ini", "cfg": "ini", "config": "ini", "editorconfig": "ini",
}

// langIcon returns the glyph + color for a fenced-code language. ok is false
// when there's no icon (caller may fall back to the text label). Matching is
// case-insensitive and alias-aware.
func langIcon(lang string) (glyph string, color string, ok bool) {
	key := strings.ToLower(strings.TrimSpace(lang))
	if canon, isAlias := langAliases[key]; isAlias {
		key = canon
	}
	if def, found := langIcons[key]; found {
		return def.glyph, def.color, true
	}
	return "", "", false
}

// langIconOrDefault returns the glyph and color for lang. For unknown languages
// it returns an empty glyph — callers that check for an empty glyph should skip
// the icon cell entirely (no reserved column).
func langIconOrDefault(lang string) (glyph, color string) {
	if g, c, ok := langIcon(lang); ok {
		return g, c
	}
	return "", colSubtext0
}
