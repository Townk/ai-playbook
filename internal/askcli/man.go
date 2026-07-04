// man.go renders askcli's Commands/ThemeFlags/CrossCuttingFlags metadata as a
// single roff man(7) page (docs/man/ask.1) — unlike climeta's one-page-per-
// subcommand model, ask gets ONE page covering every widget, since the whole
// surface is small enough to read in one sitting. It reuses climeta's
// exported troff-escaping primitives (EscapeInline/EscapeLine) instead of
// duplicating them — see internal/climeta/man.go. Output is deterministic —
// no timestamps, no map iteration — so regenerating the docs never produces a
// spurious diff (see cmd/docgen and the "docs" Makefile target).
package askcli

import (
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// manDate is deliberately fixed (not the current date) so Man's output is
// byte-stable across regenerations; the committed docs/man/ask.1 file must
// never churn just because time passed. Mirrors climeta's manDate.
const manDate = ""

// renderFlagTP renders one FlagSpec as a .TP item: a \fB--name\fR (or
// \fB--name\fR \fI<arg>\fR for a value flag) tag, followed by its Summary and
// — when present — the ASK_<FLAG> environment fallback name.
func renderFlagTP(b *strings.Builder, f FlagSpec) {
	b.WriteString(".TP\n")
	tag := `\fB\-\-` + climeta.EscapeInline(f.Name) + `\fR`
	if f.Arg != "" {
		tag += ` \fI` + climeta.EscapeInline(f.Arg) + `\fR`
	}
	b.WriteString(tag)
	b.WriteString("\n")
	desc := f.Summary
	if f.Env != "" {
		desc += " (environment fallback: " + f.Env + ")"
	}
	b.WriteString(climeta.EscapeLine(desc))
	b.WriteString("\n")
}

// renderCommandSection renders one subcommand as a .SS subsection: its
// synopsis line, Summary, then a .TP item per flag (its own Flags first, the
// cross-cutting flags are documented once globally — see FLAGS below).
func renderCommandSection(b *strings.Builder, c Command) {
	fmt.Fprintf(b, ".SS \"ask %s\"\n", climeta.EscapeInline(c.Name))
	synopsis := "ask " + c.Name
	if c.Args != "" {
		synopsis += " " + c.Args
	}
	synopsis += " [flags]"
	b.WriteString(".PP\n")
	b.WriteString(`\fB` + climeta.EscapeInline(synopsis) + `\fR` + "\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(c.Summary) + "\n")
	if len(c.Flags) == 0 {
		return
	}
	for _, f := range c.Flags {
		renderFlagTP(b, f)
	}
}

// Man renders the standalone ask(1) man page. Output is deterministic:
// calling Man() twice returns byte-identical text, so the committed
// docs/man/ask.1 file never churns on regeneration.
func Man() string {
	var b strings.Builder

	fmt.Fprintf(&b, ".TH ASK 1 \"%s\" \"ask\" \"User Commands\"\n", manDate)

	b.WriteString(".SH NAME\n")
	b.WriteString(climeta.EscapeInline("ask - themed terminal dialogs for scripts (confirm/line/text/choose/form)") + "\n")

	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(`\fBask\fR \fICOMMAND\fR [\fIARGS\fR ...] [\fIFLAGS\fR ...]` + "\n")

	b.WriteString(".SH DESCRIPTION\n")
	b.WriteString(climeta.EscapeLine(
		"ask exposes ai-playbook's themed dialog widgets as a standalone tool for",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"shell scripts. Its contract is pure I/O: the submitted value (if any) goes",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"to stdout and nothing else, diagnostics go to stderr, and the exit code",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"reports the outcome — see EXIT CODES below.",
	) + "\n")

	b.WriteString(".SH COMMANDS\n")
	for _, c := range Commands {
		renderCommandSection(&b, c)
	}

	b.WriteString(".SH \"FLAGS (every subcommand)\"\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine("Every subcommand additionally accepts:") + "\n")
	for _, f := range CrossCuttingFlags() {
		renderFlagTP(&b, f)
	}

	b.WriteString(".SH THEMING\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(
		"Every --theme-* flag has an ASK_<FLAG> environment fallback, so a script can",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"export the palette once instead of passing it on every invocation.",
	) + "\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine("Precedence: flag > ASK_<FLAG> environment variable > built-in default.") + "\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(
		"A SET-but-empty ASK_<FLAG> variable deliberately overrides the default with",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"the empty string: presence wins, not non-emptiness. Exporting",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"'ASK_THEME_ACCENT=\"\"' intentionally blanks that color; leaving the variable",
	) + "\n")
	b.WriteString(climeta.EscapeLine("unset (not exported at all) keeps the built-in default.") + "\n")
	for _, f := range ThemeFlags() {
		renderFlagTP(&b, f)
	}

	b.WriteString(".SH \"FLAG PARSING\"\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(
		"Flags may appear before OR after a subcommand's positional argument(s) —",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"e.g. both 'ask confirm --danger \"Delete?\"' and 'ask confirm \"Delete?\"",
	) + "\n")
	b.WriteString(climeta.EscapeLine("--danger' are equivalent.") + "\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(
		"A bare '--' terminates flag parsing: everything after the FIRST '--' is",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"taken verbatim as positional arguments and never re-parsed as a flag.",
	) + "\n")
	b.WriteString(".PP\n")
	b.WriteString(climeta.EscapeLine(
		"A flag that needs a value which itself starts with '-' (or is exactly",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"'--') MUST use the '--flag=value' form, e.g. '--negative=--weird'. The",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"space form ('--flag -value') does NOT work — the dashed word is parsed as",
	) + "\n")
	b.WriteString(climeta.EscapeLine("its own flag (or positional) and the command errors loudly.") + "\n")

	b.WriteString(".SH \"EXIT CODES\"\n")
	b.WriteString(".TP\n")
	b.WriteString(`\fB0\fR` + "\n")
	b.WriteString(climeta.EscapeLine("Submit / affirmative answer.") + "\n")
	b.WriteString(".TP\n")
	b.WriteString(`\fB1\fR` + "\n")
	b.WriteString(climeta.EscapeLine(
		"confirm's negative answer, OR any other non-usage runtime widget failure —",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"the two are indistinguishable by exit code alone (a runtime failure reuses",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"exit 1 rather than inventing a new code). This is the safe",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"direction for shell idioms like 'if ask confirm \"...\"; then': both a",
	) + "\n")
	b.WriteString(climeta.EscapeLine(
		"genuine \"No\" and an unexpected failure take the same false branch. A",
	) + "\n")
	b.WriteString(climeta.EscapeLine("script that must tell them apart should check stderr.") + "\n")
	b.WriteString(".TP\n")
	b.WriteString(`\fB130\fR` + "\n")
	b.WriteString(climeta.EscapeLine("Cancelled (Esc / Ctrl-C).") + "\n")
	b.WriteString(".TP\n")
	b.WriteString(`\fB2\fR` + "\n")
	b.WriteString(climeta.EscapeLine(
		"Usage error (unknown subcommand/flag, malformed --spec/stdin JSON form",
	) + "\n")
	b.WriteString(climeta.EscapeLine("spec), or no controlling terminal is reachable.") + "\n")

	b.WriteString(".SH EXAMPLES\n")
	examples := []string{
		`ask confirm "Delete the branch?" --danger`,
		`name=$(ask line "Your name" --value "$USER")`,
		`notes=$(ask text "Release notes")`,
		`env=$(ask choose "Target environment" staging production)`,
		`ask form --spec ./fields.json --json`,
	}
	for i, ex := range examples {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		b.WriteString(".nf\n")
		b.WriteString(climeta.EscapeLine(ex))
		b.WriteString("\n.fi\n")
	}

	b.WriteString(".SH \"SEE ALSO\"\n")
	b.WriteString(`\fBai\-playbook\fR(1)` + "\n")

	return b.String()
}
