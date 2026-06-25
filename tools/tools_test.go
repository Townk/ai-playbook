package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/driver"
	"ai-playbook/floatinput"
	"ai-playbook/kb"
)

// newTestDriver opens a real driver against a minimal controlled ZDOTDIR (no
// p10k/mise) so the tools backend's run RPC executes deterministically without
// touching the user's real rc.
func newTestDriver(t *testing.T) *driver.Driver {
	t.Helper()
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("# minimal rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// serveTest starts a tools backend on a temp socket against deps and returns the
// socket path; the server is closed on cleanup.
func serveTest(t *testing.T, deps Deps) string {
	t.Helper()
	// A short socket path: unix sun_path is ~104 bytes on darwin, and a nested
	// t.TempDir() path can overflow it. MkdirTemp under the OS temp root stays short.
	dir, err := os.MkdirTemp("", "tsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "t.sock")
	srv, err := Serve(socket, deps)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return socket
}

func TestServe_Run(t *testing.T) {
	d := newTestDriver(t)
	socket := serveTest(t, Deps{Driver: d})

	// A successful command: stdout captured, exit 0.
	res, err := Dial(socket, Call{Tool: "run", Cmd: "print -r -- hi"})
	if err != nil {
		t.Fatalf("Dial run: %v", err)
	}
	if res.Out != "hi" {
		t.Errorf("run out = %q, want %q", res.Out, "hi")
	}
	if res.Exit != 0 {
		t.Errorf("run exit = %d, want 0", res.Exit)
	}

	// A failing command: the exit code is forwarded faithfully.
	res, err = Dial(socket, Call{Tool: "run", Cmd: "(exit 3)"})
	if err != nil {
		t.Fatalf("Dial run (exit 3): %v", err)
	}
	if res.Exit != 3 {
		t.Errorf("run (exit 3) exit = %d, want 3", res.Exit)
	}
}

func TestServe_RunValuePassing(t *testing.T) {
	d := newTestDriver(t)
	socket := serveTest(t, Deps{Driver: d})

	// run with an id exports AAS_OUT_<id>; a later run on the SAME driver sees it
	// (the backend uses one shared session shell), proving value-passing rides the
	// shared driver.
	if _, err := Dial(socket, Call{Tool: "run", ID: "first", Cmd: "print -r -- carried"}); err != nil {
		t.Fatalf("Dial run first: %v", err)
	}
	res, err := Dial(socket, Call{Tool: "run", Cmd: "print -r -- $AAS_OUT_first"})
	if err != nil {
		t.Fatalf("Dial run second: %v", err)
	}
	if res.Out != "carried" {
		t.Errorf("value-passing: AAS_OUT_first = %q, want %q", res.Out, "carried")
	}
}

func TestServe_Remember(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	projectRoot := "/some/project"
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: projectRoot, KBRoot: root})

	res, err := Dial(socket, Call{Tool: "remember", Fact: "deploys via fly.io"})
	if err != nil {
		t.Fatalf("Dial remember: %v", err)
	}
	if !res.OK {
		t.Errorf("remember ok = %v, want true (err=%q)", res.OK, res.Error)
	}

	// The fact landed in the project KB under the backend's data root.
	got := kb.LoadFrom(root, projectRoot)
	if string(got) != "- deploys via fly.io\n" {
		t.Errorf("KB contents = %q, want %q", got, "- deploys via fly.io\n")
	}
}

func TestServe_RememberProjectOverride(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: "/default", KBRoot: root})

	// An explicit projectRoot in the call overrides Deps.ProjectRoot.
	if _, err := Dial(socket, Call{Tool: "remember", Fact: "uses bazel", ProjectRoot: "/other"}); err != nil {
		t.Fatalf("Dial remember override: %v", err)
	}
	if got := kb.LoadFrom(root, "/other"); string(got) != "- uses bazel\n" {
		t.Errorf("override KB = %q, want %q", got, "- uses bazel\n")
	}
	if got := kb.LoadFrom(root, "/default"); got != "" {
		t.Errorf("default KB should be empty, got %q", got)
	}
}

func TestServe_AskSentinel(t *testing.T) {
	d := newTestDriver(t)
	socket := serveTest(t, Deps{Driver: d})

	res, err := Dial(socket, Call{Tool: "ask", Prompt: "which env?", Type: "line"})
	if err != nil {
		t.Fatalf("Dial ask: %v", err)
	}
	if !res.Unavailable {
		t.Errorf("ask unavailable = %v, want true", res.Unavailable)
	}
	if res.Error != askUnavailableMsg {
		t.Errorf("ask error = %q, want sentinel %q", res.Error, askUnavailableMsg)
	}
}

