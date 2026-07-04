package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

func TestFormatConfirmVars_AlignsAndWraps(t *testing.T) {
	names := []string{"DATA_DIR", "SOME_LONG_VARIABLE", "PROJECT_ROOT"}
	vals := map[string]string{
		"DATA_DIR":           "$PROJECT_ROOT/data",
		"SOME_LONG_VARIABLE": "The value of this variable could span many lines depending on how long it is!",
		"PROJECT_ROOT":       "~/Projects/langs/go/ai-playbook/some/other/directory",
	}
	out := formatConfirmVars(names, vals, 51)
	lines := strings.Split(out, "\n")
	// value column = len("SOME_LONG_VARIABLE")=18 + 2 = 20
	// short value fits on the label line, aligned at col 20:
	if !strings.HasPrefix(lines[0], "DATA_DIR:") {
		t.Fatalf("line0 = %q", lines[0])
	}
	if !strings.Contains(lines[0], "$PROJECT_ROOT/data") {
		t.Fatalf("short value must sit on the label line: %q", lines[0])
	}
	// the label is padded so the value starts at col 20 (18 + colon + space):
	if idx := strings.Index(lines[0], "$PROJECT_ROOT/data"); idx != 20 {
		t.Errorf("value column = %d, want 20", idx)
	}
	// the long value wraps: a continuation line is indented to col 20 (all spaces before it):
	var cont string
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" && strings.HasPrefix(l, strings.Repeat(" ", 20)) {
			cont = l
			break
		}
	}
	if cont == "" {
		t.Fatal("expected a continuation line indented to the value column")
	}
	// no rendered line exceeds the inner width:
	for _, l := range lines {
		if lipgloss.Width(l) > 51 {
			t.Errorf("line exceeds innerW: %q (%d)", l, lipgloss.Width(l))
		}
	}
}

func TestFormatConfirmVars_HardBreaksLongToken(t *testing.T) {
	// a single unbreakable token longer than the available width must char-break, not overflow.
	out := formatConfirmVars([]string{"P"}, map[string]string{"P": strings.Repeat("x", 200)}, 51)
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 51 {
			t.Fatalf("long token must hard-break; line width %d: %q", lipgloss.Width(l), l)
		}
	}
}

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

