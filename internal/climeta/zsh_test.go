// zsh_test.go — TDD tests for the zsh completion renderer (see
// .superpowers/sdd/task-4-brief.md).
package climeta

import (
	"strings"
	"testing"
)

// TestZsh_Header asserts Zsh() starts with the #compdef pragma zsh requires
// to recognize this file as a completion function for both ai-playbook and
// apb.
func TestZsh_Header(t *testing.T) {
	out := Zsh()
	if !strings.HasPrefix(out, "#compdef ai-playbook apb\n") {
		head := out
		if len(head) > 80 {
			head = head[:80]
		}
		t.Errorf("Zsh() does not start with #compdef ai-playbook apb:\n%s", head)
	}
}

// TestZsh_ListsEveryNonInternalCommand asserts every non-Internal command's
// Name appears in the generated script (so it is at least name-completable
// at the top level).
func TestZsh_ListsEveryNonInternalCommand(t *testing.T) {
	out := Zsh()
	for _, cmd := range Commands {
		if cmd.Internal {
			continue
		}
		if !strings.Contains(out, cmd.Name) {
			t.Errorf("Zsh() does not mention non-internal command %q", cmd.Name)
		}
	}
}

// TestZsh_ListsEveryCommand asserts every registered command (including
// internal/plumbing ones) is at least name-completable at the top level.
func TestZsh_ListsEveryCommand(t *testing.T) {
	out := Zsh()
	for _, cmd := range Commands {
		if !strings.Contains(out, cmd.Name) {
			t.Errorf("Zsh() does not mention command %q", cmd.Name)
		}
	}
}

// TestZsh_SlugHelper asserts the dynamic store-slug completion helper is
// present, verbatim, including the exact "list --format fuzzy-data-source"
// invocation it shells out to — and that it invokes the completed binary
// (via $service), never a hardcoded "ai-playbook", so slug completion still
// works when only apb is on PATH.
func TestZsh_SlugHelper(t *testing.T) {
	out := Zsh()
	if !strings.Contains(out, "_ai-playbook_slugs()") {
		t.Error(`Zsh() missing the "_ai-playbook_slugs" helper function`)
	}
	if !strings.Contains(out, "list --format fuzzy-data-source") {
		t.Error(`Zsh() missing the literal "list --format fuzzy-data-source" invocation`)
	}
	if !strings.Contains(out, "${service} list --format fuzzy-data-source") {
		t.Error(`Zsh() slug helper does not invoke "${service} list --format fuzzy-data-source"`)
	}
	if strings.Contains(out, "ai-playbook list --format") {
		t.Error(`Zsh() slug helper hardcodes "ai-playbook list --format" instead of using the invoked-name variable`)
	}
}

// TestZsh_RunFlags asserts run's --with-env flag is rendered.
func TestZsh_RunFlags(t *testing.T) {
	out := Zsh()
	if !strings.Contains(out, "--with-env") {
		t.Error(`Zsh() missing "--with-env"`)
	}
}

// TestZsh_ListFormatChoices asserts list's --format flag completes the fixed
// set of output formats, including "fuzzy-data-source".
func TestZsh_ListFormatChoices(t *testing.T) {
	out := Zsh()
	if !strings.Contains(out, "fuzzy-data-source") {
		t.Error(`Zsh() missing "fuzzy-data-source" (list --format choices)`)
	}
}

// TestZsh_SlugArgCommandsWireHelper asserts every non-Internal command whose
// positional resolves a store slug (SlugArg) has its own per-command
// completion function that references the slug helper.
func TestZsh_SlugArgCommandsWireHelper(t *testing.T) {
	out := Zsh()
	for _, cmd := range Commands {
		if cmd.Internal || !cmd.SlugArg {
			continue
		}
		marker := "_ai-playbook_cmd_" + cmd.Name + "()"
		idx := strings.Index(out, marker)
		if idx < 0 {
			t.Errorf("Zsh() missing per-command function %q", marker)
			continue
		}
		// The helper reference should appear within a reasonable window
		// after the function's opening brace (i.e. inside its body).
		end := idx + 400
		if end > len(out) {
			end = len(out)
		}
		if !strings.Contains(out[idx:end], "_ai-playbook_slugs") {
			t.Errorf("Zsh() per-command function %q does not wire _ai-playbook_slugs", marker)
		}
	}
}

// TestZsh_Deterministic asserts Zsh() is byte-stable across calls, so
// regenerating completions/_ai-playbook never produces a diff by itself.
func TestZsh_Deterministic(t *testing.T) {
	first := Zsh()
	second := Zsh()
	if first != second {
		t.Error("Zsh() is not deterministic")
	}
}

// TestZsh_QuotesBalanced is a cheap structural sanity check that every
// single-quoted zsh string literal in the script is properly terminated.
// It cannot use a naive "count of ' is even" check: this package's
// zshSingleQuote escapes an embedded apostrophe by closing the quoted
// string, emitting a backslash-escaped literal quote, then reopening the
// quoted string — which legitimately emits an odd number of raw quote
// characters per escaped apostrophe. Instead this walks the text with a
// minimal zsh-quoting-aware scanner (outside a quote, a backslash followed
// by a quote is a single escaped literal quote and does not toggle state;
// inside a quote, a quote always closes it) and asserts the scan ends
// outside any quote.
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

// TestZsh_BracketsBalanced is a cheap structural sanity check: every
// '--flag[...]' spec's brackets must balance per line (escaped brackets
// inside a description don't count, since escapeBracketBalance below skips
// them).
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
