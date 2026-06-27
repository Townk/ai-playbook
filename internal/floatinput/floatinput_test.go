package floatinput

import (
	"os"
	"strings"
	"testing"
	"time"

	"ai-playbook/internal/mux"
)

// recordMux is a recording fake Mux. SpawnFloat captures the spawned argv and,
// via answer, simulates the float writing the user's submitted value to the
// --out file (so the Asker's poll observes a submit). A blank answer with
// cancel=true simulates a cancel (no file written → the poll times out).
type recordMux struct {
	floats   [][]string       // argv of each input float
	lastOpts mux.SpawnOptions // the last SpawnInputFloat opts (geometry assertions)
	answer   string           // value the simulated float "submits" to --out
	cancel   bool             // when true, write nothing (simulate cancel)
}

func (m *recordMux) DumpScreen(string) (string, error)  { return "", nil }
func (m *recordMux) SpawnPane(mux.SpawnOptions) error   { return nil }
func (m *recordMux) SpawnDocked(mux.SpawnOptions) error { return nil }
func (m *recordMux) TypeInto(string, string) error      { return nil }
func (m *recordMux) SpawnFloat(mux.SpawnOptions) error  { return nil }

// SpawnInputFloat is the request/ask seam Asker.Ask now drives. It records the
// opts (argv + absolute geometry) and simulates the floated `input --out <file>`
// writing the submitted value (or the cancel marker).
func (m *recordMux) SpawnInputFloat(opts mux.SpawnOptions) error {
	m.lastOpts = opts
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
	res, err := a.Ask(Request{Type: "text", Title: "ai-playbook", Prompt: "How can I help?", Value: "seed", Cwd: "/proj"})
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
	for _, want := range []string{"--type", "text", "--out", "--title", "ai-playbook", "--prompt", "How can I help?", "--value", "seed"} {
		if !contains(argv, want) {
			t.Errorf("argv missing %q\nargv: %v", want, argv)
		}
	}
}

// The input float is spawned through SpawnInputFloat with ABSOLUTE geometry:
// WidthCols=57 and a positive HeightRows (the measured height, or the 9-row
// fallback when measuring can't run because SelfExe isn't a real binary). The
// pane Name is empty — matching ai-assist-summon's `--name "" --width 57`.
func TestAsker_InputFloatAbsoluteGeometry(t *testing.T) {
	m := &recordMux{answer: "x"}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond}
	if _, err := a.Ask(Request{Type: "text", Title: "ai-playbook", Prompt: "How?"}); err != nil {
		t.Fatal(err)
	}
	if m.lastOpts.WidthCols != floatCols {
		t.Errorf("WidthCols = %d, want %d", m.lastOpts.WidthCols, floatCols)
	}
	if m.lastOpts.HeightRows <= 0 {
		t.Errorf("HeightRows = %d, want a positive (measured or fallback) height", m.lastOpts.HeightRows)
	}
	// SelfExe isn't a real binary here, so measuring fails → the 9-row fallback.
	if m.lastOpts.HeightRows != fallbackHeight {
		t.Errorf("HeightRows = %d, want fallback %d when measuring can't run", m.lastOpts.HeightRows, fallbackHeight)
	}
	if m.lastOpts.Name != "" {
		t.Errorf("input float Name = %q, want empty", m.lastOpts.Name)
	}
	// The live float carries --height (so it renders like the measured pane).
	if after(m.floats[0], "--height") != "3" {
		t.Errorf("float argv --height = %q, want 3", after(m.floats[0], "--height"))
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

// TestAsker_HistoryWiredOnlyWhenSet asserts the request float (History set on a
// text Request) carries `--history <path>`, while the ask/`f` floats (History
// empty) carry no --history at all — history is opt-in per invocation.
func TestAsker_HistoryWiredOnlyWhenSet(t *testing.T) {
	// Request float: History set on a text ask → --history present with the path.
	m := &recordMux{answer: "x"}
	a := Asker{SelfExe: "ai-playbook", Mux: m, poll: time.Millisecond}
	if _, err := a.Ask(Request{Type: "text", History: "/data/request-history.jsonl"}); err != nil {
		t.Fatal(err)
	}
	if got := after(m.floats[0], "--history"); got != "/data/request-history.jsonl" {
		t.Errorf("request-float --history = %q, want /data/request-history.jsonl", got)
	}

	// Ask/`f` float: History empty → NO --history flag.
	m2 := &recordMux{answer: "x"}
	a2 := Asker{SelfExe: "ai-playbook", Mux: m2, poll: time.Millisecond}
	if _, err := a2.Ask(Request{Type: "text"}); err != nil {
		t.Fatal(err)
	}
	if contains(m2.floats[0], "--history") {
		t.Errorf("ask/`f` float must not pass --history\nargv: %v", m2.floats[0])
	}

	// Non-text float (e.g. choose) ignores History even if set.
	m3 := &recordMux{answer: "b"}
	a3 := Asker{SelfExe: "ai-playbook", Mux: m3, poll: time.Millisecond}
	if _, err := a3.Ask(Request{Type: "choose", Choices: []string{"a", "b"}, History: "/data/h.jsonl"}); err != nil {
		t.Fatal(err)
	}
	if contains(m3.floats[0], "--history") {
		t.Errorf("non-text float must not pass --history\nargv: %v", m3.floats[0])
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
