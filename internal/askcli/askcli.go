// Package askcli implements the standalone `ask` binary: the themed dialog
// widgets (confirm/line/text/choose/form) from pkg/dialog exposed as a
// pure-I/O tool for shell scripts. It owns the subcommand dispatch, per-widget
// flag sets, ASK_* env fallbacks, JSON form-spec parsing, and the exit-code
// contract; the widgets remain the single implementation shared with
// `ai-playbook input`, which is untouched. See docs/specifications/ask-binary.md
// and ADR-0009 (interaction-toolkit surface, migration step 3).
//
// Exit codes (all subcommands): 0 submit/affirmative, 1 confirm-negative, 130
// cancel (ESC/Ctrl-C), 2 usage/spec error. Values go to stdout; diagnostics to
// stderr.
package askcli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/Townk/ai-playbook/pkg/dialog"
)

// Version is the binary's version string. It defaults to "dev" for local
// builds; GoReleaser injects the real tag via -ldflags "-X
// github.com/Townk/ai-playbook/internal/askcli.Version=...".
var Version = "dev"

// Exit codes — the public contract (docs/specifications/ask-binary.md).
const (
	exitOK       = 0
	exitNegative = 1
	exitCancel   = 130
	exitUsage    = 2
)

// resolveVersion picks the version to report. The GoReleaser ldflag wins when
// present; otherwise fall back to the module version Go embeds in build info, so
// proxy-installed binaries report their real tag instead of "dev". Duplicated
// from internal/cli deliberately (importing it would drag the whole ai-playbook
// CLI into the ask binary).
func resolveVersion(ldflag, buildVer string, buildOK bool) string {
	if ldflag != "dev" {
		return ldflag
	}
	if buildOK && buildVer != "" && buildVer != "(devel)" {
		return buildVer
	}
	return ldflag
}

func version() string {
	buildVer, ok := "", false
	if info, iok := debug.ReadBuildInfo(); iok {
		buildVer, ok = info.Main.Version, true
	}
	return resolveVersion(Version, buildVer, ok)
}

// Run is the entrypoint: dispatch os.Args to a subcommand and return the process
// exit code (the caller owns os.Exit). args[0] is the program name.
func Run(args []string) int {
	// Match ai-playbook input's rune-width accounting so measure/render widths
	// agree with the terminal (and with `ai-playbook input --measure`).
	dialog.NarrowRuneWidth()

	if len(args) < 2 {
		usage(os.Stderr)
		return exitUsage
	}
	switch args[1] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return exitOK
	case "-v", "--version":
		fmt.Println(version())
		return exitOK
	case "confirm":
		return runConfirmCmd(args[2:])
	case "line":
		return runLineCmd(args[2:])
	case "text":
		return runTextCmd(args[2:])
	case "choose":
		return runChooseCmd(args[2:])
	case "form":
		return runFormCmd(args[2:])
	default:
		fmt.Fprintf(os.Stderr, "ask: unknown subcommand %q\n", args[1])
		usage(os.Stderr)
		return exitUsage
	}
}

// --- flag parsing helpers ----------------------------------------------------

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we render our own usage/error text
	return fs
}

// parse parses args into fs, permuting flags and positionals so a leading
// positional (the prompt) does not stop flag parsing — the Go flag package
// halts at the first non-flag argument, but `ask confirm "Q?" --danger` puts the
// prompt first. A bare `--` terminates flag parsing: everything after the FIRST
// `--` is appended verbatim as positionals and never re-parsed. The split must
// happen BEFORE the permutation loop — a `--` consumed by one fs.Parse round is
// gone when the tail is re-parsed, so a second dashed positional would be
// re-read as a flag (`ask choose "Pick" -- -foo --bar` broke on --bar). It
// returns the collected positionals plus (code, true) when Run should return
// immediately (help → 0, unknown flag → 2) or (nil, 0, false) to continue.
func parse(fs *flag.FlagSet, args []string) ([]string, int, bool) {
	// Split on the first bare `--`: only the head is parsed/permuted.
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		err := fs.Parse(args)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				commandUsage(os.Stdout, fs)
				return nil, exitOK, true
			}
			fmt.Fprintf(os.Stderr, "ask %s: %v\n", fs.Name(), err)
			commandUsage(os.Stderr, fs)
			return nil, exitUsage, true
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	return append(pos, tail...), 0, false
}

