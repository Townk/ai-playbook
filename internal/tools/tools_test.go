package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/pkg/driver"
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

	res, err := Dial(socket, Call{Tool: "remember", Kind: "environment", Fact: "deploys via fly.io"})
	if err != nil {
		t.Fatalf("Dial remember: %v", err)
	}
	if !res.OK {
		t.Errorf("remember ok = %v, want true (err=%q)", res.OK, res.Error)
	}

	// The fact landed in the project KB, sectioned under ## Environment, under
	// the backend's data root.
	want := "<!-- meta: project-root: " + projectRoot + " -->\n\n## Environment\n- deploys via fly.io\n"
	if got := kb.LoadProject(root, projectRoot); string(got) != want {
		t.Errorf("KB contents = %q, want %q", got, want)
	}
}

func TestServe_RememberProjectOverride(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: "/default", KBRoot: root})

	// An explicit projectRoot in the call overrides Deps.ProjectRoot.
	if _, err := Dial(socket, Call{Tool: "remember", Kind: "environment", Fact: "uses bazel", ProjectRoot: "/other"}); err != nil {
		t.Fatalf("Dial remember override: %v", err)
	}
	want := "<!-- meta: project-root: /other -->\n\n## Environment\n- uses bazel\n"
	if got := kb.LoadProject(root, "/other"); string(got) != want {
		t.Errorf("override KB = %q, want %q", got, want)
	}
	if got := kb.LoadProject(root, "/default"); got != "" {
		t.Errorf("default KB should be empty, got %q", got)
	}
}

// TestServe_RememberKindValidation exercises the spec's kind validation matrix
// through the socket seam: each violation is a tool error (res.Error non-empty,
// res.OK false), not a written fact.
func TestServe_RememberKindValidation(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: "/p", KBRoot: root})

	cases := []struct {
		name    string
		call    Call
		wantErr string // substring the tool error must carry
	}{
		{"missing kind", Call{Tool: "remember", Fact: "x"}, "want one of: system, user, environment, topic"},
		{"unknown kind", Call{Tool: "remember", Kind: "bogus", Fact: "x"}, "want one of: system, user, environment, topic"},
		{"topic without kind=topic", Call{Tool: "remember", Kind: "environment", Topic: "db", Fact: "x"}, "only valid with kind=topic"},
		{"topic missing when kind=topic", Call{Tool: "remember", Kind: "topic", Fact: "x"}, "requires a topic"},
		{"projectRoot with global kind", Call{Tool: "remember", Kind: "system", ProjectRoot: "/p", Fact: "x"}, "projectRoot is only valid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Dial(socket, tc.call)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			if res.OK {
				t.Errorf("%s: ok = true, want a tool error", tc.name)
			}
			if !strings.Contains(res.Error, tc.wantErr) {
				t.Errorf("%s: error = %q, want it to contain %q", tc.name, res.Error, tc.wantErr)
			}
		})
	}
}

// TestServe_RememberRoutingAllKinds asserts each of the four kinds routes to the
// right file + section through the socket seam.
func TestServe_RememberRoutingAllKinds(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	projectRoot := "/routing/project"
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: projectRoot, KBRoot: root})

	send := func(call Call) {
		t.Helper()
		res, err := Dial(socket, call)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		if !res.OK {
			t.Fatalf("remember %+v: ok = false, err = %q", call, res.Error)
		}
	}

	send(Call{Tool: "remember", Kind: "system", Fact: "ripgrep is installed"})
	send(Call{Tool: "remember", Kind: "user", Fact: "prefers vim"})
	send(Call{Tool: "remember", Kind: "environment", Fact: "uses bazel"})
	send(Call{Tool: "remember", Kind: "topic", Topic: "Database", Fact: "pg needs PGPASSWORD"})

	global := string(kb.LoadGlobal(root))
	if !strings.Contains(global, "## System\n- ripgrep is installed") {
		t.Errorf("global KB missing system fact: %q", global)
	}
	if !strings.Contains(global, "## User\n- prefers vim") {
		t.Errorf("global KB missing user fact: %q", global)
	}

	project := string(kb.LoadProject(root, projectRoot))
	if !strings.Contains(project, "## Environment\n- uses bazel") {
		t.Errorf("project KB missing environment fact: %q", project)
	}
	if !strings.Contains(project, "## Topics\n### Database\n- pg needs PGPASSWORD") {
		t.Errorf("project KB missing topic fact: %q", project)
	}
}

// TestServe_RememberDedupIdempotent asserts a duplicate fact submitted twice
// through the socket is a silent no-op the second time (write-dedup).
func TestServe_RememberDedupIdempotent(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	projectRoot := "/dedup/project"
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: projectRoot, KBRoot: root})

	call := Call{Tool: "remember", Kind: "environment", Fact: "uses bazel"}
	for i := 0; i < 2; i++ {
		res, err := Dial(socket, call)
		if err != nil {
			t.Fatalf("Dial #%d: %v", i, err)
		}
		if !res.OK {
			t.Fatalf("Dial #%d: ok = false, err = %q", i, res.Error)
		}
	}

	want := "<!-- meta: project-root: " + projectRoot + " -->\n\n## Environment\n- uses bazel\n"
	if got := kb.LoadProject(root, projectRoot); string(got) != want {
		t.Errorf("KB contents after dup = %q, want %q (one bullet only)", got, want)
	}
}

