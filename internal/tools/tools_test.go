package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/internal/playbook"
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
	// Pin zsh: zsh-specific fixture; the default now honors $SHELL (bash on CI).
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
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

	// run with an id exports APB_OUT_<id>; a later run on the SAME driver sees it
	// (the backend uses one shared session shell), proving value-passing rides the
	// shared driver.
	if _, err := Dial(socket, Call{Tool: "run", ID: "first", Cmd: "print -r -- carried"}); err != nil {
		t.Fatalf("Dial run first: %v", err)
	}
	res, err := Dial(socket, Call{Tool: "run", Cmd: "print -r -- $APB_OUT_first"})
	if err != nil {
		t.Fatalf("Dial run second: %v", err)
	}
	if res.Out != "carried" {
		t.Errorf("value-passing: APB_OUT_first = %q, want %q", res.Out, "carried")
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

func TestServe_SubmitPlaybook(t *testing.T) {
	d := newTestDriver(t)
	var got playbook.Playbook
	gotN := 0
	socket := serveTest(t, Deps{Driver: d, OnPlaybook: func(pb playbook.Playbook) { got = pb; gotN++ }})

	pb := playbook.Playbook{
		Title:    "T",
		Sections: []playbook.Section{{Heading: "S", Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", Code: "echo hi", ID: "fix"}}}},
		Meta:     playbook.Meta{Description: "d", ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)

	res, err := Dial(socket, Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("Dial submit: %v", err)
	}
	if !res.OK || res.Error != "" {
		t.Fatalf("submit reply = %+v, want ok", res)
	}
	if gotN != 1 || got.Title != "T" || !got.Meta.ProjectBound {
		t.Fatalf("OnPlaybook got %d calls, pb=%+v", gotN, got)
	}

	// An invalid playbook (no runnable block) is rejected and NOT delivered.
	bad, _ := json.Marshal(playbook.Playbook{Title: "T", Sections: []playbook.Section{{Heading: "S"}}})
	res, err = Dial(socket, Call{Tool: "submit_playbook", Playbook: bad})
	if err != nil {
		t.Fatalf("Dial bad submit: %v", err)
	}
	if res.OK || res.Error == "" {
		t.Fatalf("bad submit should be rejected, got %+v", res)
	}
	if gotN != 1 {
		t.Fatalf("invalid submit must not call OnPlaybook (calls=%d)", gotN)
	}
}
