package floatinput

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/mux"
)

// recordMux is a recording fake Mux. SpawnFloat captures the spawned argv and,
// via answer, simulates the float writing the user's submitted value to the
// --out file (so the Asker's poll observes a submit). A blank answer with
// cancel=true simulates a cancel (no file written → the poll times out).
type recordMux struct {
	floats   [][]string       // argv of each input float
	lastOpts mux.SpawnOptions // the last SpawnInputFloat opts (geometry assertions)
	answer   string           // value the simulated float "submits" to --out
	cancel   bool             // when true, write cancel marker (simulate dismiss)
	spawnErr error            // when non-nil, SpawnInputFloat returns this error
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
	if m.spawnErr != nil {
		return m.spawnErr
	}
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

// ---------------------------------------------------------------------------
// AskThinking tests
// ---------------------------------------------------------------------------

// TestAskThinking_Submit verifies the happy path: the float writes the answer
// file, poll_ reads it, AskThinking returns Submitted=true plus the out path.
// It also checks that --thinking is present in the spawned argv, confirming
// buildCmd wires the flag when req.Thinking is set.
func TestAskThinking_Submit(t *testing.T) {
	m := &recordMux{answer: "my question"}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond}
	res, out, err := a.AskThinking(Request{Type: "text", Title: "Ask", Prompt: "What?"})
	if err != nil {
		t.Fatal(err)
	}
	// AskThinking leaves the temp dir for the async .done consumer; clean up here.
	if out != "" {
		defer os.RemoveAll(filepath.Dir(out))
	}
	if !res.Submitted {
		t.Fatal("expected Submitted=true")
	}
	if res.Value != "my question" {
		t.Fatalf("value = %q, want %q", res.Value, "my question")
	}
	if out == "" {
		t.Fatal("expected non-empty out path")
	}
	if !contains(m.floats[0], "--thinking") {
		t.Errorf("AskThinking should pass --thinking to the float\nargv: %v", m.floats[0])
	}
}

// TestAskThinking_Cancel verifies that a dismissed float (cancel marker written)
// results in Submitted=false without error.
func TestAskThinking_Cancel(t *testing.T) {
	m := &recordMux{cancel: true}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond, Timeout: 50 * time.Millisecond}
	res, out, err := a.AskThinking(Request{Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		defer os.RemoveAll(filepath.Dir(out))
	}
	if res.Submitted {
		t.Fatal("expected Submitted=false on cancel")
	}
}

// TestAskThinking_SpawnError verifies that a mux spawn failure is propagated as
// an error (and the temp dir is cleaned up by AskThinking).
func TestAskThinking_SpawnError(t *testing.T) {
	m := &recordMux{spawnErr: errors.New("pane spawn failed")}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond}
	_, _, err := a.AskThinking(Request{Type: "text"})
	if err == nil {
		t.Fatal("expected error from spawn failure, got nil")
	}
}

// ---------------------------------------------------------------------------
// Ask spawn-error path
// ---------------------------------------------------------------------------

// TestAsk_SpawnError verifies that Ask propagates a mux spawn failure.
func TestAsk_SpawnError(t *testing.T) {
	m := &recordMux{spawnErr: errors.New("pane spawn failed")}
	a := Asker{SelfExe: "/path/ai-playbook", Mux: m, poll: time.Millisecond}
	_, err := a.Ask(Request{Type: "text"})
	if err == nil {
		t.Fatal("expected error from spawn failure, got nil")
	}
}

// ---------------------------------------------------------------------------
// buildCmd --thinking flag
// ---------------------------------------------------------------------------

