// man.go renders climeta's command registry as roff man(7) pages: a single
// overview page (ai-playbook.1) plus one page per user-invokable command
// (ai-playbook-<cmd>.1). Output is deterministic — no timestamps, no map
// iteration — so regenerating the docs never produces spurious diffs (see
// cmd/docgen and the "docs" Makefile target).
package climeta

import (
	"fmt"
	"strings"
)

// manDate is deliberately fixed (not the current date) so Man/ManOverview
// output is byte-stable across regenerations; the committed docs/man/*.1
// files must never churn just because time passed.
const manDate = ""

// EscapeInline escapes roff special characters for use within a single
// line of running text: backslash becomes the roff escape \e, and every
// hyphen-minus becomes \- (the man-pages(7)-recommended literal-hyphen
// escape, distinguishing it from a hyphenation point). It must run before
// any leading-dot/quote check (EscapeLine) since it never introduces a new
// leading dot or apostrophe.
func EscapeInline(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	return s
}

// EscapeLine escapes s (via EscapeInline) and, if the result begins with a
// dot or an apostrophe — which roff would otherwise parse as a
// request/macro invocation — prefixes it with the zero-width \& escape so
// it renders as literal text.
func EscapeLine(s string) string {
	s = EscapeInline(s)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// DocumentedCommands returns, in registry order, every command that gets
// its own man page: all non-Internal commands, plus "finalize" (which is
// marked Internal for Overview/Help grouping purposes but is a documented,
// user-invokable command for man/completion purposes).
func DocumentedCommands() []Command {
	var out []Command
	for _, cmd := range Commands {
		if !cmd.Internal || cmd.Name == "finalize" {
			out = append(out, cmd)
		}
	}
	return out
}

// renderDescription renders c's Long text as a .SH DESCRIPTION section: a
// blank line in Long starts a new .PP paragraph; every other line is
// escaped and emitted as-is (roff fills running text automatically). Long
// == "" renders nothing (Man/ManOverview simply omit the section).
func renderDescription(long string) string {
	if long == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(".SH DESCRIPTION\n")
	for i, para := range strings.Split(long, "\n\n") {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		for _, line := range strings.Split(para, "\n") {
			b.WriteString(EscapeLine(line))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderOptions renders c's Flags as a .SH OPTIONS section: each flag is a
// .TP item whose tag is \fB--name\fR (a bare boolean switch) or
// \fB--name\fR \fI<placeholder>\fR (a value flag), followed by its verbatim
// (escaped) description.
func renderOptions(flags []Flag) string {
	var b strings.Builder
	b.WriteString(".SH OPTIONS\n")
	if len(flags) == 0 {
		b.WriteString("This command takes no flags.\n")
		return b.String()
	}
	for _, f := range flags {
		b.WriteString(".TP\n")
		tag := `\fB\-\-` + EscapeInline(f.Name) + `\fR`
		if !f.Bool && f.Placeholder != "" {
			tag += ` \fI` + EscapeInline(f.Placeholder) + `\fR`
		}
		b.WriteString(tag)
		b.WriteString("\n")
		b.WriteString(EscapeLine(f.Desc))
		b.WriteString("\n")
	}
	return b.String()
}

// renderExamples renders c's Examples as a .SH EXAMPLES section: each
// example is wrapped in .nf/.fi (no-fill) so shell quoting/spacing survives
// verbatim.
func renderExamples(examples []string) string {
	if len(examples) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(".SH EXAMPLES\n")
	for i, ex := range examples {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		b.WriteString(".nf\n")
		b.WriteString(EscapeLine(ex))
		b.WriteString("\n.fi\n")
	}
	return b.String()
}

// Man renders c as a standalone roff man(7) page (ai-playbook-<name>(1)).
// Output is deterministic: calling Man(c) twice returns byte-identical
// text, so the committed docs/man/*.1 files never churn on regeneration.
func Man(c Command) string {
	var b strings.Builder

	title := "AI-PLAYBOOK-" + strings.ToUpper(c.Name)
	fmt.Fprintf(&b, ".TH %s 1 \"%s\" \"ai-playbook\" \"User Commands\"\n", title, manDate)

	b.WriteString(".SH NAME\n")
	b.WriteString(EscapeInline("ai-playbook-"+c.Name+" - "+c.Summary) + "\n")

	b.WriteString(".SH SYNOPSIS\n")
	fmt.Fprintf(&b, "\\fBai\\-playbook\\fR %s\n", EscapeInline(c.Synopsis))

	b.WriteString(renderDescription(c.Long))
	b.WriteString(renderOptions(c.Flags))
	b.WriteString(renderExamples(c.Examples))

	b.WriteString(".SH \"SEE ALSO\"\n")
	b.WriteString(`\fBai\-playbook\fR(1)` + "\n")

	return b.String()
}

// ManOverview renders the top-level ai-playbook(1) man page: a NAME/
// SYNOPSIS/DESCRIPTION, a COMMANDS section listing every DocumentedCommands
// entry (name + Summary), and a SEE ALSO section cross-referencing every
// per-command page. Output is deterministic for the same reason as Man.
func ManOverview() string {
	var b strings.Builder

	fmt.Fprintf(&b, ".TH AI-PLAYBOOK 1 \"%s\" \"ai-playbook\" \"User Commands\"\n", manDate)

	b.WriteString(".SH NAME\n")
	b.WriteString(EscapeInline("ai-playbook - capture, author, and run terminal playbooks") + "\n")

	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(`\fBai\-playbook\fR \fICOMMAND\fR [\fIARGS\fR ...]` + "\n")

	b.WriteString(renderDescription(
		"ai-playbook captures, authors, and runs terminal playbooks. Each\n" +
			"command below has its own manual page; run 'ai-playbook <command>\n" +
			"--help' or see ai-playbook-<command>(1) for full details.",
	))

	docs := DocumentedCommands()

	b.WriteString(".SH COMMANDS\n")
	for _, cmd := range docs {
		b.WriteString(".TP\n")
		b.WriteString(`\fB` + EscapeInline(cmd.Name) + `\fR` + "\n")
		b.WriteString(EscapeLine(cmd.Summary) + "\n")
	}

	b.WriteString(".SH \"SEE ALSO\"\n")
	refs := make([]string, len(docs))
	for i, cmd := range docs {
		refs[i] = `\fBai\-playbook\-` + EscapeInline(cmd.Name) + `\fR(1)`
	}
	b.WriteString(strings.Join(refs, ", ") + "\n")

	return b.String()
}
