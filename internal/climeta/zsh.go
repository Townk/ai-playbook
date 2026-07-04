// zsh.go renders climeta's command registry as a single zsh completion
// script (#compdef ai-playbook apb): a top-level state machine that
// name/summary completes every registered command (user-facing and
// internal, so every subcommand is at least name-completable), then a
// per-command _arguments block for each non-Internal command's flags —
// internal/plumbing commands are deliberately left without flag/arg
// completion to keep the script lean (see the Commands doc comment for which
// commands are Internal). Commands whose positional resolves a playbook
// store slug (SlugArg) — and run's --playbook flag — wire into a dynamic
// _ai-playbook_slugs helper that shells out to `$service list --format
// fuzzy-data-source`, where $service is zsh's name for the command actually
// being completed (ai-playbook or apb — see zshSlugsHelper), so slug
// completion works even when only the apb binary is on PATH. Output is
// deterministic — no timestamps, no map iteration — so regenerating the
// completion never produces a spurious diff (see cmd/docgen and the "docs"
// Makefile target).
package climeta

import (
	"fmt"
	"strings"
)

// zshSlugsHelper is the dynamic store-slug completer: it shells out to
// `$service list --format fuzzy-data-source` — $service is the zsh
// completion-system variable holding the name of the command currently being
// completed (set by #compdef to either "ai-playbook" or "apb"), so this
// helper always invokes whichever binary the user actually has on PATH
// rather than a hardcoded name. Its output lines are \x1f-delimited
// "<description>\x1f<slug>\x1f..." records; the helper reshapes field 2
// (slug) and field 1 (description) into "<slug>:<description>" entries for
// _describe. Kept as a literal constant (not built from Go string
// concatenation) so the exact invocation this package's tests assert on is
// unambiguous.
const zshSlugsHelper = `_ai-playbook_slugs() {
  local -a slugs
  slugs=(${(f)"$(${service} list --format fuzzy-data-source 2>/dev/null | awk -F$'\x1f' '{print $2":"$1}')"})
  _describe -t playbooks 'playbook' slugs
}
`

// ZshEscapeBracket escapes s for use inside a zsh _arguments bracket spec
// ('--name[DESC]...'): backslash is doubled first (so the escapes below
// aren't themselves re-escaped), then '[', ']' and ':' are backslash-escaped
// so they can't prematurely close the bracket or be misread as an
// option/message/action field separator.
func ZshEscapeBracket(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	return s
}

// ZshEscapeDescribe escapes s for use as the description half of a
// _describe "value:description" array entry: backslash is doubled first,
// then ':' is backslash-escaped so an embedded colon isn't parsed as an
// additional field separator.
func ZshEscapeDescribe(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	return s
}

// ZshSingleQuote wraps s in single quotes for embedding in the generated
// script, escaping any embedded single quote using the standard shell
// technique: close the quoted string, emit a backslash-escaped literal
// quote, then reopen the quoted string. It must run last — after any
// bracket/describe-field escaping — since those never touch single quotes
// themselves.
func ZshSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ZshDescribeEntry renders one _describe array entry for name/desc.
func ZshDescribeEntry(name, desc string) string {
	return ZshSingleQuote(name + ":" + ZshEscapeDescribe(desc))
}

// zshFlagAction returns the completion action for a value flag (the part
// after the second ':' in '--name[desc]:msg:action'), or "" when the flag's
// values have no specific completer. format flags complete the fixed set of
// output formats; run's --playbook value (like every SlugArg command's
// positional) completes via the dynamic slug helper; file-path flags
// complete via _files.
func zshFlagAction(cmd Command, f Flag) string {
	switch f.Name {
	case "format":
		return "(human fuzzy-data-source json)"
	case "playbook":
		if cmd.SlugArg {
			return "_ai-playbook_slugs"
		}
	case "file":
		return "_files"
	}
	return ""
}

