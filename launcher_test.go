package main

import (
	"os"
	"strings"
	"testing"

	"ai-playbook/capture"
	"ai-playbook/mux"
)

// launchMux is a recording fake Mux for the launcher topology tests. SpawnFloat
// simulates the floated `input --out <file>` by writing answer to that file (so
// the launcher's poll observes a submit, or — with floatCancel — a cancel).
// SpawnDocked records the docked-pane argv + cwd and snapshots the request-JSON
// file's contents (the launcher→session context hand-off) before the session
// would consume it.
type launchMux struct {
	floats      [][]string
	docked      [][]string
	dockedCwd   string
	dockedReq   string // contents of the --request file at spawn time
	answer      string
	floatCancel bool
}

func (m *launchMux) DumpScreen(string) (string, error) { return "", nil }
func (m *launchMux) SpawnPane(mux.SpawnOptions) error  { return nil }
func (m *launchMux) TypeInto(string, string) error     { return nil }
func (m *launchMux) SpawnFloat(mux.SpawnOptions) error { return nil }

// SpawnInputFloat is the launcher's request-float seam (Asker.Ask now spawns the
// borderless input float through it). It records the argv and simulates the
// floated `input --out <file>` writing the submitted value (or cancel marker).
func (m *launchMux) SpawnInputFloat(opts mux.SpawnOptions) error {
	m.floats = append(m.floats, opts.Cmd)
	for i, a := range opts.Cmd {
		if a == "--out" && i+1 < len(opts.Cmd) {
			if m.floatCancel {
				// Simulate the float writing the cancel marker on dismiss.
				_ = os.WriteFile(opts.Cmd[i+1]+".cancel", nil, 0o600)
			} else {
				_ = os.WriteFile(opts.Cmd[i+1], []byte(m.answer), 0o600)
			}
		}
	}
	return nil
}

func (m *launchMux) SpawnDocked(opts mux.SpawnOptions) error {
	m.docked = append(m.docked, opts.Cmd)
	m.dockedCwd = opts.Cwd
	// Snapshot the request file the launcher wrote (the session reads + removes it).
	for i, a := range opts.Cmd {
		if a == "--request" && i+1 < len(opts.Cmd) {
			if b, err := os.ReadFile(opts.Cmd[i+1]); err == nil {
				m.dockedReq = string(b)
			}
		}
	}
	return nil
}

// TestLaunch_FloatThenDocked asserts the topology: the launcher spawns the input
// float with the right `ai-playbook input` command (prefilled), reads the
// submitted request from the out-file, then spawns the docked pane with the right
// `ai-playbook session --request <json>` command carrying the captured context +
// the submitted request.
func TestLaunch_FloatThenDocked(t *testing.T) {
	m := &launchMux{answer: "please fix it"}
	req := capture.Request{
		Kind:        "error",
		Command:     "gg build",
		Exit:        "1",
		CWD:         "/proj/dir",
		ProjectRoot: "/proj",
		Project:     capture.Project{Name: "proj"},
	}

	if code := launch(m, "/bin/ai-playbook", req); code != 0 {
		t.Fatalf("launch exit = %d, want 0", code)
	}

	// 1) One input float, prefilled with the error template.
	if len(m.floats) != 1 {
		t.Fatalf("expected 1 SpawnFloat, got %d", len(m.floats))
	}
	fargv := m.floats[0]
	if fargv[0] != "/bin/ai-playbook" || fargv[1] != "input" {
		t.Fatalf("float argv prefix = %v, want [/bin/ai-playbook input …]", fargv[:2])
	}
	prefill := argAfter(fargv, "--value")
	if !strings.Contains(prefill, "gg build") || !strings.Contains(prefill, "exit 1") {
		t.Errorf("float --value (prefill) = %q, want the error template", prefill)
	}
	// The request float carries --history <data-root>/request-history.jsonl so it
	// recalls + appends. The ask/`f` floats must NOT (asserted separately).
	if got, want := argAfter(fargv, "--history"), requestHistoryPath(); got != want {
		t.Errorf("float --history = %q, want %q", got, want)
	}

	// 2) One docked session pane, carrying the context + submitted request.
	if len(m.docked) != 1 {
		t.Fatalf("expected 1 SpawnDocked, got %d", len(m.docked))
	}
	dargv := m.docked[0]
	if dargv[0] != "/bin/ai-playbook" || dargv[1] != "session" {
		t.Fatalf("docked argv prefix = %v, want [/bin/ai-playbook session …]", dargv[:2])
	}
	if argAfter(dargv, "--request") == "" {
		t.Errorf("docked pane missing --request <json>\nargv: %v", dargv)
	}
	if m.dockedCwd != "/proj" {
		t.Errorf("docked cwd = %q, want project root /proj", m.dockedCwd)
	}
	// The request JSON carries the captured context AND the user's submitted request.
	if !strings.Contains(m.dockedReq, "gg build") {
		t.Errorf("docked request JSON missing captured command:\n%s", m.dockedReq)
	}
	if !strings.Contains(m.dockedReq, "please fix it") {
		t.Errorf("docked request JSON missing submitted request:\n%s", m.dockedReq)
	}
}

// TestLaunch_CancelNoSession asserts that cancelling the request float exits
// cleanly (0) and spawns NO docked session pane.
func TestLaunch_CancelNoSession(t *testing.T) {
	m := &launchMux{floatCancel: true}
	code := launch(m, "/bin/ai-playbook", capture.Request{CWD: "/x"})
	if code != 0 {
		t.Fatalf("cancelled launch exit = %d, want 0", code)
	}
	if len(m.docked) != 0 {
		t.Fatalf("cancel should spawn no docked pane, got %d", len(m.docked))
	}
}

// TestReadRequestJSON_RoundTrip asserts requestJSON → readRequestJSON preserves
// the launcher→session context fields.
func TestReadRequestJSON_RoundTrip(t *testing.T) {
	in := capture.Request{
		Kind:        "error",
		Command:     "make",
		Exit:        "2",
		DurationMs:  "1500",
		CWD:         "/c",
		ProjectRoot: "/r",
		PaneID:      "terminal_3",
		Scrollback:  "boom",
		UserRequest: "fix make",
		Project:     capture.Project{Name: "r", Branch: "main"},
	}
	f, err := os.CreateTemp(t.TempDir(), "req-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(requestJSON(in)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := readRequestJSON(f.Name())
	if err != nil {
		t.Fatalf("readRequestJSON: %v", err)
	}
	if got != in {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}

// TestPrefillTemplate ports assist::prefill_template's behavior.
func TestPrefillTemplate(t *testing.T) {
	errReq := capture.Request{Kind: "error", Command: "gg build", Exit: "1", Project: capture.Project{Name: "app"}}
	if got, want := prefillTemplate(errReq), "Diagnose and fix why `gg build` failed (exit 1) in app"; got != want {
		t.Errorf("error prefill = %q, want %q", got, want)
	}
	// A non-error (question) request has an empty prefill.
	if got := prefillTemplate(capture.Request{Kind: "question"}); got != "" {
		t.Errorf("question prefill = %q, want empty", got)
	}
	// Missing project name falls back to "this directory".
	got := prefillTemplate(capture.Request{Kind: "error", Command: "x", Exit: "3"})
	if !strings.Contains(got, "this directory") {
		t.Errorf("missing-project prefill = %q, want fallback 'this directory'", got)
	}
}

func argAfter(ss []string, key string) string {
	for i, s := range ss {
		if s == key && i+1 < len(ss) {
			return ss[i+1]
		}
	}
	return ""
}
