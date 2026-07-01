package autorun

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/frontmatter"
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