// TestBuildConfirmVars_DeclaredDefaults verifies the declared front-matter `value:` is
// used as each var's default (shown literally, shell expands later), a live shell value
// overrides it, and PROJECT_ROOT always takes the heuristic root.
func TestBuildConfirmVars_DeclaredDefaults(t *testing.T) {
	env := map[string]frontmatter.EnvValue{
		"PROJECT_ROOT": {Value: "declared/ignored", Why: "root"},
		"DATA_DIR":     {Value: "$PROJECT_ROOT/data", Why: "data"},
		"OVERRIDE_ME":  {Value: "declared-default", Why: "x"},
	}
	getenv := func(k string) string {
		if k == "OVERRIDE_ME" {
			return "shell-override"
		}
		return ""
	}
	got := buildConfirmVars(env, "/abs/root", getenv)
	want := []confirmVar{
		{"DATA_DIR", "$PROJECT_ROOT/data", "data"}, // declared default, shown literally
		{"OVERRIDE_ME", "shell-override", "x"},     // live shell env overrides the default
		{"PROJECT_ROOT", "/abs/root", "root"},      // heuristic root wins over declared
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
		blockStates: map[string]blockRunState{},
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

// TestGate_EscReturnsToReading: ESC on the confirm dialog (not a per-var edit) now ends
// the run — it does not return to reading. Confirm the gate clears and the model exits
// ask-mode without ever being satisfied, and that the returned cmd is tea.Quit.
func TestGate_EscReturnsToReading(t *testing.T) {
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	m, cmd := m.advanceGate("", false) // ESC on the confirm dialog
	if m.gate != nil || m.askMode || m.gateSatisfied {
		t.Fatalf("ESC must clear the gate, leave it unsatisfied, exit ask mode")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("confirm-phase ESC must return a tea.Quit command")
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
	// Assert the edit was actually stored before proceeding to the next var.
	if m.gate.values["FOO"] != "/edited/foo" {
		t.Fatalf("FOO edit not stored: got %q, want %q", m.gate.values["FOO"], "/edited/foo")
	}
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
	// Verify the direct-run path was actually taken: the block must be marked running.
	// (emitAction returns nil without an orch, so cmd==nil here is by design; block state
	// is the reliable proof that the run path — not a silent no-op — was followed.)
	if st := direct.blockStates["fix"]; st.Status != "running" {
		t.Fatalf("satisfied run must mark block running; got Status=%q", st.Status)
	}
}

// TestGate_ESCDoesNotMarkRunning is the regression test for the ESC-spinner bug:
// opening the gate must NOT mark the block running; ESC must leave it idle.
func TestGate_ESCDoesNotMarkRunning(t *testing.T) {
	m := gateModel(t)
	const blkID = "fix"
	m, _ = m.beginGate(Button{Kind: "run", BlockID: blkID, Payload: "x"})
	if !m.askMode || m.gate == nil {
		t.Fatal("beginGate must raise a dialog")
	}
	// Block must NOT be running just because the gate opened.
	if st := m.blockStates[blkID]; st.Status == "running" {
		t.Fatalf("opening the gate must not mark block running; got Status=%q", st.Status)
	}
	// ESC on the confirm dialog: gate cancelled, run ends.
	m, cmd := m.advanceGate("", false)
	if m.gate != nil || m.askMode || m.gateSatisfied {
		t.Fatal("ESC must clear the gate, exit ask-mode, and leave gate unsatisfied")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("confirm-phase ESC must return a tea.Quit command")
	}
	// After ESC the block must still NOT be running.
	if st := m.blockStates[blkID]; st.Status == "running" {
		t.Fatalf("ESC must not leave block %q in running state; got Status=%q", blkID, st.Status)
	}
}

// newGateModelInConfirmPhase builds a model with a live m.gate in the confirm
// phase (beginGate raises the first group's Confirm/Customize/Quit dialog).
func newGateModelInConfirmPhase(t *testing.T) model {
	t.Helper()
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if m.gate == nil || m.gate.customizing {
		t.Fatal("newGateModelInConfirmPhase: expected a live gate in the confirm phase")
	}
	return m
}

// newGateModelInCustomizePhase builds a model with a live m.gate in the
// customizing phase (Customize answered on the confirm dialog).
func newGateModelInCustomizePhase(t *testing.T) model {
	t.Helper()
	m := newGateModelInConfirmPhase(t)
	m, _ = m.advanceGate("no", true) // Customize → per-var edit phase
	if m.gate == nil || !m.gate.customizing {
		t.Fatal("newGateModelInCustomizePhase: expected a live gate in the customizing phase")
	}
	return m
}

// isQuitCmd reports whether cmd, when invoked, yields tea.QuitMsg.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestAdvanceGate_QuitButtonQuitsRun(t *testing.T) {
	m := newGateModelInConfirmPhase(t)
	m, cmd := m.advanceGate("quit", true)
	if m.gate != nil {
		t.Fatal("Quit must clear the gate")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("Quit must return a tea.Quit command")
	}
}

func TestAdvanceGate_ConfirmEscQuitsRun(t *testing.T) {
	m := newGateModelInConfirmPhase(t)
	m, cmd := m.advanceGate("", false) // ESC on the confirm dialog
	if m.gate != nil {
		t.Fatal("confirm-phase ESC must clear the gate")
	}
	if !isQuitCmd(cmd) {
		t.Fatal("confirm-phase ESC must return a tea.Quit command")
	}
}

func TestAdvanceGate_EditEscReturnsToConfirm(t *testing.T) {
	m := newGateModelInCustomizePhase(t)
	m, cmd := m.advanceGate("", false) // ESC while editing a var
	if m.gate == nil {
		t.Fatal("edit-phase ESC must keep the gate (back to confirm, not quit)")
	}
	if m.gate.customizing {
		t.Fatal("edit-phase ESC must leave the customizing phase")
	}
	if isQuitCmd(cmd) {
		t.Fatal("edit-phase ESC must not quit the run")
	}
	if !m.askMode { // re-raised the confirm dialog
		t.Fatal("edit-phase ESC must re-raise the confirm dialog")
	}
}

// TestShellQuote verifies that shellQuote produces correct POSIX single-quoting,
// including injection-safe escaping of embedded single quotes and shell metacharacters.
func TestShellQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"plain", "'plain'"},
		{"/path/to/dir", "'/path/to/dir'"},
		// Embedded single quote — the critical injection safety case.
		{"a'b", `'a'\''b'`},
		// Single quote + shell metachars: must be fully contained, no injection escape.
		{"a'b; rm -rf /", `'a'\''b; rm -rf /'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.input); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// TestBuildExportCmd_Quoting verifies that buildExportCmd shell-quotes values safely.
func TestBuildExportCmd_Quoting(t *testing.T) {
	values := map[string]string{
		"FOO": "a'b; rm -rf /",
		"BAR": "plain",
	}
	got := buildExportCmd(values)
	// Sorted by name: BAR first, then FOO.
	want := `export BAR='plain'; export FOO='a'\''b; rm -rf /'; `
	if got != want {
		t.Errorf("buildExportCmd = %q, want %q", got, want)
	}
}

// TestBuildExportCmd_ExpandsConfirmedRefs verifies a derived value referencing another
// confirmed var (DATA_DIR=$PROJECT_ROOT/data, NESTED=${DATA_DIR}/logs) is expanded to
// the resolved path at export time — so the shell never creates a literal "$PROJECT_ROOT"
// directory — while a literal $ that is not a confirmed var name (p$ssw0rd) is untouched.
func TestBuildExportCmd_ExpandsConfirmedRefs(t *testing.T) {
	values := map[string]string{
		"PROJECT_ROOT": "/abs/portable",
		"DATA_DIR":     "$PROJECT_ROOT/data",
		"NESTED":       "${DATA_DIR}/logs",
		"LITERAL":      "p$ssw0rd",
	}
	got := buildExportCmd(values)
	// Sorted by name: DATA_DIR, LITERAL, NESTED, PROJECT_ROOT.
	want := `export DATA_DIR='/abs/portable/data'; export LITERAL='p$ssw0rd'; export NESTED='/abs/portable/data/logs'; export PROJECT_ROOT='/abs/portable'; `
	if got != want {
		t.Errorf("buildExportCmd = %q, want %q", got, want)
	}
}
