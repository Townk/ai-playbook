package input

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
)

// measureHeight returns the number of lines in view (splitting on "\n").
// ANSI escape sequences contain no newlines, so no stripping is required.
func measureHeight(view string) int {
	return len(strings.Split(view, "\n"))
}

// Main is the entrypoint for the `input` subcommand. It parses os.Args[2:]
// (os.Args[1] is the "input" subcommand selector) and returns a process exit
// code; the caller is responsible for os.Exit.
func Main() int {
	// Narrow (1-cell) accounting for ambiguous-width + nerd-font glyphs so
	// lipgloss widths match the terminal. Must run before any lipgloss call.
	os.Setenv("RUNEWIDTH_EASTASIAN", "0")
	runewidth.DefaultCondition.EastAsianWidth = false

	fs := flag.NewFlagSet("input", flag.ExitOnError)
	var typ, title, prompt, value, placeholder, variant, affirmative, negative, defaultSide, other, spec, icon string
	var outFifo, inFifo, outFile string
	var height, padding, inset, width int
	var danger, warning, multi, measure bool
	fs.StringVar(&typ, "type", "text", "widget type: text|line|confirm|choose|form")
	fs.StringVar(&spec, "spec", "", "form: path to spec file (US/RS encoded); omit to read stdin")
	fs.StringVar(&title, "title", "", "modal title")
	fs.StringVar(&prompt, "prompt", "", "description shown above the input")
	fs.StringVar(&value, "value", "", "initial value (text|line)")
	fs.StringVar(&placeholder, "placeholder", "", "placeholder (line)")
	fs.IntVar(&height, "height", 10, "textarea viewport rows (text)")
	fs.IntVar(&padding, "padding", 1, "frame vertical padding rows")
	fs.IntVar(&inset, "inset", 1, "frame inter-section blank rows")
	fs.StringVar(&variant, "variant", "default", "default|danger|warning")
	fs.BoolVar(&danger, "danger", false, "shorthand for --variant danger")
	fs.BoolVar(&warning, "warning", false, "shorthand for --variant warning")
	fs.StringVar(&affirmative, "affirmative", "Yes", "confirm affirmative label")
	fs.StringVar(&negative, "negative", "No", "confirm negative label")
	fs.StringVar(&defaultSide, "default", "affirmative", "confirm default focus: affirmative|negative")
	fs.BoolVar(&multi, "multi", false, "choose: allow multiple selections")
	fs.StringVar(&other, "other", "", "choose: label for free-text other entry (empty disables)")
	fs.StringVar(&icon, "icon", "", "text/line: prompt-column glyph override (default ❯)")
	fs.StringVar(&outFifo, "out-fifo", "", "path to output FIFO (submit/cancel records written here)")
	fs.StringVar(&inFifo, "in-fifo", "", "path to input FIFO (status/close records read from here)")
	fs.StringVar(&outFile, "out", "", "path to a one-shot output FILE: on submit the value is written here and the process exits 0; on cancel nothing is written and the process exits 130. Lets a FLOATED input (whose stdout is detached) hand its answer back to a polling launcher.")
	fs.BoolVar(&measure, "measure", false, "print the rendered height and exit (no TUI)")
	fs.IntVar(&width, "width", 50, "pane width for measurement/sizing")
	theme := registerThemeFlags(fs)
	fs.Parse(os.Args[2:])

	if danger {
		variant = "danger"
	}
	if warning {
		variant = "warning"
	}
	if variant == "danger" {
		defaultSide = "negative" // never default to a destructive action
	}

	if measure {
		// Build the same model the normal run would build, apply --width, call
		// render(), count the lines, and exit — no TUI, no tea.NewProgram.
		var rendered string
		switch typ {
		case "confirm":
			if prompt == "" {
				prompt = strings.Join(fs.Args(), " ")
			}
			m := newConfirmModel(*theme, variant, title, prompt, affirmative, negative, defaultSide == "negative", padding, inset)
			m.width = width
			rendered = m.render()
		case "line":
			m := newInputModel(*theme, variant, title, prompt, value, placeholder, 1, padding, inset, true, icon)
			m.width = width
			m.resize()
			rendered = m.render()
		case "text":
			m := newInputModel(*theme, variant, title, prompt, value, "", height, padding, inset, false, icon)
			m.width = width
			m.resize()
			rendered = m.render()
		case "choose":
			m := newChooseModel(*theme, variant, title, prompt, fs.Args(), multi, other, padding, inset)
			m.width = width
			rendered = m.render()
		case "form":
			var raw string
			if spec != "" {
				data, err := os.ReadFile(spec)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ai-assist-input: --spec: %v\n", err)
					return 1
				}
				raw = string(data)
			} else {
				data, err := os.ReadFile("/dev/stdin")
				if err != nil {
					fmt.Fprintf(os.Stderr, "ai-assist-input: reading stdin: %v\n", err)
					return 1
				}
				raw = string(data)
			}
			parsed, err := parseFormSpec(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ai-assist-input: %v\n", err)
				return 1
			}
			m := newFormModel(*theme, title, parsed, padding, inset)
			fmt.Println(m.maxHeight(width))
			return 0
		default:
			fmt.Fprintf(os.Stderr, "ai-assist-input: unknown --type %q\n", typ)
			return 2
		}
		fmt.Println(measureHeight(rendered))
		return 0
	}

	switch typ {
	case "confirm":
		if prompt == "" {
			prompt = strings.Join(fs.Args(), " ") // positional fallback for backward compat
		}
		runConfirm(*theme, variant, title, prompt, affirmative, negative, defaultSide == "negative", padding, inset, outFile)
	case "line":
		if outFifo != "" && inFifo != "" {
			runInputWithFifo(*theme, variant, title, prompt, value, placeholder, 1, padding, inset, true, icon, outFifo, inFifo)
		} else {
			runInput(*theme, variant, title, prompt, value, placeholder, 1, padding, inset, true, icon, outFile)
		}
	case "text", "free":
		if outFifo != "" && inFifo != "" {
			runInputWithFifo(*theme, variant, title, prompt, value, "", height, padding, inset, false, icon, outFifo, inFifo)
		} else {
			runInput(*theme, variant, title, prompt, value, "", height, padding, inset, false, icon, outFile)
		}
	case "choose":
		runChoose(*theme, variant, title, prompt, fs.Args(), multi, other, padding, inset, outFile)
	case "form":
		runForm(*theme, title, spec, padding, inset)
	default:
		fmt.Fprintf(os.Stderr, "ai-assist-input: unknown --type %q\n", typ)
		return 2
	}
	return 0
}
