package input

import (
	"flag"

	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/theme"
)

// Theme is the resolved color palette. Defaults are Catppuccin Mocha (today's
// exact values); each field is overridable via a --theme-* flag.
type Theme struct {
	Accent      string // default title (▓▓▓)
	Border      string // default border / focus
	Danger      string // danger variant
	Warning     string // warning variant
	Base        string // dark fg on warning's focused button
	Text        string // body text
	Muted       string // hint words / comment
	Rule        string // title rule, scroll track
	Key         string // hint key bindings
	FieldBorder string // inner input box (line/text)
	ButtonBg    string
	ButtonFg    string
	ButtonSelBg string
	ButtonSelFg string
	ScrollThumb string
}

func defaultTheme() Theme {
	return Theme{
		Accent:      theme.Mauve,
		Border:      theme.Blue,
		Danger:      "#ff5555",
		Warning:     "#e5bf7b",
		Base:        theme.Base,
		Text:        theme.Text,
		Muted:       theme.Overlay0,
		Rule:        theme.Surface0,
		Key:         "#ffffff",
		FieldBorder: "#585b70",
		ButtonBg:    "#282c41",
		ButtonFg:    "#9b9fc1",
		ButtonSelBg: "#656a83",
		ButtonSelFg: "#ffffff",
		ScrollThumb: "#7f849c",
	}
}

// registerThemeFlags binds --theme-* flags onto fs (defaulting to today's
// palette) and returns the Theme they write into. Call after fs.Parse.
func registerThemeFlags(fs *flag.FlagSet) *Theme {
	t := defaultTheme()
	fs.StringVar(&t.Accent, "theme-accent", t.Accent, "title accent color (hex)")
	fs.StringVar(&t.Border, "theme-border", t.Border, "default border/focus color (hex)")
	fs.StringVar(&t.Danger, "theme-danger", t.Danger, "danger variant color (hex)")
	fs.StringVar(&t.Warning, "theme-warning", t.Warning, "warning variant color (hex)")
	fs.StringVar(&t.Base, "theme-base", t.Base, "dark fg for warning focused button (hex)")
	fs.StringVar(&t.Text, "theme-text", t.Text, "body text color (hex)")
	fs.StringVar(&t.Muted, "theme-muted", t.Muted, "hint words / comment color (hex)")
	fs.StringVar(&t.Rule, "theme-rule", t.Rule, "title rule / scroll track color (hex)")
	fs.StringVar(&t.Key, "theme-key", t.Key, "hint key-binding color (hex)")
	fs.StringVar(&t.FieldBorder, "theme-field-border", t.FieldBorder, "inner input box border (hex)")
	fs.StringVar(&t.ButtonBg, "theme-button-bg", t.ButtonBg, "unselected button bg (hex)")
	fs.StringVar(&t.ButtonFg, "theme-button-fg", t.ButtonFg, "unselected button fg (hex)")
	fs.StringVar(&t.ButtonSelBg, "theme-button-sel-bg", t.ButtonSelBg, "selected button bg (hex)")
	fs.StringVar(&t.ButtonSelFg, "theme-button-sel-fg", t.ButtonSelFg, "selected button fg (hex)")
	fs.StringVar(&t.ScrollThumb, "theme-scroll-thumb", t.ScrollThumb, "scroll thumb color (hex)")
	return &t
}

func (t Theme) variantColor(variant string) string {
	switch variant {
	case "danger":
		return t.Danger
	case "warning":
		return t.Warning
	default:
		return t.Border
	}
}

func (t Theme) titleColor(variant string) string {
	switch variant {
	case "danger":
		return t.Danger
	case "warning":
		return t.Warning
	default:
		return t.Accent
	}
}

// promptStyle is the style every dialog prompt/description section must use:
// the theme's body-text foreground on the dialog's Mantle background. A
// foreground-only style (no Background) emits a bare SGR reset after its
// content, which drops those cells to the terminal's default background
// instead of the enclosing frame's Mantle fill — renderFrame's own
// Background(Mantle) wrapper (frame.go) does not protect against an inner
// style's reset. Every prompt/body section rendered inside a Mantle frame
// must carry this same background to avoid that bleed.
func promptStyle(t Theme) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Text)).
		Background(lipgloss.Color(theme.Mantle))
}

// hintFrameBG is the background hint segments paint on inside a frame, so their
// per-segment SGR resets don't drop to the terminal default (the same bleed
// promptStyle prevents for the prompt body). The inline/unframed layout passes
// "" instead — it composites on the terminal background, not Mantle.
const hintFrameBG = theme.Mantle

// hintKW returns the key (accelerator) and word (description) styles for a hint
// line. When bg is non-empty the styles paint on it; pass hintFrameBG inside a
// frame and "" for the inline layout.
func hintKW(t Theme, bg string) (key, word lipgloss.Style) {
	key = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Key))
	word = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	if bg != "" {
		key = key.Background(lipgloss.Color(bg))
		word = word.Background(lipgloss.Color(bg))
	}
	return key, word
}
