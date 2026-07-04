package driver

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestShAdapterTokens(t *testing.T) {
	a := shAdapter{}

	if got := a.name(); got != "sh" {
		t.Errorf("name() = %q, want %q", got, "sh")
	}
	if got := a.jobExt(); got != "sh" {
		t.Errorf("jobExt() = %q, want %q", got, "sh")
	}
	if got := a.spawnArgs(); len(got) != 1 || got[0] != "-i" {
		t.Errorf("spawnArgs() = %v, want [-i]", got)
	}
	if got := a.sourceCmd("/x"); got != ". /x" {
		t.Errorf("sourceCmd(/x) = %q, want %q", got, ". /x")
	}
	if got := a.cdCmd("/p"); got != "cd -- '/p' 2>/dev/null" {
		t.Errorf("cdCmd(/p) = %q, want %q", got, "cd -- '/p' 2>/dev/null")
	}

	// job with id — POSIX-sh tokens present, zsh/bash-specific tokens absent.
	jobWithID := a.job(jobParams{
		cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd",
		id: "fix", key: "fix",
	})

	for _, want := range []string{"printf '%s\\n'", "$(cat", "__apb_q"} {
		if !strings.Contains(jobWithID, want) {
			t.Errorf("job (id case) must contain %q\ngot: %q", want, jobWithID)
		}
	}
	for _, forbidden := range []string{"[[", "printf %q", "${(q)", "print -r", "$(<"} {
		if strings.Contains(jobWithID, forbidden) {
			t.Errorf("job (id case) must NOT contain %q\ngot: %q", forbidden, jobWithID)
		}
	}
	// APB_* exports must be present when id is set.
	for _, want := range []string{"APB_OUT_fix", "APB_ERR_fix", "APB_EXIT_fix"} {
		if !strings.Contains(jobWithID, want) {
			t.Errorf("job (id case) must contain %q\ngot: %q", want, jobWithID)
		}
	}

	// job without id — APB_* absent; LAST_* present.
	jobNoID := a.job(jobParams{
		cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd",
	})
	for _, forbidden := range []string{"APB_OUT_", "APB_ERR_", "APB_EXIT_"} {
		if strings.Contains(jobNoID, forbidden) {
			t.Errorf("job (no-id case) must NOT contain %q\ngot: %q", forbidden, jobNoID)
		}
	}
	if !strings.Contains(jobNoID, "export LAST_EXCODE=") {
		t.Errorf("job (no-id case) must contain LAST_EXCODE export\ngot: %q", jobNoID)
	}
	if !strings.Contains(jobNoID, "export LAST_STDOUT=") {
		t.Errorf("job (no-id case) must contain LAST_STDOUT export\ngot: %q", jobNoID)
	}
}

// TestShQuoterRoundTrip runs the emitted pure-shell quoter through a real sh and
// asserts that each quoted value, when eval'd back, reproduces the original. This
// is decision (b)'s round-trip guarantee (word-split/glob-safe re-expansion). The
// test skips gracefully when no sh binary is available.
func TestShQuoterRoundTrip(t *testing.T) {
	shBin, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on PATH; skipping quoter round-trip")
	}

	values := []string{
		"a b",              // word-splitting
		"a'b",              // embedded single quote
		"a\nb",             // newline
		"$(x)",             // command substitution
		"",                 // empty
		"a'\\''b",          // already-escaped sequence
		"*",                // glob
		"  leading/trail ", // surrounding whitespace
	}

	for _, v := range values {
		// Build a script that defines the quoter, quotes v, eval's it back into
		// `back`, and prints back delimited by sentinels so we can compare exactly.
		script := shQuoterFunc + "\n" +
			"q=$(__apb_q \"$1\")\n" +
			"eval \"back=$q\"\n" +
			"printf '<<%s>>' \"$back\"\n"
		out, runErr := exec.Command(shBin, "-c", script, "sh", v).Output()
		if runErr != nil {
			t.Fatalf("running quoter for %q: %v", v, runErr)
		}
		got := string(out)
		want := "<<" + v + ">>"
		if got != want {
			t.Errorf("round-trip mismatch for %q:\n got %q\nwant %q", v, got, want)
		}
	}
}

func TestResolveSh(t *testing.T) {
	shOnPath := func(name string) (string, error) {
		if name == "sh" {
			return "sh", nil
		}
		return "", errors.New("not found: " + name)
	}
	noEnv := func(string) string { return "" }

	bin, a, err := resolveShell("sh", noEnv, shOnPath)
	if err != nil {
		t.Fatalf("resolveShell(\"sh\") unexpected error: %v", err)
	}
	if bin != "sh" {
		t.Errorf("bin = %q, want %q", bin, "sh")
	}
	if a.name() != "sh" {
		t.Errorf("adapter.name() = %q, want %q", a.name(), "sh")
	}
}

func TestResolveAutoShFallback(t *testing.T) {
	// zsh/bash absent; $SHELL=/bin/sh (or dash) → should resolve to sh as the
	// final fallback rather than errUnsupportedShell.
	shAbsPath := "/bin/sh"
	lookSh := func(name string) (string, error) {
		if name == shAbsPath || name == "sh" {
			return name, nil
		}
		return "", errors.New("not found: " + name)
	}
	getenv := func(k string) string {
		if k == "SHELL" {
			return shAbsPath
		}
		return ""
	}

	bin, a, err := resolveShell("", getenv, lookSh)
	if err != nil {
		t.Fatalf("resolveShell(\"\") with SHELL=/bin/sh unexpected error: %v", err)
	}
	if a.name() != "sh" {
		t.Errorf("adapter.name() = %q, want %q", a.name(), "sh")
	}
	_ = bin
}

// TestResolveAutoAllAbsentFallsBackToSh covers the all-absent-except-sh "" path:
// zsh and bash unavailable, $SHELL unset, but sh present on PATH → sh.
func TestResolveAutoAllAbsentFallsBackToSh(t *testing.T) {
	lookShOnly := func(name string) (string, error) {
		if name == "sh" {
			return "sh", nil
		}
		return "", errors.New("not found: " + name)
	}
	noEnv := func(string) string { return "" }

	_, a, err := resolveShell("", noEnv, lookShOnly)
	if err != nil {
		t.Fatalf("resolveShell(\"\") all-absent-except-sh unexpected error: %v", err)
	}
	if a.name() != "sh" {
		t.Errorf("adapter.name() = %q, want %q", a.name(), "sh")
	}
}