// TestBuildCmd_ThinkingFlag verifies the --thinking flag: present for text
// requests with Thinking=true, absent for non-text types regardless of the
// field value.
func TestBuildCmd_ThinkingFlag(t *testing.T) {
	a := Asker{SelfExe: "/path/ai-playbook"}
	cmd := a.buildCmd(Request{Type: "text", Thinking: true}, "/tmp/out")
	if !contains(cmd, "--thinking") {
		t.Errorf("buildCmd(text, Thinking=true) missing --thinking\ncmd: %v", cmd)
	}
	// Non-text type must NOT receive --thinking even when the field is set.
	cmd2 := a.buildCmd(Request{Type: "choose", Thinking: true, Choices: []string{"a"}}, "/tmp/out")
	if contains(cmd2, "--thinking") {
		t.Errorf("buildCmd(choose, Thinking=true) must not pass --thinking\ncmd: %v", cmd2)
	}
}

// ---------------------------------------------------------------------------
// poll_ timeout path (no file, no cancel marker)
// ---------------------------------------------------------------------------

// TestPoll_TimeoutWithoutFile drives poll_ to its timeout exit: neither the
// answer file nor the cancel marker is written, so the loop exhausts the
// deadline and returns Submitted=false. Uses a short poll+timeout so the test
// runs in a few milliseconds.
func TestPoll_TimeoutWithoutFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "floatinput-poll-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "answer")
	a := Asker{poll: time.Millisecond, Timeout: 5 * time.Millisecond}
	res := a.poll_(out)
	if res.Submitted {
		t.Fatal("expected Submitted=false after timeout with no file")
	}
}

// TestPoll_DefaultPollInterval exercises the default-interval branch
// (a.poll == 0 → interval = pollInterval). Writing the cancel file before
// calling poll_ ensures the loop exits on the first iteration so the test
// completes without sleeping through a full pollInterval (100 ms).
func TestPoll_DefaultPollInterval(t *testing.T) {
	dir, err := os.MkdirTemp("", "floatinput-poll-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "answer")
	// Pre-write the cancel marker so the loop returns immediately.
	if err := os.WriteFile(out+cancelSuffix, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := Asker{poll: 0, Timeout: time.Second} // poll=0 triggers interval=pollInterval
	res := a.poll_(out)
	if res.Submitted {
		t.Fatal("expected Submitted=false")
	}
}

// TestPoll_DefaultTimeout exercises the default-timeout branch
// (a.Timeout == 0 → timeout = defaultTimeout). Pre-writing the cancel file
// keeps the test instant despite the 30-minute default deadline.
func TestPoll_DefaultTimeout(t *testing.T) {
	dir, err := os.MkdirTemp("", "floatinput-poll-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "answer")
	if err := os.WriteFile(out+cancelSuffix, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := Asker{poll: time.Millisecond, Timeout: 0} // Timeout=0 triggers defaultTimeout
	res := a.poll_(out)
	if res.Submitted {
		t.Fatal("expected Submitted=false")
	}
}

// ---------------------------------------------------------------------------
// measureHeight success path
// ---------------------------------------------------------------------------

// TestMeasureHeight_ValidOutput exercises the success return of measureHeight
// (the path skipped when SelfExe is not a real binary). A minimal shell script
// that echoes a valid integer is used as a stand-in for the ai-playbook binary.
func TestMeasureHeight_ValidOutput(t *testing.T) {
	dir, err := os.MkdirTemp("", "floatinput-measure-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Write a tiny helper that outputs a valid height and exits 0.
	script := filepath.Join(dir, "fake-playbook")
	const scriptBody = "#!/bin/sh\necho 15\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}

	a := Asker{SelfExe: script}
	h := a.measureHeight(Request{Type: "text"})
	if h != 15 {
		t.Errorf("measureHeight = %d, want 15", h)
	}
}

// TestMeasureHeight_ZeroOutputFallsBack verifies that an output of "0" (or
// any non-positive integer) triggers the fallback height.
func TestMeasureHeight_ZeroOutputFallsBack(t *testing.T) {
	dir, err := os.MkdirTemp("", "floatinput-measure-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	script := filepath.Join(dir, "fake-playbook-zero")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := Asker{SelfExe: script}
	h := a.measureHeight(Request{Type: "text"})
	if h != fallbackHeight {
		t.Errorf("measureHeight with output=0 = %d, want fallbackHeight %d", h, fallbackHeight)
	}
}
