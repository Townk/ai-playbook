// main_test.go — TDD tests for the top-level --help/help dispatch (see
// .superpowers/sdd/task-2-brief.md). main() calls os.Exit, so the help
// decision lives in the pure helpFor function tested directly here; main
// itself stays a thin wrapper (print + os.Exit) that is not unit-tested.
package main

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// TestHelpFor_TopLevelNoCommand asserts `-h`/`--help`/`help` with no trailing
// command name resolves to the top-level Overview(), handled.
func TestHelpFor_TopLevelNoCommand(t *testing.T) {
	overview := climeta.Overview()
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		text, handled := helpFor(args)
		if !handled {
			t.Errorf("helpFor(%v) handled=false, want true", args)
		}
		if text != overview {
			t.Errorf("helpFor(%v) = %q, want Overview()", args, text)
		}
	}
}

// TestHelpFor_HelpCommand asserts `help <cmd>` (and `--help <cmd>`) resolves
// to that command's climeta.Help() text, containing its documented flags.
func TestHelpFor_HelpCommand(t *testing.T) {
	for _, args := range [][]string{{"help", "run"}, {"--help", "run"}} {
		text, handled := helpFor(args)
		if !handled {
			t.Errorf("helpFor(%v) handled=false, want true", args)
		}
		if !strings.Contains(text, "--with-env") {
			t.Errorf("helpFor(%v) missing --with-env:\n%s", args, text)
		}
	}
}

// TestHelpFor_HelpUnknownCommand asserts `help <unknown>` is still handled
// (so it never falls through to dispatch), even though there is no specific
// help text for it.
func TestHelpFor_HelpUnknownCommand(t *testing.T) {
	_, handled := helpFor([]string{"help", "nope-not-a-command"})
	if !handled {
		t.Error("helpFor([help nope-not-a-command]) handled=false, want true")
	}
}

// TestHelpFor_SubcommandHelpFlag asserts a bare -h/--help token anywhere in a
// subcommand's own args resolves to that subcommand's help — never falling
// through to the subcommand's own flag.FlagSet.
func TestHelpFor_SubcommandHelpFlag(t *testing.T) {
	cases := [][]string{
		{"run", "--help"},
		{"run", "-h"},
		{"run", "deploy-staging", "--help"},
		{"env", "-h"},
	}
	for _, args := range cases {
		text, handled := helpFor(args)
		if !handled {
			t.Errorf("helpFor(%v) handled=false, want true", args)
		}
		want, ok := climeta.Help(args[0])
		if !ok {
			t.Fatalf("climeta.Help(%q) ok=false", args[0])
		}
		if text != want {
			t.Errorf("helpFor(%v) = %q, want climeta.Help(%q) = %q", args, text, args[0], want)
		}
	}
}

// TestHelpFor_NoHelpRequested asserts ordinary dispatch args (no -h/--help
// token) are NOT handled, so normal dispatch proceeds unimpeded.
func TestHelpFor_NoHelpRequested(t *testing.T) {
	cases := [][]string{
		{"run", "deploy-staging"},
		{"list", "--format", "json"},
		{"version"},
		{},
	}
	for _, args := range cases {
		_, handled := helpFor(args)
		if handled {
			t.Errorf("helpFor(%v) handled=true, want false", args)
		}
	}
}

// TestWantsHelp asserts wantsHelp scans for a bare -h/--help token anywhere
// in args, and does not false-positive on flag VALUES that merely contain
// "-h"/"--help" as a substring.
func TestWantsHelp(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"--file", "foo.md"}, false},
		{[]string{"-h"}, true},
		{[]string{"--help"}, true},
		{[]string{"deploy-staging", "-h"}, true},
		{[]string{"--file", "./scratch.md", "--help"}, true},
		{[]string{"--with-env", "--help-me-please"}, false},
	}
	for _, c := range cases {
		if got := wantsHelp(c.args); got != c.want {
			t.Errorf("wantsHelp(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
