package askcli

import (
	"flag"
	"os"
	"strings"

	"github.com/Townk/ai-playbook/internal/input"
)

// envName maps a theme flag name to its ASK_* environment fallback:
// "theme-accent" → "ASK_THEME_ACCENT".
func envName(flag string) string {
	return "ASK_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// themeField describes one --theme-* flag: its name, help summary, and an
// accessor into the input.Theme it writes. The list is the single source for
// both flag registration (env.go) and docs metadata (meta.go).
type themeField struct {
	flag    string
	summary string
	get     func(*input.Theme) *string
}

var themeFields = []themeField{
	{"theme-accent", "title accent color (hex)", func(t *input.Theme) *string { return &t.Accent }},
	{"theme-border", "default border/focus color (hex)", func(t *input.Theme) *string { return &t.Border }},
	{"theme-danger", "danger variant color (hex)", func(t *input.Theme) *string { return &t.Danger }},
	{"theme-warning", "warning variant color (hex)", func(t *input.Theme) *string { return &t.Warning }},
	{"theme-base", "dark fg for warning focused button (hex)", func(t *input.Theme) *string { return &t.Base }},
	{"theme-text", "body text color (hex)", func(t *input.Theme) *string { return &t.Text }},
	{"theme-muted", "hint words / comment color (hex)", func(t *input.Theme) *string { return &t.Muted }},
	{"theme-rule", "title rule / scroll track color (hex)", func(t *input.Theme) *string { return &t.Rule }},
	{"theme-key", "hint key-binding color (hex)", func(t *input.Theme) *string { return &t.Key }},
	{"theme-field-border", "inner input box border (hex)", func(t *input.Theme) *string { return &t.FieldBorder }},
	{"theme-button-bg", "unselected button bg (hex)", func(t *input.Theme) *string { return &t.ButtonBg }},
	{"theme-button-fg", "unselected button fg (hex)", func(t *input.Theme) *string { return &t.ButtonFg }},
	{"theme-button-sel-bg", "selected button bg (hex)", func(t *input.Theme) *string { return &t.ButtonSelBg }},
	{"theme-button-sel-fg", "selected button fg (hex)", func(t *input.Theme) *string { return &t.ButtonSelFg }},
	{"theme-scroll-thumb", "scroll thumb color (hex)", func(t *input.Theme) *string { return &t.ScrollThumb }},
}

// registerTheme binds the --theme-* flags onto fs with ASK_* env fallbacks and
// returns the input.Theme they write into. Precedence is flag > env > built-in
// default: each flag's default is seeded from its ASK_* env var when set, so a
// passed flag overrides the env, the env overrides the built-in default, and an
// absent env leaves the built-in default. Call before fs.Parse.
func registerTheme(fs *flag.FlagSet) *input.Theme {
	t := input.DefaultTheme()
	for _, tf := range themeFields {
		ptr := tf.get(&t)
		def := *ptr // built-in default from input.DefaultTheme()
		if v, ok := os.LookupEnv(envName(tf.flag)); ok {
			def = v
		}
		fs.StringVar(ptr, tf.flag, def, tf.summary)
	}
	return &t
}

// common holds the cross-cutting flags every subcommand accepts.
type common struct {
	title   string
	width   int
	padding int
	inset   int
	measure bool
	theme   *input.Theme
}

// registerCommon binds the cross-cutting flags (--title/--width/--padding/
// --inset/--measure) plus the theme flags onto fs. Defaults mirror
// `ai-playbook input` so --measure heights match byte-for-byte.
func registerCommon(fs *flag.FlagSet) *common {
	c := &common{}
	fs.StringVar(&c.title, "title", "", "modal title")
	fs.IntVar(&c.width, "width", 50, "pane width for measurement/sizing")
	fs.IntVar(&c.padding, "padding", 1, "frame vertical padding rows")
	fs.IntVar(&c.inset, "inset", 1, "frame inter-section blank rows")
	fs.BoolVar(&c.measure, "measure", false, "print the rendered height and exit (no TUI)")
	c.theme = registerTheme(fs)
	return c
}
