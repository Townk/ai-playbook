package autorun

import (
	"strings"
	"testing"

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