// TestServe_RememberFlattensEmbeddedNewlines locks newline hygiene through the
// socket seam: a fact carrying embedded newlines must round-trip as ONE
// flattened bullet — never raw multi-line text whose continuation lines would
// parse back as un-prefixed (non-bullet) lines and corrupt the section.
func TestServe_RememberFlattensEmbeddedNewlines(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	projectRoot := "/newline/project"
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: projectRoot, KBRoot: root})

	res, err := Dial(socket, Call{Tool: "remember", Kind: "environment", Fact: "uses bazel\nfor all builds"})
	if err != nil {
		t.Fatalf("Dial remember: %v", err)
	}
	if !res.OK {
		t.Fatalf("remember ok = false, err = %q", res.Error)
	}
	want := "<!-- meta: project-root: " + projectRoot + " -->\n\n## Environment\n- uses bazel for all builds\n"
	if got := kb.LoadProject(root, projectRoot); string(got) != want {
		t.Fatalf("KB after multi-line fact = %q, want one flattened bullet %q", got, want)
	}

	// The file parses back cleanly: a follow-up write round-trips without
	// dropping or duplicating the flattened bullet.
	if res, err := Dial(socket, Call{Tool: "remember", Kind: "environment", Fact: "second fact"}); err != nil || !res.OK {
		t.Fatalf("second remember: err = %v, res = %+v", err, res)
	}
	want = "<!-- meta: project-root: " + projectRoot + " -->\n\n## Environment\n- uses bazel for all builds\n- second fact\n"
	if got := kb.LoadProject(root, projectRoot); string(got) != want {
		t.Errorf("KB after second write = %q, want %q", got, want)
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

func TestServe_SubmitPlaybookEmptyPayload(t *testing.T) {
	d := newTestDriver(t)
	called := false
	socket := serveTest(t, Deps{Driver: d, OnPlaybook: func(draft.Playbook) { called = true }})

	// No Playbook field → should be rejected before OnPlaybook is called.
	res, err := Dial(socket, Call{Tool: "submit_playbook"})
	if err != nil {
		t.Fatalf("Dial submit (empty): %v", err)
	}
	if !strings.Contains(res.Error, "requires") {
		t.Errorf("empty submit error = %q, want contains %q", res.Error, "requires")
	}
	if called {
		t.Error("OnPlaybook must not be called for an empty-payload submit")
	}
}

func TestServe_SubmitPlaybookNoHandler(t *testing.T) {
	d := newTestDriver(t)
	// No OnPlaybook wired → submit_playbook must return unavailable error.
	socket := serveTest(t, Deps{Driver: d})

	pb := draft.Playbook{
		Title:    "T",
		Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{{Kind: "code", Lang: "bash", Code: "echo hi", ID: "fix"}}}},
	}
	raw, _ := json.Marshal(pb)

	res, err := Dial(socket, Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("Dial submit (no handler): %v", err)
	}
	if res.OK {
		t.Error("submit without OnPlaybook must not return ok=true")
	}
	if res.Error == "" {
		t.Error("submit without OnPlaybook must return a non-empty error")
	}
}

func TestServe_SubmitPlaybook(t *testing.T) {
	d := newTestDriver(t)
	var got draft.Playbook
	gotN := 0
	socket := serveTest(t, Deps{Driver: d, OnPlaybook: func(pb draft.Playbook) { got = pb; gotN++ }})

	pb := draft.Playbook{
		Title:    "T",
		Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{{Kind: "code", Lang: "bash", Code: "echo hi", ID: "fix"}}}},
		Meta:     draft.Meta{Description: "d", ProjectBound: true},
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
	bad, _ := json.Marshal(draft.Playbook{Title: "T", Sections: []draft.Section{{Heading: "S"}}})
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

// TestServe_SubmitPlaybook_ValidateFileBlocksRejects asserts that when ValidateFileBlocks
// returns an error the handler returns reply.Error with that message and does NOT call
// OnPlaybook — the file= create-vs-edit gate fires before delivery.
func TestServe_SubmitPlaybook_ValidateFileBlocksRejects(t *testing.T) {
	d := newTestDriver(t)
	called := false
	fakeErr := errors.New(`file "main.go" already exists — use a diff block to edit an existing file (file= is for new files)`)
	socket := serveTest(t, Deps{
		Driver:     d,
		OnPlaybook: func(draft.Playbook) { called = true },
		ValidateFileBlocks: func(pb draft.Playbook) error {
			return fakeErr
		},
	})

	// A valid playbook with a file= block (lang required for the code block to pass Validate).
	pb := draft.Playbook{
		Title: "T",
		Sections: []draft.Section{{Heading: "S", Content: []draft.ContentItem{
			{Kind: "code", Lang: "go", Code: "package main", File: "main.go"},
		}}},
		Meta: draft.Meta{Description: "d", ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)

	res, err := Dial(socket, Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("Dial submit: %v", err)
	}
	if res.OK {
		t.Error("ValidateFileBlocks error must prevent ok=true")
	}
	if !strings.Contains(res.Error, "diff") {
		t.Errorf("reply.Error = %q, want message containing %q", res.Error, "diff")
	}
	if called {
		t.Error("OnPlaybook must not be called when ValidateFileBlocks returns an error")
	}
}
