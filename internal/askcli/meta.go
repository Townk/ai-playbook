package askcli

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
)

// FlagSpec is one documented flag of a subcommand: its long name (without the
// leading --), an Arg metavar ("" for boolean flags), a one-line Summary, and
// the ASK_* env fallback var name ("" when the flag has none).
type FlagSpec struct {
	Name    string
	Arg     string
	Summary string
	Env     string
}

// Command is the documentation metadata for one `ask` subcommand. A2's docgen
// generates the man page and completion from this table (mirroring the climeta
// pattern) — keep it in sync with the per-subcommand flag sets.
type Command struct {
	Name    string
	Summary string
	Args    string // positional-argument usage, e.g. `"Pick one" <item>...`
	Flags   []FlagSpec
}

// Commands is the ordered subcommand table. It is the data source for help text
// and (in A2) man/completion generation.
var Commands = []Command{
	{
		Name:    "confirm",
		Summary: "yes/no dialog; the exit code is the answer (0 yes, 1 no, 130 cancel)",
		Args:    `"Prompt"`,
		Flags: []FlagSpec{
			{Name: "danger", Summary: "danger variant (forces default=negative)"},
			{Name: "warning", Summary: "warning variant"},
			{Name: "affirmative", Arg: "<label>", Summary: "affirmative button label (default Yes)"},
			{Name: "negative", Arg: "<label>", Summary: "negative button label (default No)"},
			{Name: "default", Arg: "affirmative|negative", Summary: "default focus (default affirmative)"},
			{Name: "print", Summary: "also print yes/no to stdout"},
		},
	},
	{
		Name:    "line",
		Summary: "one-line input; the submitted value is printed to stdout",
		Args:    `"Prompt"`,
		Flags: []FlagSpec{
			{Name: "value", Arg: "<v>", Summary: "initial value"},
			{Name: "placeholder", Arg: "<p>", Summary: "placeholder text"},
			{Name: "icon", Arg: "<glyph>", Summary: "prompt-column glyph override"},
		},
	},
	{
		Name:    "text",
		Summary: "multi-line editor; the submitted value is printed to stdout",
		Args:    `"Prompt"`,
		Flags: []FlagSpec{
			{Name: "value", Arg: "<v>", Summary: "initial value"},
			{Name: "height", Arg: "<rows>", Summary: "editor viewport rows (default 10)"},
			{Name: "icon", Arg: "<glyph>", Summary: "prompt-column glyph override"},
		},
	},
	{
		Name:    "choose",
		Summary: "select from items; selection printed to stdout (--multi one per line)",
		Args:    `"Prompt" <item>...`,
		Flags: []FlagSpec{
			{Name: "multi", Summary: "allow multiple selections (one per line)"},
			{Name: "other", Arg: "<label>", Summary: "enable a free-text entry row"},
		},
	},
	{
		Name:    "form",
		Summary: "multi-field form from a JSON spec; key=value lines (or --json)",
		Args:    "",
		Flags: []FlagSpec{
			{Name: "spec", Arg: "<file>", Summary: "JSON spec file; omit to read stdin"},
			{Name: "json", Summary: "emit one JSON object instead of key=value lines"},
		},
	},
}

// crossCuttingFlags documents the flags every subcommand accepts.
var crossCuttingFlags = []FlagSpec{
	{Name: "title", Arg: "<t>", Summary: "modal title"},
	{Name: "width", Arg: "<cols>", Summary: "pane width for measurement/sizing"},
	{Name: "padding", Arg: "<rows>", Summary: "frame vertical padding rows"},
	{Name: "inset", Arg: "<rows>", Summary: "frame inter-section blank rows"},
	{Name: "measure", Summary: "print the rendered height and exit (no TUI)"},
}

// ThemeFlags returns the --theme-* flags, each carrying its ASK_* env fallback,
// as documentation metadata. Exported for A2's docgen.
func ThemeFlags() []FlagSpec {
	out := make([]FlagSpec, len(themeFields))
	for i, tf := range themeFields {
		out[i] = FlagSpec{Name: tf.flag, Arg: "<hex>", Summary: tf.summary, Env: envName(tf.flag)}
	}
	return out
}

// CrossCuttingFlags returns a copy of the cross-cutting flag metadata. Exported
// for A2's docgen.
func CrossCuttingFlags() []FlagSpec {
	out := make([]FlagSpec, len(crossCuttingFlags))
	copy(out, crossCuttingFlags)
	return out
}

// usage writes the top-level help to w.
func usage(w io.Writer) {
	fmt.Fprintln(w, "ask — themed terminal dialogs for scripts (confirm/line/text/choose/form)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  ask <command> [args] [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range Commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit codes: 0 submit/affirmative · 1 confirm-negative · 130 cancel · 2 usage error")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Every subcommand also accepts --title/--width/--padding/--inset/--measure and the")
	fmt.Fprintln(w, "--theme-* flags (each with an ASK_<FLAG> environment fallback; flag > env > default).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'ask <command> -h' for a command's flags, or 'ask --version'.")
}

// commandUsage writes a subcommand's help (its own flags) to w.
func commandUsage(w io.Writer, fs *flag.FlagSet) {
	var cmd *Command
	for i := range Commands {
		if Commands[i].Name == fs.Name() {
			cmd = &Commands[i]
			break
		}
	}
	if cmd == nil {
		return
	}
	line := "ask " + cmd.Name
	if cmd.Args != "" {
		line += " " + cmd.Args
	}
	line += " [flags]"
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  "+line)
	fmt.Fprintln(w)
	fmt.Fprintln(w, cmd.Summary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	writeFlag := func(f FlagSpec) {
		name := "--" + f.Name
		if f.Arg != "" {
			name += " " + f.Arg
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, f.Summary)
	}
	for _, f := range cmd.Flags {
		writeFlag(f)
	}
	for _, f := range crossCuttingFlags {
		writeFlag(f)
	}
	tw.Flush()
}