// TestServe_AskFloat asserts that, with an Ask seam wired, the `ask` tool drives
// the float (here a fake that records the request and returns a canned answer)
// and returns the user's submitted answer over the socket.
func TestServe_AskFloat(t *testing.T) {
	d := newTestDriver(t)
	var gotReq floatinput.Request
	ask := func(req floatinput.Request) (floatinput.Result, error) {
		gotReq = req
		return floatinput.Result{Value: "production", Submitted: true}, nil
	}
	socket := serveTest(t, Deps{Driver: d, Cwd: "/proj", Ask: ask})

	res, err := Dial(socket, Call{Tool: "ask", Prompt: "which env?", Type: "line"})
	if err != nil {
		t.Fatalf("Dial ask: %v", err)
	}
	if res.Unavailable {
		t.Errorf("ask should be available with an Ask seam (err=%q)", res.Error)
	}
	if res.Answer != "production" {
		t.Errorf("ask answer = %q, want %q", res.Answer, "production")
	}
	// The float request carried the agent's prompt/type and the session cwd.
	if gotReq.Prompt != "which env?" || gotReq.Type != "line" || gotReq.Cwd != "/proj" {
		t.Errorf("float request = %+v, want prompt/type/cwd from the ask call + session", gotReq)
	}
}

// TestServe_AskFloatCancel asserts a cancelled float (Submitted=false) returns
// the unavailable sentinel so the agent gets a definite, non-hanging answer.
func TestServe_AskFloatCancel(t *testing.T) {
	d := newTestDriver(t)
	ask := func(floatinput.Request) (floatinput.Result, error) {
		return floatinput.Result{Submitted: false}, nil
	}
	socket := serveTest(t, Deps{Driver: d, Ask: ask})

	res, err := Dial(socket, Call{Tool: "ask", Prompt: "?"})
	if err != nil {
		t.Fatalf("Dial ask: %v", err)
	}
	if !res.Unavailable || res.Error != askUnavailableMsg {
		t.Errorf("cancelled ask = %+v, want unavailable sentinel", res)
	}
}

func TestServe_UnknownTool(t *testing.T) {
	d := newTestDriver(t)
	socket := serveTest(t, Deps{Driver: d})

	res, err := Dial(socket, Call{Tool: "bogus"})
	if err != nil {
		t.Fatalf("Dial bogus: %v", err)
	}
	if res.Error == "" {
		t.Errorf("unknown tool should return an error reply, got %+v", res)
	}
}

func TestServe_NilDriver(t *testing.T) {
	if _, err := Serve(filepath.Join(t.TempDir(), "x.sock"), Deps{}); err == nil {
		t.Errorf("Serve with nil driver should error")
	}
}

// TestOnActivity verifies each tool call invokes the OnActivity hook with the
// expected SHORT summary at the START of the handler (issue #2): run → "run: <cmd>",
// ask → "ask: <prompt>", remember → "remember: noted".
func TestOnActivity(t *testing.T) {
	d := newTestDriver(t)
	got := make(chan string, 8)
	deps := Deps{
		Driver: d,
		KBRoot: t.TempDir(),
		Ask: func(floatinput.Request) (floatinput.Result, error) {
			return floatinput.Result{Submitted: true, Value: "ok"}, nil
		},
		OnActivity: func(s string) { got <- s },
	}
	socket := serveTest(t, deps)

	recv := func() string {
		select {
		case s := <-got:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("OnActivity not called within timeout")
			return ""
		}
	}

	if _, err := Dial(socket, Call{Tool: "run", Cmd: "print -r -- hi"}); err != nil {
		t.Fatalf("Dial run: %v", err)
	}
	if s := recv(); s != "run: print -r -- hi" {
		t.Errorf("run activity = %q, want %q", s, "run: print -r -- hi")
	}

	if _, err := Dial(socket, Call{Tool: "ask", Prompt: "which env?"}); err != nil {
		t.Fatalf("Dial ask: %v", err)
	}
	if s := recv(); s != "ask: which env?" {
		t.Errorf("ask activity = %q, want %q", s, "ask: which env?")
	}

	if _, err := Dial(socket, Call{Tool: "remember", Fact: "deploys via fly"}); err != nil {
		t.Fatalf("Dial remember: %v", err)
	}
	if s := recv(); s != "remember: noted" {
		t.Errorf("remember activity = %q, want %q", s, "remember: noted")
	}
}

// TestOnActivityTruncates verifies a long, multi-line run command is collapsed to
// one line and capped (with an ellipsis) in the activity summary.
func TestOnActivityTruncates(t *testing.T) {
	d := newTestDriver(t)
	got := make(chan string, 1)
	socket := serveTest(t, Deps{Driver: d, OnActivity: func(s string) { got <- s }})

	long := "echo " + strings.Repeat("x", 200) + "\nsecond line"
	if _, err := Dial(socket, Call{Tool: "run", Cmd: long}); err != nil {
		t.Fatalf("Dial run: %v", err)
	}
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "run: ") {
			t.Errorf("summary must keep the run: prefix, got %q", s)
		}
		if !strings.HasSuffix(s, "…") {
			t.Errorf("over-long summary must end with an ellipsis, got %q", s)
		}
		if strings.Contains(s, "\n") {
			t.Errorf("summary must be a single line, got %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnActivity not called")
	}
}
