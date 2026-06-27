package input

import "flag"

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
		Accent:      "#cba6f7",
		Border:      "#89b4fa",
		Danger:      "#ff5555",
		Warning:     "#e5bf7b",
		Base:        "#1e1e2e",
		Text:        "#cdd6f4",
		Muted:       "#6c7086",
		Rule:        "#313244",
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
