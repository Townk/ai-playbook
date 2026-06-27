package mcpserver

// mcpserver_coverage_test.go — additional tests that raise the package
// coverage from ~42 % toward 90 %+.
//
// These tests exercise:
//   - renderResult: the missing branches (error path for non-ask tools, run
//     with stderr, "not saved", normal ask answer, default case)
//   - newServer: construction + tool registration
//   - runHandler / rememberHandler / askHandler: the returned closures
//
// The only function left un-covered is Run (line 46), which calls
// mcp.StdioTransport{} — a live stdin/stdout shim — and uses
// context.Background(), so it cannot be exercised as a unit without
// redirecting global I/O. The end-to-end test in
// cmd/ai-playbook/mcp_e2e_test.go covers the full round-trip.

import (
	"context"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/tools"
)

// ---------------------------------------------------------------------------
// renderResult — missing branches
// ---------------------------------------------------------------------------

// TestRenderResult_ErrorNonAsk covers the early-return error path when the
// backend result carries an error and the tool is not "ask".
func TestRenderResult_ErrorNonAsk(t *testing.T) {
	got := renderResult("run", tools.Result{Error: "backend panic"})
	want := "error: backend panic"
	if got != want {
		t.Errorf("renderResult error (run) = %q, want %q", got, want)
	}
}

// TestRenderResult_RememberError covers the same guard for the remember tool.
func TestRenderResult_RememberError(t *testing.T) {
	got := renderResult("remember", tools.Result{Error: "disk full"})
	if got != "error: disk full" {
		t.Errorf("renderResult error (remember) = %q, want %q", got, "error: disk full")
	}
}

// TestRenderResult_RunWithStderr covers the stderr branch inside the "run"
// case (res.Err != "" while res.Out == "").
func TestRenderResult_RunWithStderr(t *testing.T) {
	got := renderResult("run", tools.Result{Exit: 2, Err: "build failed"})
	if !strings.Contains(got, "exit: 2") {
		t.Errorf("run result %q missing exit code", got)
	}
	if !strings.Contains(got, "build failed") {
		t.Errorf("run result %q missing stderr content", got)
	}
	if strings.Contains(got, "--- stdout ---") {
		t.Errorf("run result %q should not contain stdout section when Out is empty", got)
	}
}

// TestRenderResult_RunWithBoth covers having both stdout and stderr.
func TestRenderResult_RunWithBoth(t *testing.T) {
	got := renderResult("run", tools.Result{Exit: 1, Out: "partial output", Err: "then failed"})
	if !strings.Contains(got, "--- stdout ---") || !strings.Contains(got, "partial output") {
		t.Errorf("run result %q missing stdout section", got)
	}
	if !strings.Contains(got, "--- stderr ---") || !strings.Contains(got, "then failed") {
		t.Errorf("run result %q missing stderr section", got)
	}
}

// TestRenderResult_RememberNotSaved covers the "not saved" branch (OK false).
func TestRenderResult_RememberNotSaved(t *testing.T) {
	got := renderResult("remember", tools.Result{OK: false})
	if got != "not saved" {
		t.Errorf("renderResult remember not-saved = %q, want %q", got, "not saved")
	}
}

// TestRenderResult_AskAnswer covers the normal ask path (Unavailable false),
// which returns the answer string directly.
func TestRenderResult_AskAnswer(t *testing.T) {
	got := renderResult("ask", tools.Result{Answer: "production"})
	if got != "production" {
		t.Errorf("renderResult ask answer = %q, want %q", got, "production")
	}
}

// TestRenderResult_Default covers the default switch case for an unrecognised
// tool name, which formats the Result with %%+v.
func TestRenderResult_Default(t *testing.T) {
	got := renderResult("unknown-tool", tools.Result{Exit: 99})
	if !strings.Contains(got, "99") {
		t.Errorf("renderResult default = %q, want the Exit value (99)", got)
	}
}

// ---------------------------------------------------------------------------
// newServer — construction
// ---------------------------------------------------------------------------

// TestNewServer verifies that newServer returns a non-nil MCP server and
// exercises the full tool-registration code path without taking over stdio.
func TestNewServer(t *testing.T) {
	srv := newServer("/tmp/unused-for-mcpserver-test.sock")
	if srv == nil {
		t.Fatal("newServer returned nil")
	}
}

// ---------------------------------------------------------------------------
// Handler closure coverage
// ---------------------------------------------------------------------------

// TestRunHandler_ForwardsToBackend exercises the closure returned by
// runHandler: the typed runInput is translated into a tools.Call that
// reaches the fake backend with the correct fields.
func TestRunHandler_ForwardsToBackend(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{Out: "handler ok", Exit: 0})
	h := runHandler(fb.socket)

	res, _, err := h(context.Background(), nil, runInput{Cmd: "echo hello", ID: "myid"})
	if err != nil {
		t.Fatalf("runHandler: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError: %+v", res)
	}

	got := fb.lastCall()
	if got.Tool != "run" || got.Cmd != "echo hello" || got.ID != "myid" {
		t.Errorf("backend got %+v, want run/echo hello/myid", got)
	}

	text := contentText(t, res)
	if !strings.Contains(text, "exit: 0") || !strings.Contains(text, "handler ok") {
		t.Errorf("runHandler text = %q, want exit code + stdout", text)
	}
}

// TestRememberHandler_ForwardsToBackend exercises the closure returned by
// rememberHandler: fact and optional projectRoot are forwarded verbatim.
func TestRememberHandler_ForwardsToBackend(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{OK: true})
	h := rememberHandler(fb.socket)

	res, _, err := h(context.Background(), nil, rememberInput{Fact: "uses bazel", ProjectRoot: "/proj"})
	if err != nil {
		t.Fatalf("rememberHandler: %v", err)
	}

	got := fb.lastCall()
	if got.Tool != "remember" || got.Fact != "uses bazel" || got.ProjectRoot != "/proj" {
		t.Errorf("backend got %+v, want remember/uses bazel//proj", got)
	}
	if text := contentText(t, res); text != "saved" {
		t.Errorf("rememberHandler text = %q, want %q", text, "saved")
	}
}

// TestAskHandler_ForwardsToBackend exercises the closure returned by
// askHandler: prompt and type are forwarded, and a normal (non-sentinel)
// answer is returned.
func TestAskHandler_ForwardsToBackend(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{Answer: "production"})
	h := askHandler(fb.socket)

	res, _, err := h(context.Background(), nil, askInput{Prompt: "which env?", Type: "line"})
	if err != nil {
		t.Fatalf("askHandler: %v", err)
	}

	got := fb.lastCall()
	if got.Tool != "ask" || got.Prompt != "which env?" || got.Type != "line" {
		t.Errorf("backend got %+v, want ask/which env?/line", got)
	}
	if text := contentText(t, res); text != "production" {
		t.Errorf("askHandler text = %q, want %q", text, "production")
	}
}

// TestForward_AskNormalAnswer covers the renderResult ask branch for a
// submitted (non-sentinel) answer flowing through forward itself.
func TestForward_AskNormalAnswer(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{Answer: "staging"})

	res, err := forward(fb.socket, tools.Call{Tool: "ask", Prompt: "env?"})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError for a healthy ask: %+v", res)
	}
	if text := contentText(t, res); text != "staging" {
		t.Errorf("ask answer = %q, want %q", text, "staging")
	}
}
