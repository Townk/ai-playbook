package floatinput

import (
	"os"
	"strings"
	"testing"
	"time"

	"ai-playbook/mux"
)

// recordMux is a recording fake Mux. SpawnFloat captures the spawned argv and,
// via answer, simulates the float writing the user's submitted value to the
// --out file (so the Asker's poll observes a submit). A blank answer with
// cancel=true simulates a cancel (no file written → the poll times out).
type recordMux struct {
	floats [][]string // argv of each SpawnFloat
	answer string     // value the simulated float "submits" to --out
	cancel bool       // when true, write nothing (simulate cancel)
}

func (m *recordMux) DumpScreen(string) (string, error)  { return "", nil }
func (m *recordMux) SpawnPane(mux.SpawnOptions) error   { return nil }
func (m *recordMux) SpawnDocked(mux.SpawnOptions) error { return nil }
func (m *recordMux) TypeInto(string, string) error      { return nil }
func (m *recordMux) SpawnFloat(opts mux.SpawnOptions) error {
	m.floats = append(m.floats, opts.Cmd)
	out := outFromArgv(opts.Cmd)
	if m.cancel {
		// Simulate the float writing the cancel marker on dismiss.
		if out != "" {
			_ = os.WriteFile(out+cancelSuffix, nil, 0o600)
		}
		return nil
	}
	// Simulate the floated `input --out <file>` writing the submitted value.
	if out != "" {
		_ = os.WriteFile(out, []byte(m.answer), 0o600)
	}
	return nil
}

// outFromArgv returns the value after the --out flag in an argv.
func outFromArgv(argv []string) string {
	for i, a := range argv {
		if a == "--out" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func TestAsker_BuildsInputCommand(t *testing.T) {
	m := &recordMux{answer: "fix the build"}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond}
	res, err := a.Ask(Request{Type: "text", Title: "ai-assist", Prompt: "How can I help?", Value: "seed", Cwd: "/proj"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Submitted {
		t.Fatal("expected Submitted=true")
	}
	if res.Value != "fix the build" {
		t.Fatalf("value = %q, want %q", res.Value, "fix the build")
	}
	if len(m.floats) != 1 {
		t.Fatalf("expected 1 SpawnFloat, got %d", len(m.floats))
	}
	argv := m.floats[0]
	// The float runs `<selfExe> input --type text --out <tmp> --title … --prompt … --value …`.
	if argv[0] != "/path/ai-playbook" || argv[1] != "input" {
		t.Fatalf("argv prefix = %v, want [/path/ai-playbook input …]", argv[:2])
	}
	for _, want := range []string{"--type", "text", "--out", "--title", "ai-assist", "--prompt", "How can I help?", "--value", "seed"} {
		if !contains(argv, want) {
			t.Errorf("argv missing %q\nargv: %v", want, argv)
		}
	}
}

func TestAsker_FreeMapsToText(t *testing.T) {
	m := &recordMux{answer: "x"}
	a := Asker{SelfExe: "ai-playbook", Mux: m, poll: time.Millisecond}
	if _, err := a.Ask(Request{Type: "free"}); err != nil {
		t.Fatal(err)
	}
	argv := m.floats[0]
	if v := after(argv, "--type"); v != "text" {
		t.Fatalf("--type = %q, want text (free→text)", v)
	}
}

func TestAsker_ChooseAppendsOptions(t *testing.T) {
	m := &recordMux{answer: "b"}
	a := Asker{SelfExe: "ai-playbook", Mux: m, poll: time.Millisecond}
	if _, err := a.Ask(Request{Type: "choose", Choices: []string{"a", "b", "c"}, Multi: true}); err != nil {
		t.Fatal(err)
	}
	argv := m.floats[0]
	if !contains(argv, "--multi") {
		t.Errorf("choose with Multi should pass --multi\nargv: %v", argv)
	}
	// The options are positionals at the tail.
	tail := strings.Join(argv[len(argv)-3:], " ")
	if tail != "a b c" {
		t.Errorf("choose options tail = %q, want %q", tail, "a b c")
	}
}

func TestAsker_CancelReturnsNotSubmitted(t *testing.T) {
	m := &recordMux{cancel: true}
	a := Asker{SelfExe: "ai-playbook", Mux: m, poll: time.Millisecond, Timeout: 50 * time.Millisecond}
	res, err := a.Ask(Request{Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Submitted {
		t.Fatal("expected Submitted=false on cancel (no out-file written)")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func after(ss []string, key string) string {
	for i, s := range ss {
		if s == key && i+1 < len(ss) {
			return ss[i+1]
		}
	}
	return ""
}