// zshFlagMessage returns the human-readable message zsh shows while
// prompting for a value flag's argument (the part between the two ':' in
// '--name[desc]:msg:action'), derived from the flag's Placeholder (its
// surrounding <> stripped) or "value" when there is none.
func zshFlagMessage(f Flag) string {
	msg := strings.Trim(f.Placeholder, "<>")
	if msg == "" {
		msg = "value"
	}
	return msg
}

// zshFlagSpec renders one _arguments spec for flag f of command cmd: a bare
// '--name[desc]' for a boolean switch, or '--name[desc]:msg:action' for a
// value flag.
func zshFlagSpec(cmd Command, f Flag) string {
	spec := "--" + f.Name + "[" + ZshEscapeBracket(f.Desc) + "]"
	if !f.Bool {
		spec += ":" + zshFlagMessage(f) + ":" + zshFlagAction(cmd, f)
	}
	return ZshSingleQuote(spec)
}

// zshCommandFunc renders cmd's per-command completion function
// (_ai-playbook_cmd_<name>): a positional slug completer first (when
// cmd.SlugArg), then one spec per flag.
func zshCommandFunc(cmd Command) string {
	var specs []string
	if cmd.SlugArg {
		specs = append(specs, ZshSingleQuote("1:playbook:_ai-playbook_slugs"))
	}
	for _, f := range cmd.Flags {
		specs = append(specs, zshFlagSpec(cmd, f))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "_ai-playbook_cmd_%s() {\n", cmd.Name)
	if len(specs) == 0 {
		b.WriteString("  _arguments\n")
	} else {
		b.WriteString("  _arguments \\\n")
		for i, spec := range specs {
			end := " \\\n"
			if i == len(specs)-1 {
				end = "\n"
			}
			b.WriteString("    " + spec + end)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// Zsh renders the full zsh completion script for ai-playbook. Output is
// deterministic: calling Zsh() twice returns byte-identical text, so the
// committed completions/_ai-playbook file never churns on regeneration.
func Zsh() string {
	var b strings.Builder

	b.WriteString("#compdef ai-playbook apb\n\n")
	b.WriteString(zshSlugsHelper)
	b.WriteString("\n")

	b.WriteString("_ai-playbook_commands() {\n")
	b.WriteString("  local -a commands\n")
	b.WriteString("  commands=(\n")
	for _, cmd := range Commands {
		b.WriteString("    " + ZshDescribeEntry(cmd.Name, cmd.Summary) + "\n")
		for _, alias := range cmd.Aliases {
			b.WriteString("    " + ZshDescribeEntry(alias, cmd.Summary) + "\n")
		}
	}
	b.WriteString("  )\n")
	b.WriteString("  _describe -t commands 'command' commands\n")
	b.WriteString("}\n\n")

	for _, cmd := range Commands {
		if cmd.Internal {
			continue
		}
		b.WriteString(zshCommandFunc(cmd))
		b.WriteString("\n")
	}

	b.WriteString("_ai-playbook() {\n")
	b.WriteString("  local curcontext=\"$curcontext\" state line\n")
	b.WriteString("  typeset -A opt_args\n\n")
	b.WriteString("  _arguments -C \\\n")
	b.WriteString("    '1: :->cmd' \\\n")
	b.WriteString("    '*::arg:->args'\n\n")
	b.WriteString("  case $state in\n")
	b.WriteString("    cmd)\n")
	b.WriteString("      _ai-playbook_commands\n")
	b.WriteString("      ;;\n")
	b.WriteString("    args)\n")
	b.WriteString("      case $words[1] in\n")
	for _, cmd := range Commands {
		if cmd.Internal {
			continue
		}
		names := append([]string{cmd.Name}, cmd.Aliases...)
		fmt.Fprintf(&b, "        %s)\n", strings.Join(names, "|"))
		fmt.Fprintf(&b, "          _ai-playbook_cmd_%s\n", cmd.Name)
		b.WriteString("          ;;\n")
	}
	b.WriteString("      esac\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n\n")
	b.WriteString("_ai-playbook \"$@\"\n")

	return b.String()
}
