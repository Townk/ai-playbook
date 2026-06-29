package ui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

func TestGroupSizes(t *testing.T) {
	cases := []struct {
		n    int
		want []int
	}{
		{0, nil},
		{1, []int{1}},
		{5, []int{5}},
		{6, []int{3, 3}},
		{11, []int{4, 4, 3}},
		{12, []int{4, 4, 4}},
		{13, []int{5, 5, 3}},
		{16, []int{4, 4, 4, 4}},
	}
	for _, c := range cases {
		if got := groupSizes(c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("groupSizes(%d) = %v, want %v", c.n, got, c.want)
		}
		// every group ≤ 5
		for _, s := range groupSizes(c.n) {
			if s > 5 || s < 1 {
				t.Errorf("groupSizes(%d) produced out-of-range size %d", c.n, s)
			}
		}
	}
}

func TestLoadPlaybookDocument_ReturnsEnv(t *testing.T) {
	doc := "---\nname: T\nenv:\n  FOO:\n    why: bar\n---\n# T\n\n```bash {id=fix}\ntrue\n```\n"
	_, _, _, env := loadPlaybookDocument(doc)
	if env == nil || env["FOO"].Why != "bar" {
		t.Fatalf("loadPlaybookDocument env = %v, want FOO.why=bar", env)
	}
}

func TestBuildConfirmVars(t *testing.T) {
	env := map[string]frontmatter.EnvValue{
		"PROJECT_ROOT":     {Why: "the project directory"},
		"ANDROID_SDK_ROOT": {Why: "the SDK"},
		"UNSET_VAR":        {Why: "not in shell"},
	}
	getenv := func(k string) string {
		if k == "ANDROID_SDK_ROOT" {
			return "/live/sdk"
		}
		return ""
	}
	got := buildConfirmVars(env, "/new/proj", getenv)
	want := []confirmVar{
		{"ANDROID_SDK_ROOT", "/live/sdk", "the SDK"},
		{"PROJECT_ROOT", "/new/proj", "the project directory"},
		{"UNSET_VAR", "", "not in shell"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildConfirmVars = %v, want %v", got, want)
	}
}

func gateModel(t *testing.T) model {
	t.Helper()
	return model{
		confirmEnv:  map[string]frontmatter.EnvValue{"PROJECT_ROOT": {Why: "root"}, "FOO": {Why: "foo"}},
		projectRoot: "/proj",
	}
}

func TestGate_ConfirmRunsBlockOnce(t *testing.T) {
	m := gateModel(t)
	blk := Button{Kind: "run", BlockID: "fix", Payload: "echo hi"}
	m, _ = m.beginGate(blk)
	if !m.askMode || m.gate == nil {
		t.Fatal("beginGate should raise a dialog and set gate")
	}
	// Confirm the single group (2 vars ≤5 → 1 group): answer "yes".
	var cmd tea.Cmd
	m, cmd = m.advanceGate("yes", true)
	if m.gate != nil || !m.gateSatisfied {
		t.Fatalf("after confirm the gate should clear + be satisfied (gate=%v satisfied=%v)", m.gate, m.gateSatisfied)
	}
	if cmd == nil {
		t.Fatal("confirm should return a cmd (export + deferred block)")
	}
}

func TestGate_EscReturnsToReading(t *testing.T) {
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	m, _ = m.advanceGate("", false) // ESC
	if m.gate != nil || m.askMode || m.gateSatisfied {
		t.Fatalf("ESC must clear the gate, leave it unsatisfied, exit ask mode")
	}
}

func TestGate_CustomizeEditsValue(t *testing.T) {
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	m, _ = m.advanceGate("no", true) // Customize → first var line edit
	if !m.gate.customizing {
		t.Fatal("Customize should enter the per-var edit phase")
	}
	// edit FOO (first sorted var) then PROJECT_ROOT
	m, _ = m.advanceGate("/edited/foo", true)
	m, _ = m.advanceGate("/edited/root", true)
	if m.gate != nil || !m.gateSatisfied {
		t.Fatalf("after editing all vars the gate should finish")
	}
}

func TestGate_NoEnvRunsDirectly(t *testing.T) {
	m := model{} // empty confirmEnv
	m, cmd := m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if m.gate != nil || m.askMode {
		t.Fatal("no env → no gate")
	}
	if !m.gateSatisfied || cmd == nil {
		t.Fatal("no env → satisfied + run the block directly")
	}
}

func TestTrigger_FirstRunGated(t *testing.T) {
	m := gateModel(t) // confirmEnv non-empty, gateSatisfied=false
	gated, _ := m.runOrGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if gated.gate == nil || !gated.askMode {
		t.Fatal("first run with env vars must open the gate, not run directly")
	}
}

func TestTrigger_SatisfiedRunsDirectly(t *testing.T) {
	m := gateModel(t)
	m.gateSatisfied = true
	direct, _ := m.runOrGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if direct.gate != nil {
		t.Fatal("once satisfied, runs must not re-open the gate")
	}
}
