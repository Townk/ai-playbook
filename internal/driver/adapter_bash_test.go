package driver

import (
	"errors"
	"strings"
	"testing"
)

func TestBashAdapterTokens(t *testing.T) {
	a := bashAdapter{}

	if got := a.name(); got != "bash" {
		t.Errorf("name() = %q, want %q", got, "bash")
	}
	if got := a.jobExt(); got != "bash" {
		t.Errorf("jobExt() = %q, want %q", got, "bash")
	}
	if got := a.spawnArgs(); len(got) != 1 || got[0] != "-il" {
		t.Errorf("spawnArgs() = %v, want [-il]", got)
	}
	if got := a.sourceCmd("/x"); got != "source /x" {
		t.Errorf("sourceCmd(/x) = %q, want %q", got, "source /x")
	}
	if got := a.cdCmd("/p"); got != "builtin cd -- '/p' 2>/dev/null" {
		t.Errorf("cdCmd(/p) = %q, want %q", got, "builtin cd -- '/p' 2>/dev/null")
	}

	// job with id — check bash-specific tokens are present and zsh-specific absent.
	jobWithID := a.job(jobParams{
		cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd",
		id: "fix", key: "fix",
	})

	for _, want := range []string{"printf '%s\\n'", "printf %q"} {
		if !strings.Contains(jobWithID, want) {
			t.Errorf("job (id case) must contain %q\ngot: %q", want, jobWithID)
		}
	}
	for _, forbidden := range []string{"print -r", "${(q)"} {
		if strings.Contains(jobWithID, forbidden) {
			t.Errorf("job (id case) must NOT contain %q (zsh token)\ngot: %q", forbidden, jobWithID)
		}
	}
	// AAS_* exports must be present when id is set.
	for _, want := range []string{"AAS_OUT_fix", "AAS_ERR_fix", "AAS_EXIT_fix"} {
		if !strings.Contains(jobWithID, want) {
			t.Errorf("job (id case) must contain %q\ngot: %q", want, jobWithID)
		}
	}

	// job without id — AAS_* must be absent; LAST_* must be present.
	jobNoID := a.job(jobParams{
		cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd",
	})
	for _, forbidden := range []string{"AAS_OUT_", "AAS_ERR_", "AAS_EXIT_"} {
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

func TestResolveBash(t *testing.T) {
	bashOnPath := func(name string) (string, error) {
		if name == "bash" {
			return "bash", nil
		}
		return "", errors.New("not found: " + name)
	}
	noEnv := func(string) string { return "" }

	bin, a, err := resolveShell("bash", noEnv, bashOnPath)
	if err != nil {
		t.Fatalf("resolveShell(\"bash\") unexpected error: %v", err)
	}
	if bin != "bash" {
		t.Errorf("bin = %q, want %q", bin, "bash")
	}
	if a.name() != "bash" {
		t.Errorf("adapter.name() = %q, want %q", a.name(), "bash")
	}
}

func TestResolveAutoBashFallback(t *testing.T) {
	// zsh absent from PATH; $SHELL=/bin/bash present → should resolve to bash.
	bashAbsPath := "/bin/bash"
	lookBash := func(name string) (string, error) {
		if name == bashAbsPath || name == "bash" {
			return name, nil
		}
		return "", errors.New("not found: " + name)
	}
	getenv := func(k string) string {
		if k == "SHELL" {
			return bashAbsPath
		}
		return ""
	}

	bin, a, err := resolveShell("", getenv, lookBash)
	if err != nil {
		t.Fatalf("resolveShell(\"\") with SHELL=/bin/bash unexpected error: %v", err)
	}
	if bin != bashAbsPath {
		t.Errorf("bin = %q, want %q", bin, bashAbsPath)
	}
	if a.name() != "bash" {
		t.Errorf("adapter.name() = %q, want %q", a.name(), "bash")
	}
}
