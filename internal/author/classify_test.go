package author

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
)

// sampleClassifyRequest is a captured request with a typed user ask and a failed
// command, so the prompt/guard paths have real context to exercise.
func sampleClassifyRequest() capture.Request {
	return capture.Request{
		Kind:        "question",
		Command:     "git lg",
		Exit:        "0",
		CWD:         "/Users/me/proj",
		ProjectRoot: "/Users/me/proj",
		Scrollback:  "",
		UserRequest: "How to list the last 3 commits of last week?",
		Project:     capture.Project{Name: "proj", Branch: "main"},
	}
}

// ClassifyPrompt must carry the JSON-only directive, the three kinds + their
// content rules, the "never the failed command" rule, and the request + context.
func TestClassifyPrompt(t *testing.T) {
	req := sampleClassifyRequest()
	p := ClassifyPrompt(req)

	wants := []string{
		"JSON",      // it's a JSON call
		"ONLY",      // JSON-only directive
		"fence",     // no markdown fence directive
		`"kind"`,    // schema key
		`"content"`, // schema key
		`"title"`,   // schema key (the ≤30-char pane-header label)
		"30",        // the title length bound
		"Title Case",
		"command", // the three kinds
		"answer",
		"escalate",
		"SINGLE command",         // command content rule
		"prose answer",           // answer content rule
		"PLAYBOOK",               // escalate content rule
		req.UserRequest,          // the user's ask
		"Project: proj",          // context
		"Last command: `git lg`", // context: the command
		"Working directory: /Users/me/proj",
	}
	for _, w := range wants {
		if !strings.Contains(p, w) {
			t.Errorf("ClassifyPrompt missing %q\n--- prompt ---\n%s", w, p)
		}
	}
	// The failed-command guard clause is FAILURE-only: this sample is a successful
	// question (exit 0), so it must NOT appear; a failure req must include it.
	if strings.Contains(p, "NEVER return the FAILED") {
		t.Errorf("question prompt must omit the failed-command clause")
	}
	fail := sampleClassifyRequest()
	fail.Exit = "1"
	if !strings.Contains(ClassifyPrompt(fail), "NEVER return the FAILED") {
		t.Errorf("failure prompt must include the failed-command clause")
	}
}

// Regression: a SUCCESSFUL last command (a plain question, exit 0) whose suggested
// command equals that last command must STAY a command — the guard is failure-only.
// (Was the "ask the same question twice → escalate → nothing at the prompt" bug.)
func TestClassifyRequest_SuccessCommandNotDowngraded(t *testing.T) {
	req := sampleClassifyRequest() // Kind question, Exit "0"
	req.Command = "git log --since='7 days ago' -n 3 --oneline"
	const out = `{"kind":"command","content":"git log --since='7 days ago' -n 3 --oneline"}`
	cls, err, _ := runClassify(t, req, out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if cls.Kind != KindCommand {
		t.Errorf("kind = %q, want command (success command must not downgrade)", cls.Kind)
	}
	if cls.Content == "" {
		t.Errorf("command content dropped; want the suggested command")
	}
}

// A classify response carrying an over-long title is trimmed + truncated to 30
// runes (rune-safe), and the surviving title is carried on the Classification.
func TestClassifyRequest_TitleTruncatedTo30Runes(t *testing.T) {
	// 50-char title + surrounding whitespace; must come back ≤30 runes, trimmed.
	const long = "   This Title Is Far Too Long To Fit In A Pane Header   "
	out := `{"kind":"answer","content":"ok","title":` + jsonQuote(long) + `}`
	cls, err, _ := runClassify(t, sampleClassifyRequest(), out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if n := len([]rune(cls.Title)); n > 30 {
		t.Errorf("title len = %d runes, want ≤30 (title=%q)", n, cls.Title)
	}
	if strings.TrimSpace(cls.Title) != cls.Title {
		t.Errorf("title has surrounding whitespace: %q", cls.Title)
	}
	if !strings.HasPrefix(cls.Title, "This Title Is") {
		t.Errorf("title = %q, want the leading words preserved", cls.Title)
	}
}

// A multi-byte title is truncated on a RUNE boundary, never mid-rune.
func TestTruncateTitle_RuneSafe(t *testing.T) {
	// 40 accented runes (2 bytes each in UTF-8): a byte-based cut would corrupt one.
	got := truncateTitle(strings.Repeat("é", 40))
	if n := len([]rune(got)); n != 30 {
		t.Errorf("rune count = %d, want 30", n)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated title is not valid UTF-8: %q", got)
	}
}

// jsonQuote returns s as a JSON string literal (so the test embeds arbitrary
// titles into the fake harness output safely).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// runClassify drives ClassifyRequest against a fake harness emitting resultText,
// capturing the owned argv so the triage-model + no-MCP invariants can be asserted.
// triageModel sets cfg [agent].triage_model (empty → leave the default).
func runClassify(t *testing.T, req capture.Request, resultText, triageModel string) (Classification, error, []string) {
	t.Helper()
	bin := fakeMetadataHarness(t, resultText)
	cfg := config.Default()
	cfg.Agent.Harness = "claude"
	cfg.Agent.Model = "opus" // the authoring model — classify must NOT use this
	if triageModel != "" {
		cfg.Agent.TriageModel = triageModel
	}

	var gotArgs []string
	cls, err := ClassifyRequest(req, AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: "/tmp/should-be-ignored.json", // classify must drop this
		Command: func(b string, args []string) *exec.Cmd {
			gotArgs = args
			return exec.Command(bin, args...)
		},
	})
	return cls, err, gotArgs
}

// modelArg returns the value following --model in the owned argv, or "".
func modelArg(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--model" {
			return args[i+1]
		}
	}
	return ""
}

