package autorun

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

func TestRun_MissingRequiredEnv_ExitsBeforeDriver(t *testing.T) {
	var out strings.Builder
	code := Run(RunConfig{
		Blocks:  []Block{{ID: "a", Kind: KindRun, Command: "true"}},
		EnvVars: map[string]frontmatter.EnvValue{"MUST_SET": {Value: "", Why: "the API token"}},
		Out:     &out,
	})
	if code == 0 {
		t.Error("missing required env must exit non-zero")
	}
	if !strings.Contains(out.String(), "MUST_SET") || !strings.Contains(out.String(), "the API token") {
		t.Errorf("must name the missing var + why:\n%s", out.String())
	}
}

func TestRun_AllGreen_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "a", Kind: KindRun, Command: "true"},
			{ID: "b", Kind: KindRun, Command: "true", Needs: []string{"a"}},
		},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code != 0 {
		t.Fatalf("all-green exit = %d, want 0\n%s", code, out.String())
	}
}

func TestRun_Failure_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{{ID: "boom", Kind: KindRun, Command: "false"}},
		Slug:   "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code == 0 {
		t.Error("a failing block must exit non-zero")
	}
}

func TestRunStep_Interrupt_CancelsQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}

	runner, cleanup, err := newOrchRunner(RunConfig{}, io.Discard, nil)
	if err != nil {
		t.Fatalf("newOrchRunner: %v", err)
	}
	defer cleanup()

	stopCh := make(chan struct{})
	runner.stopCh = stopCh

	type stepResult struct {
		exit      int
		cancelled bool
	}
	done := make(chan stepResult, 1)
	go func() {
		exit, _, cancelled := runner.RunStep(Step{ID: "slow", Kind: KindRun, Command: "sleep 5"})
		done <- stepResult{exit: exit, cancelled: cancelled}
	}()

	// Give the block a moment to actually start running before interrupting.
	time.Sleep(300 * time.Millisecond)
	close(stopCh)

	select {
	case r := <-done:
		if r.exit == 0 {
			t.Errorf("interrupted step exit = 0, want non-zero")
		}
		if !r.cancelled {
			t.Error("interrupted step cancelled = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunStep did not return within 2s of stopCh closing")
	}
}

func TestResolveEnv_OverridePrecedence(t *testing.T) {
	t.Setenv("PR_EXPORTED", "from-env")
	vars := map[string]frontmatter.EnvValue{
		"PR_OVERRIDE": {Value: "from-default", Why: "x"}, // override beats default
		"PR_EXPORTED": {Value: "from-default", Why: "x"}, // override beats exported env
		"PR_DEFAULT":  {Value: "from-default", Why: "x"}, // no override, no env → default
	}
	overrides := map[string]string{"PR_OVERRIDE": "from-cli", "PR_EXPORTED": "from-cli"}
	env, missing := resolveEnv(vars, overrides)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	// last-wins: the resolved value for each var is what the child would see.
	want := map[string]string{"PR_OVERRIDE": "from-cli", "PR_EXPORTED": "from-cli", "PR_DEFAULT": "from-default"}
	got := lastEnvValues(env, []string{"PR_OVERRIDE", "PR_EXPORTED", "PR_DEFAULT"})
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestResolveEnv_EmptyOverrideFallsThrough(t *testing.T) {
	t.Setenv("PR_EMPTY", "from-env")
	vars := map[string]frontmatter.EnvValue{"PR_EMPTY": {Value: "from-default", Why: "x"}}
	env, missing := resolveEnv(vars, map[string]string{"PR_EMPTY": ""}) // empty → not provided
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	if got := lastEnvValues(env, []string{"PR_EMPTY"})["PR_EMPTY"]; got != "from-env" {
		t.Errorf("empty override must fall through to exported env; got %q", got)
	}
}

func TestResolveEnv_MissingStillReported(t *testing.T) {
	vars := map[string]frontmatter.EnvValue{"PR_MISSING": {Value: "", Why: "needed"}}
	_, missing := resolveEnv(vars, nil)
	if len(missing) != 1 || missing[0].name != "PR_MISSING" {
		t.Fatalf("missing = %v, want [PR_MISSING]", missing)
	}
}

// lastEnvValues returns, for each requested name, the LAST value in env (the
// value os/exec would give the child for a duplicate key).
func lastEnvValues(env []string, names []string) map[string]string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]string{}
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 && want[e[:i]] {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}

func TestRun_WarnsUndeclaredOverride(t *testing.T) {
	var buf bytes.Buffer
	rc := RunConfig{
		Blocks:       nil, // no blocks → Execute no-ops after env preflight
		EnvVars:      map[string]frontmatter.EnvValue{"KNOWN": {Value: "v", Why: "x"}},
		EnvOverrides: map[string]string{"KNOWN": "v", "BOGUS": "z", "ALSO_BOGUS": "z"},
		Out:          &buf,
		Now:          func() string { return "T" },
	}
	_ = Run(rc)
	got := buf.String()
	if !strings.Contains(got, "with-env: ignoring undeclared variable ALSO_BOGUS") ||
		!strings.Contains(got, "with-env: ignoring undeclared variable BOGUS") {
		t.Fatalf("expected sorted undeclared-key warnings; got:\n%s", got)
	}
	// sorted order: ALSO_BOGUS before BOGUS. Match full lines (not the bare
	// "BOGUS" substring, which also occurs inside "ALSO_BOGUS").
	if strings.Index(got, "variable ALSO_BOGUS\n") > strings.Index(got, "variable BOGUS\n") {
		t.Errorf("warnings must be sorted; got:\n%s", got)
	}
}
