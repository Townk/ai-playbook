package askcli

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// flagTag returns the escaped \fB--name\fR tag Man() emits for flag name, so
// tests don't need to duplicate climeta's hyphen-escaping rule.
func flagTag(name string) string {
	return `\fB\-\-` + climeta.EscapeInline(name) + `\fR`
}

// knownRoffRequests mirrors climeta's man_test.go guard: the request/macro
// names this renderer ever emits at the start of a line.
var knownRoffRequests = []string{
	".TH", ".SH", ".SS", ".PP", ".TP", ".nf", ".fi",
}

func isKnownRoffRequestLine(line string) bool {
	for _, req := range knownRoffRequests {
		if line == req || strings.HasPrefix(line, req+" ") || strings.HasPrefix(line, req+"\"") {
			return true
		}
	}
	return false
}

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

func TestMan_StructuralSections(t *testing.T) {
	out := Man()

	if !strings.HasPrefix(out, ".TH ASK 1") {
		t.Errorf("Man() does not start with .TH ASK 1:\n%s", out)
	}
	for _, want := range []string{
		".SH NAME", ".SH SYNOPSIS", ".SH DESCRIPTION", ".SH COMMANDS",
		".SH \"FLAGS (every subcommand)\"", ".SH THEMING", ".SH \"FLAG PARSING\"",
		".SH \"EXIT CODES\"", ".SH EXAMPLES", ".SH \"SEE ALSO\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Man() missing %q:\n%s", want, out)
		}
	}

	assertNoUnescapedLeadingDot(t, out)
}

// TestMan_DocumentsEverySubcommand asserts every askcli.Commands entry gets
// its own .SS subsection and every one of its flags is rendered.
func TestMan_DocumentsEverySubcommand(t *testing.T) {
	out := Man()
	for _, c := range Commands {
		section := ".SS \"ask " + c.Name + "\""
		if !strings.Contains(out, section) {
			t.Errorf("Man() missing subsection %q", section)
		}
		for _, f := range c.Flags {
			tag := flagTag(f.Name)
			if !strings.Contains(out, tag) {
				t.Errorf("Man() missing flag tag %q for command %q", tag, c.Name)
			}
		}
	}
}

// TestMan_DocumentsCrossCuttingAndThemeFlags asserts the shared flag sets are
// rendered, including the ASK_<FLAG> environment fallback names for theme
// flags.
func TestMan_DocumentsCrossCuttingAndThemeFlags(t *testing.T) {
	out := Man()
	for _, f := range CrossCuttingFlags() {
		tag := flagTag(f.Name)
		if !strings.Contains(out, tag) {
			t.Errorf("Man() missing cross-cutting flag tag %q", tag)
		}
	}
	for _, f := range ThemeFlags() {
		tag := flagTag(f.Name)
		if !strings.Contains(out, tag) {
			t.Errorf("Man() missing theme flag tag %q", tag)
		}
		if !strings.Contains(out, f.Env) {
			t.Errorf("Man() missing env fallback name %q for flag --%s", f.Env, f.Name)
		}
	}
}

// TestMan_DocumentsRequiredA2Handoffs asserts the three A1-handoff items are
// present verbatim in spirit: the exit-1 runtime-failure ambiguity, the
// set-but-empty env override semantics, and the "--flag=value" requirement
// for a dashed flag value.
func TestMan_DocumentsRequiredA2Handoffs(t *testing.T) {
	out := Man()
	// Markers avoid hyphenated words (man.go's EscapeInline turns every "-"
	// into the roff literal-hyphen escape "\-", so a literal "non-usage" or
	// "--flag=value" substring would never match the rendered text).
	for _, want := range []string{
		"runtime widget failure", // exit-1 ambiguity with confirm's "No"
		"if ask confirm",         // the safe-direction shell idiom
		"presence wins, not non", // set-but-empty LookupEnv semantics
		"flag=value",             // the --flag=value requirement
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Man() missing required documentation of %q", want)
		}
	}
}

// TestMan_Deterministic asserts Man() is byte-stable across calls, so
// regenerating docs/man/ask.1 never produces a diff by itself.
func TestMan_Deterministic(t *testing.T) {
	first := Man()
	second := Man()
	if first != second {
		t.Error("Man() is not deterministic")
	}
}
