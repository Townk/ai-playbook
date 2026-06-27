package launcher

import (
	"io"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/mux"
)

// TestLauncherRoute asserts the routing predicate:
//   - null mux → false (never float, regardless of request)
//   - real mux + empty request → true (use the float)
//   - real mux + explicit request → false (skip the float, request already known)
func TestLauncherRoute(t *testing.T) {
	nullM := mux.Null()
	realM := &launchMux{} // non-null: mux.IsNull returns false for it

	if launcherRoute(nullM, "") {
		t.Error("null mux + empty request: want false (inline, not float)")
	}
	if launcherRoute(nullM, "some request") {
		t.Error("null mux + explicit request: want false")
	}
	if !launcherRoute(realM, "") {
		t.Error("real mux + empty request: want true (use float)")
	}
	if launcherRoute(realM, "some request") {
		t.Error("real mux + explicit request: want false")
	}
}

// TestReadRequestStdin_ReadsLine asserts a plain line is returned trimmed, ok=true.
func TestReadRequestStdin_ReadsLine(t *testing.T) {
	in := strings.NewReader("fix the build\n")
	s, ok := readRequestStdin(in, io.Discard)
	if !ok {
		t.Fatal("expected ok=true, got false")
	}
	if s != "fix the build" {
		t.Errorf("got %q, want %q", s, "fix the build")
	}
}

// TestReadRequestStdin_WhitespaceEmpty asserts whitespace-only and blank lines
// return ok=false (nothing useful to submit).
func TestReadRequestStdin_WhitespaceEmpty(t *testing.T) {
	for _, input := range []string{"   \n", "\t\n", "\n"} {
		in := strings.NewReader(input)
		_, ok := readRequestStdin(in, io.Discard)
		if ok {
			t.Errorf("whitespace-only input %q: want ok=false", input)
		}
	}
}

// TestReadRequestStdin_EOF asserts EOF (no input) returns ok=false.
func TestReadRequestStdin_EOF(t *testing.T) {
	in := strings.NewReader("")
	_, ok := readRequestStdin(in, io.Discard)
	if ok {
		t.Error("EOF: want ok=false")
	}
}

// TestStdinAsk_ReadsAnswer asserts the returned AskFunc prints the prompt and reads
// the answer line, returning it as a submitted Result.
func TestStdinAsk_ReadsAnswer(t *testing.T) {
	in := strings.NewReader("the answer\n")
	ask := stdinAsk(in, io.Discard)
	res, err := ask(floatinput.Request{Prompt: "What do you want?"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Submitted {
		t.Error("expected Submitted=true")
	}
	if res.Value != "the answer" {
		t.Errorf("got Value=%q, want %q", res.Value, "the answer")
	}
}

// TestStdinAsk_EOFError asserts EOF before any answer returns a non-nil error
// (the agent can't get an answer; the caller handles the error).
func TestStdinAsk_EOFError(t *testing.T) {
	in := strings.NewReader("")
	ask := stdinAsk(in, io.Discard)
	_, err := ask(floatinput.Request{Prompt: "?"})
	if err == nil {
		t.Error("EOF: want non-nil error")
	}
}
