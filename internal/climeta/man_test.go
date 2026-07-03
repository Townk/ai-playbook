// man_test.go — TDD tests for the roff man-page renderer (see
// .superpowers/sdd/task-3-brief.md).
package climeta

import (
	"strings"
	"testing"
)

// knownRoffRequests are the request/macro names this package ever emits at
// the start of a line. Any other leading-dot (or leading-apostrophe) line in
// rendered output is un-escaped body text that would be misparsed by roff.
var knownRoffRequests = []string{
	".TH", ".SH", ".PP", ".TP", ".nf", ".fi",
}

func isKnownRoffRequestLine(line string) bool {
	for _, req := range knownRoffRequests {
		if line == req || strings.HasPrefix(line, req+" ") {
			return true
		}
	}
	return false
}

// assertNoUnescapedLeadingDot fails t if out contains a line that begins
// with a dot or an apostrophe and is not one of knownRoffRequests — i.e.
// unescaped content that roff would misinterpret as a request/macro
// invocation.
func assertNoUnescapedLeadingDot(t *testing.T, out string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if (strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'")) && !isKnownRoffRequestLine(line) {
			t.Errorf("unescaped leading-dot/quote content line: %q", line)
		}
	}
}

func TestMan_RunCommand(t *testing.T) {
	run, ok := Lookup("run")
	if !ok {
		t.Fatal(`Lookup("run") ok=false, want true`)
	}

	out := Man(run)

	if !strings.HasPrefix(out, ".TH") {
		t.Errorf("Man(run) does not start with .TH:\n%s", out)
	}
	for _, want := range []string{".SH NAME", ".SH SYNOPSIS", ".SH OPTIONS", `.SH "SEE ALSO"`} {
		if !strings.Contains(out, want) {
			t.Errorf("Man(run) missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `\fB\-\-with\-env\fR`) {
		t.Errorf(`Man(run) missing escaped \fB\-\-with\-env\fR:`+"\n%s", out)
	}

	assertNoUnescapedLeadingDot(t, out)
}

// TestMan_Deterministic asserts Man(c) is byte-stable across calls, so
// regenerating docs/man/*.1 never produces a diff by itself.
func TestMan_Deterministic(t *testing.T) {
	run, ok := Lookup("run")
	if !ok {
		t.Fatal(`Lookup("run") ok=false, want true`)
	}

	first := Man(run)
	second := Man(run)
	if first != second {
		t.Errorf("Man(run) is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestManOverview asserts ManOverview starts with .TH AI-PLAYBOOK, lists
// every DocumentedCommands entry, and has no unescaped leading-dot content.
func TestManOverview(t *testing.T) {
	out := ManOverview()

	if !strings.HasPrefix(out, ".TH AI-PLAYBOOK 1") {
		t.Errorf("ManOverview() does not start with .TH AI-PLAYBOOK 1:\n%s", out)
	}
	for _, want := range []string{".SH NAME", ".SH SYNOPSIS", ".SH COMMANDS", `.SH "SEE ALSO"`} {
		if !strings.Contains(out, want) {
			t.Errorf("ManOverview() missing %q:\n%s", want, out)
		}
	}
	for _, cmd := range DocumentedCommands() {
		if !strings.Contains(out, cmd.Name) {
			t.Errorf("ManOverview() does not mention documented command %q", cmd.Name)
		}
	}

	assertNoUnescapedLeadingDot(t, out)

	if out != ManOverview() {
		t.Error("ManOverview() is not deterministic")
	}
}

// TestDocumentedCommands_ExcludesInternalExceptFinalize asserts
// DocumentedCommands includes every non-Internal command plus "finalize"
// (documented despite being marked Internal), and excludes every other
// Internal command.
func TestDocumentedCommands_ExcludesInternalExceptFinalize(t *testing.T) {
	docs := DocumentedCommands()

	seen := make(map[string]bool, len(docs))
	for _, cmd := range docs {
		seen[cmd.Name] = true
	}

	if !seen["finalize"] {
		t.Error(`DocumentedCommands() does not include "finalize"`)
	}
	if !seen["run"] {
		t.Error(`DocumentedCommands() does not include "run"`)
	}

	for _, cmd := range Commands {
		if cmd.Internal && cmd.Name != "finalize" {
			if seen[cmd.Name] {
				t.Errorf("DocumentedCommands() unexpectedly includes internal command %q", cmd.Name)
			}
		}
	}
}

// TestMan_AllDocumentedCommandsRenderCleanly asserts every DocumentedCommands
// entry renders without an unescaped leading-dot content line.
func TestMan_AllDocumentedCommandsRenderCleanly(t *testing.T) {
	for _, cmd := range DocumentedCommands() {
		out := Man(cmd)
		if !strings.HasPrefix(out, ".TH") {
			t.Errorf("Man(%s) does not start with .TH", cmd.Name)
		}
		assertNoUnescapedLeadingDot(t, out)
	}
}
