// zsh.go renders askcli's Commands/ThemeFlags/CrossCuttingFlags metadata as a
// zsh completion script (#compdef ask): a top-level state machine that
// name/summary completes every subcommand, then a per-subcommand _arguments
// block listing that subcommand's own flags plus the cross-cutting and
// --theme-* flags every subcommand accepts. It reuses climeta's exported zsh-
// escaping primitives (ZshEscapeBracket/ZshSingleQuote/ZshDescribeEntry)
// instead of duplicating them — see internal/climeta/zsh.go. Output is
// deterministic — no timestamps, no map iteration — so regenerating the
// completion never produces a spurious diff (see cmd/docgen and the "docs"
// Makefile target).
package askcli

import (
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// zshFlagAction returns the completion action for a value flag (the part
// after the second ':' in '--name[desc]:msg:action'), or "" when the flag's
// values have no specific completer. form's --spec takes a file path.
func zshFlagAction(f FlagSpec) string {
	if f.Name == "spec" {
		return "_files"
	}
	return ""
}

// zshFlagMessage returns the human-readable message zsh shows while
// prompting for a value flag's argument, derived from the flag's Arg (its
// surrounding <> stripped) or "value" when there is none.
func zshFlagMessage(f FlagSpec) string {
	msg := strings.Trim(f.Arg, "<>")
	if msg == "" {
		msg = "value"
	}
	return msg
}

// zshFlagSpec renders one _arguments spec for flag f: a bare '--name[desc]'
// for a boolean switch, or '--name[desc]:msg:action' for a value flag. The
// description carries the ASK_<FLAG> environment fallback, when the flag has
// one, so completion menus surface it.
func zshFlagSpec(f FlagSpec) string {
	desc := f.Summary
	if f.Env != "" {
		desc += " (env " + f.Env + ")"
	}
	spec := "--" + f.Name + "[" + climeta.ZshEscapeBracket(desc) + "]"
	if f.Arg != "" {
		spec += ":" + zshFlagMessage(f) + ":" + zshFlagAction(f)
	}
	return climeta.ZshSingleQuote(spec)
}

// zshCommandFunc renders cmd's per-subcommand completion function
// (_ask_cmd_<name>): a positional-prompt completer first (every subcommand
// but form takes a leading "Prompt" string), a variadic item completer for
// choose's trailing <item>... list, then one spec per flag — the
// subcommand's own, the cross-cutting set, and the theme set.
func zshCommandFunc(cmd Command) string {
	var specs []string
	if cmd.Args != "" {
		specs = append(specs, climeta.ZshSingleQuote("1::prompt:"))
	}
	if cmd.Name == "choose" {
		specs = append(specs, climeta.ZshSingleQuote("*::item:"))
	}
	for _, f := range cmd.Flags {
		specs = append(specs, zshFlagSpec(f))
	}
	for _, f := range CrossCuttingFlags() {
		specs = append(specs, zshFlagSpec(f))
	}
	for _, f := range ThemeFlags() {
		specs = append(specs, zshFlagSpec(f))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "_ask_cmd_%s() {\n", cmd.Name)
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

// Zsh renders the full zsh completion script for ask. Output is
// deterministic: calling Zsh() twice returns byte-identical text, so the
// committed completions/_ask file never churns on regeneration.
func Zsh() string {
	var b strings.Builder

	b.WriteString("#compdef ask\n\n")

	b.WriteString("_ask_commands() {\n")
	b.WriteString("  local -a commands\n")
	b.WriteString("  commands=(\n")
	for _, cmd := range Commands {
		b.WriteString("    " + climeta.ZshDescribeEntry(cmd.Name, cmd.Summary) + "\n")
	}
	b.WriteString("  )\n")
	b.WriteString("  _describe -t commands 'command' commands\n")
	b.WriteString("}\n\n")

	for _, cmd := range Commands {
		b.WriteString(zshCommandFunc(cmd))
		b.WriteString("\n")
	}

	b.WriteString("_ask() {\n")
	b.WriteString("  local curcontext=\"$curcontext\" state line\n")
	b.WriteString("  typeset -A opt_args\n\n")
	b.WriteString("  _arguments -C \\\n")
	b.WriteString("    '1: :->cmd' \\\n")
	b.WriteString("    '*::arg:->args'\n\n")
	b.WriteString("  case $state in\n")
	b.WriteString("    cmd)\n")
	b.WriteString("      _ask_commands\n")
	b.WriteString("      ;;\n")
	b.WriteString("    args)\n")
	b.WriteString("      case $words[1] in\n")
	for _, cmd := range Commands {
		fmt.Fprintf(&b, "        %s)\n", cmd.Name)
		fmt.Fprintf(&b, "          _ask_cmd_%s\n", cmd.Name)
		b.WriteString("          ;;\n")
	}
	b.WriteString("      esac\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n\n")
	b.WriteString("_ask \"$@\"\n")

	return b.String()
}