// firstArg returns pos[0] or "".
func firstArg(pos []string) string {
	if len(pos) > 0 {
		return pos[0]
	}
	return ""
}

// --- the widget seam ---------------------------------------------------------

// widgetInvocation is what a subcommand hands to runWidget: a kind tag plus the
// populated options struct for that kind.
type widgetInvocation struct {
	kind    string
	confirm dialog.ConfirmOptions
	line    dialog.LineOptions
	text    dialog.TextOptions
	choose  dialog.ChooseOptions
	form    dialog.FormOptions
}

// widgetOutcome is the normalized result the subcommand maps to exit code +
// stdout.
type widgetOutcome struct {
	cancelled bool
	confirm   string // "yes" | "no" (confirm only)
	value     string // line/text/choose submitted value
	formPairs []dialog.FormPair
}

// runWidget is the seam between askcli and the interactive widgets. The default
// drives the real pkg/dialog runners; tests replace it to inject outcomes
// and capture the options mapping without a TTY.
var runWidget = defaultRunWidget

func defaultRunWidget(inv widgetInvocation) (widgetOutcome, error) {
	switch inv.kind {
	case "confirm":
		r, err := dialog.RunConfirm(inv.confirm)
		if err != nil {
			return widgetOutcome{}, err
		}
		return widgetOutcome{cancelled: r.Cancelled, confirm: r.Value}, nil
	case "line":
		r, err := dialog.RunLine(inv.line)
		if err != nil {
			return widgetOutcome{}, err
		}
		return widgetOutcome{cancelled: r.Cancelled, value: r.Value}, nil
	case "text":
		r, err := dialog.RunText(inv.text)
		if err != nil {
			return widgetOutcome{}, err
		}
		return widgetOutcome{cancelled: r.Cancelled, value: r.Value}, nil
	case "choose":
		r, err := dialog.RunChoose(inv.choose)
		if err != nil {
			return widgetOutcome{}, err
		}
		return widgetOutcome{cancelled: r.Cancelled, value: r.Value}, nil
	case "form":
		r, err := dialog.RunForm(inv.form)
		if err != nil {
			return widgetOutcome{}, err
		}
		return widgetOutcome{cancelled: r.Cancelled, formPairs: r.Pairs}, nil
	default:
		return widgetOutcome{}, fmt.Errorf("unknown widget kind %q", inv.kind)
	}
}

// --- TTY preflight -----------------------------------------------------------

// hasTTY reports whether an interactive terminal is reachable. The widgets drive
// /dev/tty when stdin is redirected (bubbletea opens it automatically), so a
// script piping a form spec on stdin still works; only a fully detached process
// (no controlling terminal at all) fails. Replaced in tests.
var hasTTY = defaultHasTTY

func defaultHasTTY() bool {
	// A terminal on stdin is a character device; that covers the interactive
	// case. When stdin is redirected (pipe/file) the widgets fall back to
	// /dev/tty, so probe that too.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return true
	}
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		_ = f.Close()
		return true
	}
	return false
}

// noTTY reports the no-terminal condition (exit 2 per the spec).
func noTTY(sub string) int {
	fmt.Fprintf(os.Stderr, "ask %s: a terminal is required (no TTY on stdin or /dev/tty)\n", sub)
	return exitUsage
}

// widgetErr maps a widget run error to an exit code. A TTY-open failure (stdin
// redirected AND /dev/tty unavailable) surfaces here despite the preflight in a
// race; report it as the no-TTY usage error. Any other error → 1.
func widgetErr(sub string, err error) int {
	if strings.Contains(err.Error(), "TTY") {
		return noTTY(sub)
	}
	fmt.Fprintf(os.Stderr, "ask %s: %v\n", sub, err)
	return exitNegative
}

// shellQuote single-quotes s for safe reuse in a POSIX shell, escaping embedded
// single quotes as '\”. Used for form key=value output.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