// A clean command JSON parses; the call runs on the TRIAGE model (not the
// authoring model) and attaches NO --mcp-config.
func TestClassifyRequest_ParsesCommand(t *testing.T) {
	const out = `{"kind":"command","content":"git log -3 --since='1 week ago'"}`
	cls, err, args := runClassify(t, sampleClassifyRequest(), out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if cls.Kind != KindCommand {
		t.Errorf("kind = %q, want command", cls.Kind)
	}
	if cls.Content != "git log -3 --since='1 week ago'" {
		t.Errorf("content = %q", cls.Content)
	}
	// The triage model default is "haiku"; the authoring model "opus" must NOT leak.
	if m := modelArg(args); m != "haiku" {
		t.Errorf("--model = %q, want haiku (the triage model)", m)
	}
	if strings.Contains(strings.Join(args, "\x00"), "--mcp-config") {
		t.Errorf("classify must NOT attach --mcp-config: %v", args)
	}
}

// A configured triage_model is what the classify argv carries.
func TestClassifyRequest_UsesConfiguredTriageModel(t *testing.T) {
	const out = `{"kind":"answer","content":"Use git log."}`
	_, err, args := runClassify(t, sampleClassifyRequest(), out, "claude-3-5-haiku-latest")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if m := modelArg(args); m != "claude-3-5-haiku-latest" {
		t.Errorf("--model = %q, want the configured triage model", m)
	}
}

// The failed-command GUARD: a "command" whose content equals the failed command
// (up to whitespace) is downgraded to escalate — never re-type the failure.
func TestClassifyRequest_FailedCommandGuard(t *testing.T) {
	req := sampleClassifyRequest()
	req.Command = "make build"
	req.Exit = "2"
	// The model parrots the failed command back (with extra spacing).
	const out = `{"kind":"command","content":"make   build"}`
	cls, err, _ := runClassify(t, req, out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if cls.Kind != KindEscalate {
		t.Errorf("kind = %q, want escalate (failed-command guard)", cls.Kind)
	}
	if cls.Content != "" {
		t.Errorf("escalate content = %q, want empty", cls.Content)
	}
}

// An unknown kind normalizes to escalate.
func TestClassifyRequest_UnknownKind(t *testing.T) {
	const out = `{"kind":"banana","content":"whatever"}`
	cls, err, _ := runClassify(t, sampleClassifyRequest(), out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if cls.Kind != KindEscalate {
		t.Errorf("kind = %q, want escalate (unknown normalized)", cls.Kind)
	}
}

// The triage classify uses the BARE quick-model argv: --system-prompt (REPLACE,
// not --append-system-prompt), --strict-mcp-config, and
// --exclude-dynamic-system-prompt-sections, with no --mcp-config.
func TestClassifyRequest_BareArgv(t *testing.T) {
	const out = `{"kind":"answer","content":"ok"}`
	_, err, args := runClassify(t, sampleClassifyRequest(), out, "")
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	has := func(tok string) bool {
		for _, a := range args {
			if a == tok {
				return true
			}
		}
		return false
	}
	if !has("--system-prompt") || has("--append-system-prompt") {
		t.Errorf("bare classify must use --system-prompt (replace), not --append-system-prompt: %v", args)
	}
	if !has("--strict-mcp-config") || !has("--exclude-dynamic-system-prompt-sections") {
		t.Errorf("bare classify must add --strict-mcp-config + --exclude-dynamic-system-prompt-sections: %v", args)
	}
	if has("--mcp-config") {
		t.Errorf("bare classify must NOT attach --mcp-config: %v", args)
	}
}

// fakeStreamHarness writes a fake claude emitting each string in deltas as a
// stream-json text_delta event (and NO result line → the deltas fallback supplies
// the body), so the OnText live-tap can be exercised end to end.
func fakeStreamHarness(t *testing.T, deltas []string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncat <<'NDJSON'\n")
	for _, d := range deltas {
		line, err := json.Marshal(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]any{"type": "text_delta", "text": d},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString("NDJSON\n")
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-claude-stream")
	if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// OnText is called with the ACCUMULATING assistant text as each delta arrives.
func TestClassifyRequest_OnTextAccumulates(t *testing.T) {
	deltas := []string{`{"kind":`, `"answer",`, `"content":"ok"}`}
	bin := fakeStreamHarness(t, deltas)
	cfg := config.Default()
	cfg.Agent.Harness = "claude"

	var got []string
	cls, err := ClassifyRequest(sampleClassifyRequest(), AuthorOptions{
		Cfg:    cfg,
		OnText: func(acc string) { got = append(got, acc) },
		Command: func(b string, args []string) *exec.Cmd {
			return exec.Command(bin, args...)
		},
	})
	if err != nil {
		t.Fatalf("ClassifyRequest: %v", err)
	}
	if cls.Kind != KindAnswer {
		t.Errorf("kind = %q, want answer", cls.Kind)
	}
	want := []string{
		`{"kind":`,
		`{"kind":"answer",`,
		`{"kind":"answer","content":"ok"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("OnText calls = %d (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("OnText[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A non-JSON response fails both attempts → escalate + a clear error.
func TestClassifyRequest_NonJSONEscalatesWithError(t *testing.T) {
	cls, err, _ := runClassify(t, sampleClassifyRequest(), "Sorry, I can't help right now.", "")
	if err == nil {
		t.Fatal("expected an error for non-JSON output")
	}
	if cls.Kind != KindEscalate {
		t.Errorf("kind = %q, want escalate on parse failure", cls.Kind)
	}
	if !strings.Contains(err.Error(), "classification failed after retry") {
		t.Errorf("error = %q, want a classification-failed-after-retry message", err)
	}
}
