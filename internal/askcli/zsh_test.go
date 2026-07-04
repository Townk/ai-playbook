package askcli

import (
	"strings"
	"testing"
)

func TestZsh_Header(t *testing.T) {
	out := Zsh()
	if !strings.HasPrefix(out, "#compdef ask\n") {
		head := out
		if len(head) > 80 {
			head = head[:80]
		}
		t.Errorf("Zsh() does not start with #compdef ask:\n%s", head)
	}
}

// TestZsh_ListsEverySubcommand asserts every subcommand's Name appears in
// the generated script and has its own per-command completion function.
func TestZsh_ListsEverySubcommand(t *testing.T) {
	out := Zsh()
	for _, cmd := range Commands {
		if !strings.Contains(out, cmd.Name) {
			t.Errorf("Zsh() does not mention command %q", cmd.Name)
		}
		marker := "_ask_cmd_" + cmd.Name + "()"
		if !strings.Contains(out, marker) {
			t.Errorf("Zsh() missing per-command function %q", marker)
		}
	}
}

// TestZsh_SubcommandFlagsPresent asserts each subcommand's own flags, plus
// the cross-cutting and theme flags, are rendered inside its function.
func TestZsh_SubcommandFlagsPresent(t *testing.T) {
	out := Zsh()
	idx := strings.Index(out, "_ask_cmd_confirm()")
	if idx < 0 {
		t.Fatal("Zsh() missing _ask_cmd_confirm()")
	}
	end := strings.Index(out[idx:], "\n}\n")
	if end < 0 {
		t.Fatal("could not find end of _ask_cmd_confirm()")
	}
	body := out[idx : idx+end]
	for _, want := range []string{"--danger", "--affirmative", "--title", "--measure"} {
		if !strings.Contains(body, want) {
			t.Errorf("_ask_cmd_confirm() body missing %q:\n%s", want, body)
		}
	}
}

// TestZsh_ThemeEnvNoted asserts a theme flag's ASK_<FLAG> env fallback is
// surfaced in its completion description.
func TestZsh_ThemeEnvNoted(t *testing.T) {
	out := Zsh()
	tf := ThemeFlags()
	if len(tf) == 0 {
		t.Fatal("ThemeFlags() returned no flags")
	}
	if !strings.Contains(out, "(env "+tf[0].Env+")") {
		t.Errorf("Zsh() missing env-fallback note for --%s", tf[0].Name)
	}
}

// TestZsh_Deterministic asserts Zsh() is byte-stable across calls, so
// regenerating completions/_ask never produces a diff by itself.
func TestZsh_Deterministic(t *testing.T) {
	first := Zsh()
	second := Zsh()
	if first != second {
		t.Error("Zsh() is not deterministic")
	}
}

// TestZsh_QuotesBalanced mirrors climeta's zsh_test.go structural sanity
// check for the same zsh-quoting-escape scheme (ZshSingleQuote).
func TestZsh_QuotesBalanced(t *testing.T) {
	out := Zsh()
	inQuote := false
	runes := []rune(out)
	for i := 0; i < len(runes); i++ {
		switch {
		case inQuote:
			if runes[i] == '\'' {
				inQuote = false
			}
		case runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '\'':
			i++ // skip the escaped literal quote
		case runes[i] == '\'':
			inQuote = true
		}
	}
	if inQuote {
		t.Error("Zsh() has an unterminated single-quoted string")
	}
}

// TestZsh_BracketsBalanced mirrors climeta's zsh_test.go structural sanity
// check: every '--flag[...]' spec's brackets must balance per line.
func TestZsh_BracketsBalanced(t *testing.T) {
	out := Zsh()
	for i, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "--") || !strings.Contains(line, "[") {
			continue
		}
		depth := 0
		runes := []rune(line)
		for j := 0; j < len(runes); j++ {
			switch runes[j] {
			case '\\':
				j++ // skip the escaped character
			case '[':
				depth++
			case ']':
				depth--
			}
		}
		if depth != 0 {
			t.Errorf("Zsh() line %d has unbalanced brackets (depth=%d): %q", i+1, depth, line)
		}
	}
}
