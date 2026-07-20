// main_test.go — TDD tests for the top-level --help/help dispatch (see
// .superpowers/sdd/task-2-brief.md). Run() returns an exit code (rather than
// calling os.Exit) precisely so this dispatch is unit-testable; the help
// decision itself lives in the pure helpFor function, tested directly here.
package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written to it — used to assert on Run()'s printed help
// and version text without letting it hit the test binary's real stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}

// TestHelpFor_TopLevelNoCommand asserts `-h`/`--help`/`help` with no trailing
// command name resolves to the top-level Overview(), handled.
func TestHelpFor_TopLevelNoCommand(t *testing.T) {
	overview := climeta.Overview("ai-playbook")
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		text, handled := helpFor("ai-playbook", args)
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
		text, handled := helpFor("ai-playbook", args)
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
	_, handled := helpFor("ai-playbook", []string{"help", "nope-not-a-command"})
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
		text, handled := helpFor("ai-playbook", args)
		if !handled {
			t.Errorf("helpFor(%v) handled=false, want true", args)
		}
		want, ok := climeta.Help("ai-playbook", args[0])
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
		_, handled := helpFor("ai-playbook", args)
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

// TestRun_HelpUsesInvokedProgName asserts Run computes the program name from
// args[0] and threads it into the help text: `apb --help` reads "apb"
// throughout (including the footer) and never falls back to the hardcoded
// "ai-playbook".
func TestRun_HelpUsesInvokedProgName(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"apb", "--help"})
	})
	if code != 0 {
		t.Errorf("Run([apb --help]) = %d, want 0", code)
	}
	if !strings.Contains(out, "apb <command> --help") {
		t.Errorf("Run([apb --help]) footer does not say \"apb <command> --help\":\n%s", out)
	}
	if strings.Contains(out, "ai-playbook") {
		t.Errorf("Run([apb --help]) unexpectedly mentions \"ai-playbook\":\n%s", out)
	}
}

// TestRun_HelpUsesAiPlaybookProgName asserts the same dispatch invoked as
// "ai-playbook" reads "ai-playbook" throughout (the sibling case to
// TestRun_HelpUsesInvokedProgName).
func TestRun_HelpUsesAiPlaybookProgName(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"ai-playbook", "--help"})
	})
	if code != 0 {
		t.Errorf("Run([ai-playbook --help]) = %d, want 0", code)
	}
	if !strings.Contains(out, "ai-playbook <command> --help") {
		t.Errorf("Run([ai-playbook --help]) footer does not say \"ai-playbook <command> --help\":\n%s", out)
	}
}

// TestRun_VersionUsesInvokedProgName asserts `apb version` prints "apb
// <version>" rather than the hardcoded "ai-playbook <version>".
func TestRun_VersionUsesInvokedProgName(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"apb", "version"})
	})
	if code != 0 {
		t.Errorf("Run([apb version]) = %d, want 0", code)
	}
	if !strings.HasPrefix(out, "apb ") {
		t.Errorf("Run([apb version]) output does not start with \"apb \":\n%q", out)
	}
}

// dispatchOnlyKeys lists dispatch keys that are deliberately NOT in the
// climeta registry: "--version"/"-v" are flag-shaped synonyms for the
// "version" command, kept in dispatch for user convenience (`ai-playbook
// --version` is a common idiom) but not worth documenting as their own
// registry entries since "version" already covers them in help/man/
// completion.
var dispatchOnlyKeys = map[string]bool{
	"--version": true,
	"-v":        true,
	// __cursor-pretool-hook is an internal machine interface (the cursor FULL
	// builtin-tool allowlist gate), invoked by cursor via a planted hooks.json —
	// never by a user — so it is intentionally absent from the climeta registry.
	"__cursor-pretool-hook": true,
}

// registryNames returns every name and alias climeta.Commands registers,
// mirroring climeta's own (unexported) commandIndex construction.
func registryNames() map[string]bool {
	names := make(map[string]bool)
	for _, cmd := range climeta.Commands {
		names[cmd.Name] = true
		for _, alias := range cmd.Aliases {
			names[alias] = true
		}
	}
	return names
}

// TestDispatchRegistrySync is the two-way sync check between cli.dispatch
// and climeta.Commands: every dispatch key must resolve to a registered
// command name/alias (else it tab-completes to nothing / gets no --help/man
// page), and every registered name/alias must have a dispatch entry (else it
// tab-completes but dies at runtime with "unknown subcommand") — modulo the
// documented dispatchOnlyKeys exception list above. This is the guardrail
// that motivated the switch→map refactor: a plain switch could silently
// drift from the registry in either direction.
func TestDispatchRegistrySync(t *testing.T) {
	registry := registryNames()

	for key := range dispatch {
		if dispatchOnlyKeys[key] {
			continue
		}
		if !registry[key] {
			t.Errorf("dispatch key %q has no climeta.Commands entry (name or alias) — add one, or add %q to dispatchOnlyKeys if it is deliberately dispatch-only", key, key)
		}
	}

	for name := range registry {
		if _, ok := dispatch[name]; !ok {
			t.Errorf("climeta command/alias %q has no dispatch entry — it tab-completes and shows help but dies at runtime", name)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name     string
		ldflag   string
		buildVer string
		buildOK  bool
		want     string
	}{
		{"ldflag wins over build info", "0.6.1", "v0.6.0", true, "0.6.1"},
		{"dev falls back to build info", "dev", "v0.6.1", true, "v0.6.1"},
		{"dev stays dev when build info absent", "dev", "", false, "dev"},
		{"dev stays dev on (devel) build", "dev", "(devel)", true, "dev"},
		{"dev stays dev on empty build version", "dev", "", true, "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.ldflag, tc.buildVer, tc.buildOK); got != tc.want {
				t.Errorf("resolveVersion(%q, %q, %v) = %q, want %q", tc.ldflag, tc.buildVer, tc.buildOK, got, tc.want)
			}
		})
	}
}

// TestRun_DispatchAndErrors covers Run's non-help branches: no args → usage to
// stderr + exit 2; an unknown subcommand → error + usage + exit 2; a known
// subcommand ("version") dispatches and prints the prog-aware version line.
func TestRun_DispatchAndErrors(t *testing.T) {
	if code := Run([]string{"ai-playbook"}); code != 2 {
		t.Errorf("no args: exit = %d, want 2", code)
	}
	if code := Run([]string{"ai-playbook", "definitely-not-a-command"}); code != 2 {
		t.Errorf("unknown subcommand: exit = %d, want 2", code)
	}
	out := captureStdout(t, func() {
		if code := Run([]string{"apb", "version"}); code != 0 {
			t.Errorf("version: exit = %d, want 0", code)
		}
	})
	if !strings.HasPrefix(out, "apb ") {
		t.Errorf("version output must be prog-aware: %q", out)
	}
}

// TestDirExistsAndHead covers the selftest helpers.
func TestDirExistsAndHead(t *testing.T) {
	if !dirExists(t.TempDir()) {
		t.Error("dirExists must be true for a real directory")
	}
	if dirExists("/definitely/not/a/dir") {
		t.Error("dirExists must be false for a missing path")
	}
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if dirExists(f) {
		t.Error("dirExists must be false for a plain file")
	}
	if got := head("a\nb\ncdef", 3); got != "a b" {
		t.Errorf("head = %q, want %q (newlines flattened, cut at n)", got, "a b")
	}
	if got := head("ab", 10); got != "ab" {
		t.Errorf("head must not pad short strings: %q", got)
	}
}
